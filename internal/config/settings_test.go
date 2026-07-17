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

func TestSchemaTwoMigratesGameSettingsAndValidatesCustomExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"schemaVersion":2,"window":{"x":1,"y":2,"width":1100,"height":720},"input":{"mode":0,"triggerKey":119,"outputKey":70,"stopKey":123,"intervalMs":50},"game":{"path":"  C:\\\\Games\\\\原神  ","customExecutable":"Custom.exe"}}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Settings.SchemaVersion != CurrentSchemaVersion || loaded.Settings.Game.CustomExecutable != "Custom.exe" || loaded.Settings.Game.Path != `C:\\Games\\原神` {
		t.Fatalf("migrated settings = %+v", loaded.Settings)
	}
	settings := Default()
	settings.Game.CustomExecutable = `..\\evil.exe`
	if err := Save(filepath.Join(t.TempDir(), "bad.json"), settings); err == nil {
		t.Fatal("path-like custom executable accepted")
	}
}

func TestSchemaThreeMigratesLaunchDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"schemaVersion":3,"window":{"x":1,"y":2,"width":1100,"height":720},"input":{"mode":0,"triggerKey":119,"outputKey":70,"stopKey":123,"intervalMs":50},"game":{}}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Settings.SchemaVersion != CurrentSchemaVersion || loaded.Settings.Launch.Width != 1920 || loaded.Settings.Launch.Height != 1080 {
		t.Fatalf("migrated settings = %+v", loaded.Settings)
	}
}

func TestLoadAcceptsUTF8BOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"schemaVersion":4,"window":{"x":1,"y":2,"width":1100,"height":720},"input":{"mode":0,"triggerKey":119,"outputKey":70,"stopKey":123,"intervalMs":50},"game":{},"launch":{"width":1920,"height":1080}}`)
	data = append([]byte{0xEF, 0xBB, 0xBF}, data...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RecoveredFrom != "" || loaded.Settings.Window.X != 1 || loaded.Settings.Launch.Width != 1920 || loaded.Settings.Input.IntervalMS != 50 {
		t.Fatalf("BOM settings = %+v", loaded)
	}
}

func TestSchemaSixMigratesSafeInjectionDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"schemaVersion":6,"window":{"width":1100,"height":720},"input":{"mode":0,"triggerKey":119,"outputKey":70,"stopKey":123,"intervalMs":50},"launch":{"width":1920,"height":1080}}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Settings.SchemaVersion != CurrentSchemaVersion || loaded.Settings.Injection.Enabled || loaded.Settings.Injection.RiskAcknowledged || !loaded.Settings.Injection.ElevatedHelper {
		t.Fatalf("migrated injection settings = %+v", loaded.Settings.Injection)
	}
}

func TestSchemaSevenMigratesSafePluginDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"schemaVersion":7,"window":{"width":1100,"height":720},"input":{"mode":0,"triggerKey":119,"outputKey":70,"stopKey":123,"intervalMs":50},"launch":{"width":1920,"height":1080},"injection":{"elevatedHelper":true,"helperTimeoutMs":15000,"remoteTimeoutMs":5000}}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Settings.SchemaVersion != CurrentSchemaVersion || !loaded.Settings.Plugins.SafeMode || loaded.Settings.Plugins.CatalogURL != "" {
		t.Fatalf("migrated plugin settings = %+v", loaded.Settings.Plugins)
	}
}
