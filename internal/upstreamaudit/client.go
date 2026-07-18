package upstreamaudit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maxAPIResponseBytes  = 32 << 20
	maxComparisonCommits = 10_000
	githubAPIVersion     = "2022-11-28"
)

type Client struct {
	HTTP    *http.Client
	BaseURL string
	Token   string
}

type Commit struct {
	SHA          string `json:"sha"`
	Message      string `json:"message"`
	CommittedUTC string `json:"committedUtc"`
}

type Comparison struct {
	Base          string
	Head          string
	HeadCommitUTC string
	Status        string
	TotalCommits  int
	Commits       []Commit
	Files         []FileChange
}

type githubCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message   string `json:"message"`
		Committer struct {
			Date string `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

type githubComparison struct {
	Status       string         `json:"status"`
	AheadBy      int            `json:"ahead_by"`
	BehindBy     int            `json:"behind_by"`
	TotalCommits int            `json:"total_commits"`
	BaseCommit   githubCommit   `json:"base_commit"`
	Commits      []githubCommit `json:"commits"`
	Files        []struct {
		Filename         string `json:"filename"`
		PreviousFilename string `json:"previous_filename"`
		Status           string `json:"status"`
		Additions        int    `json:"additions"`
		Deletions        int    `json:"deletions"`
		Patch            string `json:"patch"`
	} `json:"files"`
}

func (client Client) Compare(ctx context.Context, lock Lock) (Comparison, error) {
	if err := lock.Validate(); err != nil {
		return Comparison{}, err
	}
	baseURL := strings.TrimRight(client.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	parsedBase, err := url.Parse(baseURL)
	if err != nil || parsedBase.Host == "" || parsedBase.RawQuery != "" || parsedBase.Fragment != "" {
		return Comparison{}, errors.New("GitHub API base URL is invalid")
	}
	loopbackHTTP := parsedBase.Scheme == "http" && (parsedBase.Hostname() == "127.0.0.1" || parsedBase.Hostname() == "localhost") && client.Token == ""
	if parsedBase.Scheme != "https" && !loopbackHTTP {
		return Comparison{}, errors.New("GitHub API base URL must use HTTPS")
	}
	httpClient := client.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	requester := apiRequester{client: httpClient, baseURL: baseURL, token: client.Token}
	var head githubCommit
	if err := requester.get(ctx, "/repos/"+lock.Owner+"/"+lock.Name+"/commits/"+url.PathEscape(lock.Branch), nil, &head); err != nil {
		return Comparison{}, fmt.Errorf("read upstream head: %w", err)
	}
	if !shaPattern.MatchString(head.SHA) || !validRFC3339(head.Commit.Committer.Date) {
		return Comparison{}, errors.New("upstream head response is incomplete")
	}
	comparison := Comparison{Base: lock.Commit, Head: head.SHA, HeadCommitUTC: head.Commit.Committer.Date}
	for page := 1; ; page++ {
		var response githubComparison
		query := url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}
		endpoint := "/repos/" + lock.Owner + "/" + lock.Name + "/compare/" + lock.Commit + "..." + head.SHA
		if err := requester.get(ctx, endpoint, query, &response); err != nil {
			return Comparison{}, fmt.Errorf("compare upstream commits page %d: %w", page, err)
		}
		if page == 1 {
			if response.BaseCommit.SHA != lock.Commit || response.TotalCommits < 0 || response.TotalCommits > maxComparisonCommits {
				return Comparison{}, errors.New("upstream comparison base or commit count is invalid")
			}
			validAhead := response.Status == "ahead" && response.BehindBy == 0 && response.AheadBy == response.TotalCommits && response.TotalCommits > 0
			validIdentical := response.Status == "identical" && response.AheadBy == 0 && response.BehindBy == 0 && response.TotalCommits == 0 && head.SHA == lock.Commit
			if !validAhead && !validIdentical {
				return Comparison{}, errors.New("upstream baseline is not an ancestor of the configured branch head")
			}
			if len(response.Files) >= 300 {
				return Comparison{}, errors.New("upstream comparison reached GitHub's 300-file cap; manual audit is required")
			}
			comparison.Status, comparison.TotalCommits = response.Status, response.TotalCommits
			for _, file := range response.Files {
				if strings.TrimSpace(file.Filename) == "" || file.Additions < 0 || file.Deletions < 0 {
					return Comparison{}, errors.New("upstream comparison contains an invalid file entry")
				}
				comparison.Files = append(comparison.Files, FileChange{Path: file.Filename, Previous: file.PreviousFilename, Status: file.Status, Patch: file.Patch, Added: file.Additions, Deleted: file.Deletions})
			}
		}
		for _, commit := range response.Commits {
			if !shaPattern.MatchString(commit.SHA) || !validRFC3339(commit.Commit.Committer.Date) {
				return Comparison{}, errors.New("upstream comparison contains an invalid commit")
			}
			comparison.Commits = append(comparison.Commits, Commit{SHA: commit.SHA, Message: commit.Commit.Message, CommittedUTC: commit.Commit.Committer.Date})
		}
		if len(comparison.Commits) >= comparison.TotalCommits {
			break
		}
		if len(response.Commits) == 0 {
			return Comparison{}, errors.New("upstream comparison pagination ended early")
		}
	}
	if len(comparison.Commits) != comparison.TotalCommits {
		return Comparison{}, errors.New("upstream comparison commit count is incomplete")
	}
	if comparison.TotalCommits == 0 && comparison.Head != comparison.Base {
		return Comparison{}, errors.New("upstream head differs but comparison contains no commits")
	}
	for index := range comparison.Files {
		comparison.Files[index].Commit = comparison.Head
	}
	return comparison, nil
}

type apiRequester struct {
	client  *http.Client
	baseURL string
	token   string
}

func (requester apiRequester) get(ctx context.Context, endpoint string, query url.Values, destination any) error {
	requestURL := requester.baseURL + endpoint
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("User-Agent", "GenshinTools-UpstreamAudit/1")
	if requester.token != "" {
		request.Header.Set("Authorization", "Bearer "+requester.token)
	}
	response, err := requester.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponseBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxAPIResponseBytes {
		return errors.New("GitHub API response exceeds 32 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("GitHub API response contains trailing JSON")
	}
	return nil
}

func validRFC3339(value string) bool {
	_, err := time.Parse(time.RFC3339, value)
	return err == nil
}
