package upstreamaudit

import (
	"strings"
	"testing"
)

const validLock = `{
  "schemaVersion": 1,
  "repository": "https://github.com/FufuLauncher/FufuLauncher.git",
  "owner": "FufuLauncher",
  "name": "FufuLauncher",
  "branch": "master",
  "commit": "b5a050ebd319341bddc4189491c90c22162d33fa",
  "commitTimeUtc": "2026-07-16T12:14:09Z",
  "checkedAtUtc": "2026-07-17T04:08:18Z",
  "scopePolicy": "scope-v2",
  "mode": "audit-only",
  "notes": "fixture"
}`

func TestLoadLockRejectsUnknownFieldsAndRepositoryChanges(t *testing.T) {
	if _, err := LoadLock(strings.NewReader(validLock)); err != nil {
		t.Fatal(err)
	}
	unknown := strings.Replace(validLock, `"notes": "fixture"`, `"notes": "fixture", "surprise": true`, 1)
	if _, err := LoadLock(strings.NewReader(unknown)); err == nil {
		t.Fatal("unknown lock field accepted")
	}
	changed := strings.Replace(validLock, `"owner": "FufuLauncher"`, `"owner": "attacker"`, 1)
	if _, err := LoadLock(strings.NewReader(changed)); err == nil {
		t.Fatal("changed repository identity accepted")
	}
}

func TestLoadLockRejectsOversizedInput(t *testing.T) {
	if _, err := LoadLock(strings.NewReader(strings.Repeat(" ", maxLockBytes+1))); err == nil {
		t.Fatal("oversized upstream lock was accepted")
	}
}

func TestClassifyScopeExclusionDependencyAndReview(t *testing.T) {
	changes := Classify([]FileChange{
		{Commit: strings.Repeat("a", 40), Path: "src/GameLauncherService.cs", Status: "modified"},
		{Commit: strings.Repeat("b", 40), Path: "src/Hoyolab/CheckinService.cs", Status: "modified"},
		{Commit: strings.Repeat("c", 40), Path: "src/GameLauncherService.cs", Status: "modified", Patch: "+ LoginAccount(accountToken)"},
		{Commit: strings.Repeat("d", 40), Path: "assets/FPS.dll", Status: "modified"},
	})
	want := map[string]Classification{
		"src/GameLauncherService.cs":    InScope,
		"src/Hoyolab/CheckinService.cs": Excluded,
		"assets/FPS.dll":                ReviewRequired,
	}
	seenDependency := false
	for _, change := range changes {
		if change.Commit == strings.Repeat("c", 40) {
			seenDependency = change.Classification == DependencyRisk
			continue
		}
		if expected, ok := want[change.Path]; ok && change.Classification != expected {
			t.Fatalf("%s classified as %s, want %s", change.Path, change.Classification, expected)
		}
	}
	if !seenDependency {
		t.Fatal("cross-scope account dependency was not classified as dependency risk")
	}
}

func TestClassifyIsDeterministicallySorted(t *testing.T) {
	changes := Classify([]FileChange{{Commit: "b", Path: "z/unknown.txt"}, {Commit: "a", Path: "a/unknown.txt"}})
	if changes[0].Path != "a/unknown.txt" || changes[1].Path != "z/unknown.txt" {
		t.Fatalf("changes are not stable: %+v", changes)
	}
}

func TestClassifyUpdateMetadataAsSelfUpdate(t *testing.T) {
	changes := Classify([]FileChange{{Path: "FufuLauncher/FufuLauncher.csproj", Status: "modified", Patch: "+ <FileVersion>1.4.3.0</FileVersion>"}})
	if changes[0].Module != "self-update" || changes[0].Classification != ReviewRequired {
		t.Fatalf("update metadata classification=%+v", changes[0])
	}
}
