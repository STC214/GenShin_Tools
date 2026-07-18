package upstreamaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDispositionRequiresEveryReviewGateAndReference(t *testing.T) {
	report, disposition := dispositionFixture(t)
	disposition.Items[1].BinaryReviewed = false
	if err := ValidateDisposition(report, disposition); err == nil {
		t.Fatal("incomplete manual review gates were accepted")
	}
	disposition.Items[1].BinaryReviewed = true
	disposition.Items = disposition.Items[:1]
	if err := ValidateDisposition(report, disposition); err == nil {
		t.Fatal("partial disposition was accepted")
	}
}

func TestUpdateBaselineIsAtomicAndRejectsConcurrentChange(t *testing.T) {
	report, disposition := dispositionFixture(t)
	original := []byte(validLock + "\n")
	path := filepath.Join(t.TempDir(), "upstream.lock.json")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := LoadLock(strings.NewReader(validLock))
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateBaseline(path, original, lock, report, disposition); err != nil {
		t.Fatal(err)
	}
	updatedFile, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	updated, loadErr := LoadLock(updatedFile)
	_ = updatedFile.Close()
	if loadErr != nil || updated.Commit != report.Head || updated.CheckedAtUTC != disposition.ReviewedUTC {
		t.Fatalf("updated lock=%+v err=%v", updated, loadErr)
	}
	if err := os.WriteFile(path, []byte(validLock+" \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UpdateBaseline(path, original, lock, report, disposition); err == nil || !strings.Contains(err.Error(), "changed after") {
		t.Fatalf("concurrent lock change was not rejected: %v", err)
	}
}

func dispositionFixture(t *testing.T) (Report, Disposition) {
	t.Helper()
	lock, err := LoadLock(strings.NewReader(validLock))
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("e", 40)
	report := Report{
		SchemaVersion: 1,
		Repository:    lock.Repository,
		Branch:        lock.Branch,
		ScopePolicy:   lock.ScopePolicy,
		Base:          lock.Commit,
		Head:          head,
		HeadCommitUTC: "2026-07-18T00:00:00Z",
		Changes: []Change{
			{FileChange: FileChange{Path: "src/GameLauncherService.cs"}, Classification: InScope},
			{FileChange: FileChange{Path: "src/App.csproj"}, Classification: ReviewRequired},
			{FileChange: FileChange{Path: "src/Hoyolab.cs"}, Classification: Excluded},
		},
	}
	disposition := Disposition{
		SchemaVersion: 1,
		Base:          report.Base,
		Head:          report.Head,
		Reviewer:      "Fixture Reviewer",
		ReviewedUTC:   "2026-07-18T01:00:00Z",
		Items: []DispositionItem{
			{Path: "src/GameLauncherService.cs", Classification: InScope, Decision: "implemented", References: []string{"internal/launch"}, ExclusionsReviewed: true},
			{Path: "src/App.csproj", Classification: ReviewRequired, Decision: "no_action_required", References: []string{"docs/review.md"}, BinaryReviewed: true, SourceAndLicenseReviewed: true, APISchemaReviewed: true, ExclusionsReviewed: true},
		},
	}
	return report, disposition
}
