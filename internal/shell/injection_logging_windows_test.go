package shell

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"genshintools/internal/diagnostics"
)

func TestInjectionLaunchFailureIsPersisted(t *testing.T) {
	directory := t.TempDir()
	logger, err := diagnostics.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	app := application{logger: logger}
	app.logInjectionFailure(injectionUpdate{taskID: 42, kind: 1, err: "helper failed: fixture"})
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "genshin-tools.log"))
	if err != nil {
		t.Fatal(err)
	}
	var entry diagnostics.Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Level != "error" || entry.Message != "injection task failed" ||
		entry.Fields["error"] != "helper failed: fixture" || entry.Fields["kind"] != "launch" || entry.Fields["taskID"] != float64(42) {
		t.Fatalf("injection failure log = %+v", entry)
	}
}
