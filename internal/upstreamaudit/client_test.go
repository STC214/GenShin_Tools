package upstreamaudit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientCompareAndWriteDeterministicReport(t *testing.T) {
	lock, err := LoadLock(strings.NewReader(validLock))
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("a", 40)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.URL.Path == "/repos/FufuLauncher/FufuLauncher/commits/master":
			_ = json.NewEncoder(response).Encode(map[string]any{"sha": head, "commit": map[string]any{"message": "head", "committer": map[string]any{"date": "2026-07-18T00:00:00Z"}}})
		case strings.Contains(request.URL.Path, "/compare/"):
			_ = json.NewEncoder(response).Encode(map[string]any{
				"status": "ahead", "ahead_by": 1, "behind_by": 0, "total_commits": 1,
				"base_commit": map[string]any{"sha": lock.Commit},
				"commits":     []any{map[string]any{"sha": head, "commit": map[string]any{"message": "change launcher", "committer": map[string]any{"date": "2026-07-18T00:00:00Z"}}}},
				"files":       []any{map[string]any{"filename": "src/GameLauncherService.cs", "status": "modified", "additions": 2, "deletions": 1, "patch": "+ launch"}},
			})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	comparison, err := (Client{HTTP: server.Client(), BaseURL: server.URL}).Compare(context.Background(), lock)
	if err != nil {
		t.Fatal(err)
	}
	report, err := BuildReport(lock, comparison)
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts[string(InScope)] != 1 || report.TotalCommits != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	first, err := WriteReport(filepath.Join(t.TempDir(), "reports"), report)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := WriteReport(filepath.Dir(first), report)
	if err != nil || repeated != first {
		t.Fatalf("identical report was not idempotent: path=%s err=%v", repeated, err)
	}
	if err := os.Remove(filepath.Join(first, "disposition.template.json")); err != nil {
		t.Fatal(err)
	}
	if migrated, err := WriteReport(filepath.Dir(first), report); err != nil || migrated != first {
		t.Fatalf("old report migration failed: path=%s err=%v", migrated, err)
	}
	second, err := WriteReport(filepath.Join(t.TempDir(), "reports"), report)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"changes.json", "commits.json", "disposition.template.json", "summary.md"} {
		left, _ := os.ReadFile(filepath.Join(first, name))
		right, _ := os.ReadFile(filepath.Join(second, name))
		if string(left) != string(right) {
			t.Fatalf("%s is not deterministic", name)
		}
	}
}

func TestClientFailsClosedOnPaginationTruncationAndFileCap(t *testing.T) {
	lock, err := LoadLock(strings.NewReader(validLock))
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("b", 40)
	mode := "truncated"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.Path, "/commits/master") {
			_ = json.NewEncoder(response).Encode(map[string]any{"sha": head, "commit": map[string]any{"committer": map[string]any{"date": "2026-07-18T00:00:00Z"}}})
			return
		}
		files := []any{}
		commits := []any{}
		total := 2
		if mode == "file-cap" {
			total = 1
			commits = append(commits, map[string]any{"sha": head, "commit": map[string]any{"committer": map[string]any{"date": "2026-07-18T00:00:00Z"}}})
			for index := 0; index < 300; index++ {
				files = append(files, map[string]any{"filename": "src/file" + string(rune('a'+index%26)), "status": "modified", "additions": 1, "deletions": 0})
			}
		} else if request.URL.Query().Get("page") == "1" {
			commits = append(commits, map[string]any{"sha": strings.Repeat("c", 40), "commit": map[string]any{"committer": map[string]any{"date": "2026-07-18T00:00:00Z"}}})
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"status": "ahead", "ahead_by": total, "behind_by": 0, "total_commits": total, "base_commit": map[string]any{"sha": lock.Commit}, "commits": commits, "files": files})
	}))
	defer server.Close()
	client := Client{HTTP: server.Client(), BaseURL: server.URL}
	if _, err := client.Compare(context.Background(), lock); err == nil || !strings.Contains(err.Error(), "pagination ended early") {
		t.Fatalf("truncated pagination was not rejected: %v", err)
	}
	mode = "file-cap"
	if _, err := client.Compare(context.Background(), lock); err == nil || !strings.Contains(err.Error(), "300-file cap") {
		t.Fatalf("file cap was not rejected: %v", err)
	}
}

func TestClientRejectsRateLimit(t *testing.T) {
	lock, _ := LoadLock(strings.NewReader(validLock))
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	if _, err := (Client{HTTP: server.Client(), BaseURL: server.URL}).Compare(context.Background(), lock); err == nil || !strings.Contains(err.Error(), "HTTP 429") {
		t.Fatalf("rate limit was not rejected: %v", err)
	}
}
