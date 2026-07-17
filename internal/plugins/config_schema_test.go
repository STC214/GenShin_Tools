package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigUpdateAndPresetPreserveUnknownLines(t *testing.T) {
	minimum, maximum := 30.0, 240.0
	schema := ConfigSchema{SchemaVersion: 1, Fields: []ConfigField{
		{ID: "enabled", Section: "FPS", Key: "Enabled", Name: "Enabled", Type: "bool", Default: "0"},
		{ID: "target", Section: "FPS", Key: "Target", Name: "Target", Type: "int", Default: "60", Min: &minimum, Max: &maximum},
	}, Presets: []ConfigPreset{{ID: "high", Name: "High", Values: map[string]string{"enabled": "1", "target": "144"}}}}
	path := filepath.Join(t.TempDir(), "config.ini")
	original := "; keep\r\n[General]\r\nUnknown = untouched\r\n\r\n[FPS]\r\nEnabled = 0\r\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPreset(path, schema, "high"); err != nil {
		t.Fatal(err)
	}
	values, err := ReadConfig(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	if values["enabled"] != "1" || values["target"] != "144" {
		t.Fatalf("values = %v", values)
	}
	data, _ := os.ReadFile(path)
	if !containsText(string(data), "; keep") || !containsText(string(data), "Unknown = untouched") {
		t.Fatalf("unknown lines lost: %s", data)
	}
	if err := UpdateConfig(path, schema, "target", "999"); err == nil {
		t.Fatal("out-of-range target accepted")
	}
}

func TestConfigSchemaRejectsDuplicatePhysicalField(t *testing.T) {
	schema := ConfigSchema{SchemaVersion: 1, Fields: []ConfigField{
		{ID: "one", Section: "A", Key: "Value", Name: "One", Type: "string"},
		{ID: "two", Section: "a", Key: "value", Name: "Two", Type: "string"},
	}}
	if err := validateConfigSchema(schema); err == nil {
		t.Fatal("duplicate INI target accepted")
	}
}

func containsText(value, target string) bool {
	for index := 0; index+len(target) <= len(value); index++ {
		if value[index:index+len(target)] == target {
			return true
		}
	}
	return false
}
