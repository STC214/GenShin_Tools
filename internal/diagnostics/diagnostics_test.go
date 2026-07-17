package diagnostics

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoggerWritesStructuredJSON(t *testing.T) {
	directory := t.TempDir()
	logger, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("started", map[string]any{"answer": 42})
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(directory, "genshin-tools.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if !bufio.NewScanner(file).Scan() {
		t.Fatal("missing log line")
	}
	data, _ := os.ReadFile(filepath.Join(directory, "genshin-tools.log"))
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	if entry.Level != "info" || entry.Message != "started" || entry.Fields["answer"].(float64) != 42 {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestSessionMarkerDetectsPreviousUncleanExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.marker")
	previous, err := BeginSession(path, "1.0.0")
	if err != nil || previous {
		t.Fatalf("first BeginSession = %v, %v", previous, err)
	}
	previous, err = BeginSession(path, "1.0.0")
	if err != nil || !previous {
		t.Fatalf("second BeginSession = %v, %v", previous, err)
	}
	if err := EndSession(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("marker still exists: %v", err)
	}
}
