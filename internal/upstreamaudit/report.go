package upstreamaudit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Report struct {
	SchemaVersion int            `json:"schemaVersion"`
	Repository    string         `json:"repository"`
	Branch        string         `json:"branch"`
	ScopePolicy   string         `json:"scopePolicy"`
	Base          string         `json:"base"`
	Head          string         `json:"head"`
	HeadCommitUTC string         `json:"headCommitUtc"`
	Status        string         `json:"status"`
	TotalCommits  int            `json:"totalCommits"`
	Counts        map[string]int `json:"counts"`
	Commits       []Commit       `json:"commits"`
	Changes       []Change       `json:"changes"`
}

func BuildReport(lock Lock, comparison Comparison) (Report, error) {
	if comparison.Base != lock.Commit || !shaPattern.MatchString(comparison.Head) || comparison.TotalCommits != len(comparison.Commits) {
		return Report{}, errors.New("comparison does not match the upstream lock")
	}
	commits := append([]Commit(nil), comparison.Commits...)
	sort.Slice(commits, func(i, j int) bool { return commits[i].SHA < commits[j].SHA })
	changes := Classify(comparison.Files)
	counts := map[string]int{string(InScope): 0, string(Excluded): 0, string(ReviewRequired): 0, string(DependencyRisk): 0}
	for _, change := range changes {
		counts[string(change.Classification)]++
	}
	return Report{SchemaVersion: 1, Repository: lock.Repository, Branch: lock.Branch, ScopePolicy: lock.ScopePolicy, Base: comparison.Base, Head: comparison.Head, HeadCommitUTC: comparison.HeadCommitUTC, Status: comparison.Status, TotalCommits: comparison.TotalCommits, Counts: counts, Commits: commits, Changes: changes}, nil
}

func WriteReport(root string, report Report) (string, error) {
	if !shaPattern.MatchString(report.Base) || !shaPattern.MatchString(report.Head) {
		return "", errors.New("report base or head is invalid")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	name := report.Base[:12] + "_" + report.Head[:12]
	destination := filepath.Join(root, name)
	changesData, err := json.MarshalIndent(struct {
		SchemaVersion int            `json:"schemaVersion"`
		Base          string         `json:"base"`
		Head          string         `json:"head"`
		Counts        map[string]int `json:"counts"`
		Changes       []Change       `json:"changes"`
	}{report.SchemaVersion, report.Base, report.Head, report.Counts, report.Changes}, "", "  ")
	if err != nil {
		return "", err
	}
	commitsData, err := json.MarshalIndent(struct {
		SchemaVersion int      `json:"schemaVersion"`
		Base          string   `json:"base"`
		Head          string   `json:"head"`
		Commits       []Commit `json:"commits"`
	}{report.SchemaVersion, report.Base, report.Head, report.Commits}, "", "  ")
	if err != nil {
		return "", err
	}
	templateItems := make([]DispositionItem, 0, len(report.Changes))
	for _, change := range report.Changes {
		if change.Classification != Excluded {
			templateItems = append(templateItems, DispositionItem{Path: change.Path, Classification: change.Classification})
		}
	}
	templateData, err := json.MarshalIndent(Disposition{SchemaVersion: 1, Base: report.Base, Head: report.Head, Items: templateItems}, "", "  ")
	if err != nil {
		return "", err
	}
	files := map[string][]byte{
		"changes.json":              append(changesData, '\n'),
		"commits.json":              append(commitsData, '\n'),
		"disposition.template.json": append(templateData, '\n'),
		"summary.md":                []byte(summaryMarkdown(report)),
	}
	if _, err := os.Stat(destination); err == nil {
		for name, expected := range files {
			actual, readErr := os.ReadFile(filepath.Join(destination, name))
			if readErr != nil || !bytes.Equal(actual, expected) {
				return "", fmt.Errorf("existing report differs from current result: %s", destination)
			}
		}
		return destination, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	temporary, err := os.MkdirTemp(root, ".upstream-report-")
	if err != nil {
		return "", err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()
	for name, data := range files {
		if err := writeSynced(filepath.Join(temporary, name), data); err != nil {
			return "", err
		}
	}
	if err := os.Rename(temporary, destination); err != nil {
		return "", err
	}
	committed = true
	return destination, nil
}

func summaryMarkdown(report Report) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# FufuLauncher upstream audit\n\n- Base: `%s`\n- Head: `%s`\n- Head time: `%s`\n- Commits: %d\n\n", report.Base, report.Head, report.HeadCommitUTC, report.TotalCommits)
	for _, category := range []Classification{DependencyRisk, ReviewRequired, InScope, Excluded} {
		fmt.Fprintf(&builder, "## %s (%d)\n\n", category, report.Counts[string(category)])
		for _, change := range report.Changes {
			if change.Classification == category {
				fmt.Fprintf(&builder, "- `%s` — %s — %s\n", change.Path, change.Status, strings.Join(change.Rules, ", "))
			}
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}

func writeSynced(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}
