package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "config.json")
	want := Default()
	want.Window = WindowConfig{X: 30, Y: 40, Width: 1280, Height: 800}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	result, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.Settings != want || result.RecoveredFrom != "" {
		t.Fatalf("Load = %+v, want %+v", result, want)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".config-*.tmp")); len(matches) != 0 {
		t.Fatalf("temporary files left behind: %v", matches)
	}
}

func TestLoadQuarantinesCorruptSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(result.RecoveredFrom, ".corrupt-") {
		t.Fatalf("RecoveredFrom = %q", result.RecoveredFrom)
	}
	if result.Settings != Default() {
		t.Fatalf("Settings = %+v, want defaults", result.Settings)
	}
	if _, err := os.Stat(result.RecoveredFrom); err != nil {
		t.Fatalf("quarantined file: %v", err)
	}
}

func TestLoadMigratesSchemaZeroAndClampsSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"schemaVersion":0,"window":{"x":1,"y":2,"width":10,"height":20}}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.Settings.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("SchemaVersion = %d", result.Settings.SchemaVersion)
	}
	if result.Settings.Window.Width != Default().Window.Width || result.Settings.Window.Height != Default().Window.Height {
		t.Fatalf("Window = %+v", result.Settings.Window)
	}
}

func TestInputEnableIsNotRestoredAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	settings := Default()
	settings.Input.Enabled = true
	settings.Input.IntervalMS = 100
	settings.Input.Interval = 100 * 1e6
	if err := Save(path, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Settings.Input.Enabled {
		t.Fatal("input enhancement was automatically re-enabled")
	}
	if loaded.Settings.Input.IntervalMS != 100 {
		t.Fatalf("interval = %d, want 100", loaded.Settings.Input.IntervalMS)
	}
}
