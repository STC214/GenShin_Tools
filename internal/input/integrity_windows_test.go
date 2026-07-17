package input

import "testing"

func TestCurrentAndForegroundIntegrity(t *testing.T) {
	level, err := currentIntegrityLevel()
	if err != nil {
		t.Fatal(err)
	}
	if level == 0 || integrityName(level) == "untrusted" {
		t.Fatalf("unexpected current integrity RID %#x", level)
	}
	report := checkForegroundIntegrity()
	if report.SelfRID != level {
		t.Fatalf("report self RID = %#x, want %#x", report.SelfRID, level)
	}
}
