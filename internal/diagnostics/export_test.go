package diagnostics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"genshintools/internal/buildinfo"
)

func TestExportOmitsLogFieldsAndRedactsMessagePaths(t *testing.T) {
	directory := t.TempDir()
	logPath := filepath.Join(directory, "app.log")
	line := `{"timeUtc":"2026-01-01T00:00:00Z","level":"error","message":"failed C:\\Users\\Alice\\game.exe https://example.test/?token=secret","fields":{"token":"secret-token","path":"C:\\private"}}`
	line = strings.ReplaceAll(line, `\"`, `"`) + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(directory, "report.json")
	if err := Export(destination, ExportInput{Build: buildinfo.Info{Version: "1.0.0"}, LogPath: logPath, Now: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{"Alice", "example.test", "secret-token", `C:\\private`, `"fields"`} {
		if strings.Contains(text, secret) {
			t.Fatalf("report leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "[redacted]") {
		t.Fatal("report did not retain a redaction marker")
	}
}

func TestExportAtomicallyReplacesExistingReport(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "report.json")
	if err := os.WriteFile(destination, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Export(destination, ExportInput{Build: buildinfo.Info{Version: "2.0.0"}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(destination)
	if !strings.Contains(string(data), `"version": "2.0.0"`) {
		t.Fatalf("destination was not replaced: %s", data)
	}
	matches, _ := filepath.Glob(filepath.Join(directory, ".genshin-tools-diagnostics-*.tmp"))
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}
