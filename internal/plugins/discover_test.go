package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverStrictPluginManifest(t *testing.T) {
	root := t.TempDir()
	writePluginFixture(t, root, "visual", Manifest{
		SchemaVersion: ManifestSchemaVersion,
		ID:            "visual", Name: "Visual", Developer: "Fixture", Description: "Visual fixture",
		Version: "1.2.3", Category: "visuals", Capabilities: []string{"visual"},
		SourceURL: "https://example.invalid/source", License: "MIT", ModuleFile: "module.json",
	})
	state := DefaultState()
	_ = SetEnabled(&state, "visual", true)
	items, warnings, err := Discover(root, state)
	if err != nil || len(warnings) != 0 || len(items) != 1 || !items[0].Enabled || items[0].Manifest.Version != "1.2.3" {
		t.Fatalf("items=%+v warnings=%v err=%v", items, warnings, err)
	}
}

func TestDiscoverRejectsExcludedCapabilityAndUnknownJSON(t *testing.T) {
	root := t.TempDir()
	manifest := Manifest{SchemaVersion: 1, ID: "bad", Name: "Bad", Developer: "Fixture", Description: "Bad fixture", Version: "1.0.0", Category: "other", Capabilities: []string{"account.login"}, SourceURL: "https://example.invalid/source", License: "MIT", ModuleFile: "module.json"}
	writePluginFixture(t, root, "bad", manifest)
	items, warnings, err := Discover(root, DefaultState())
	if err != nil || len(items) != 0 || len(warnings) != 1 {
		t.Fatalf("items=%v warnings=%v err=%v", items, warnings, err)
	}
	manifest.Capabilities = []string{"visual"}
	data, _ := json.Marshal(manifest)
	data = append(data[:len(data)-1], []byte(`,"unexpected":true}`)...)
	if err := os.WriteFile(filepath.Join(root, "bad", "plugin.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	items, warnings, err = Discover(root, DefaultState())
	if err != nil || len(items) != 0 || len(warnings) != 1 {
		t.Fatalf("unknown JSON accepted: items=%v warnings=%v err=%v", items, warnings, err)
	}
}

func writePluginFixture(t *testing.T, root, id string, manifest Manifest) {
	t.Helper()
	if len(manifest.Files) == 0 {
		manifest.Files = []PackageFile{{Path: "module.json", Size: 2, SHA256: "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"}}
	}
	directory := filepath.Join(root, id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "plugin.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "module.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
}
