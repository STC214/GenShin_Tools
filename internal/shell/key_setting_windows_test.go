package shell

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"genshintools/internal/diagnostics"
	"genshintools/internal/localization"
	"genshintools/internal/platform/win32"
	"genshintools/internal/plugins"
)

func TestKeyConfigUsesNamesAndPersistsLegacyCodes(t *testing.T) {
	field := plugins.ConfigField{Type: "key"}
	for _, test := range []struct{ code, label string }{
		{"121", "F10"}, {"65", "A"}, {"55", "7"}, {"48", "0"},
		{"33", "Page Up"}, {"34", "Page Down"}, {"105", "Num 9"},
		{"160", "Left Shift"}, {"162", "Left Ctrl"}, {"164", "Left Alt"},
		{"172", "Browser Home"}, {"173", "Volume Mute"}, {"175", "Volume Up"},
		{"179", "Media Play/Pause"}, {"194", "ABNT C2"}, {"226", "ISO Backslash"},
	} {
		label := configFieldDisplayValue(field, test.code)
		if label != test.label {
			t.Fatalf("display %s = %q, want %q", test.code, label, test.label)
		}
		stored, err := configFieldStoredValue(field, label)
		if err != nil || stored != test.code {
			t.Fatalf("store %q = %q, %v; want %s", label, stored, err, test.code)
		}
	}
	for name, want := range map[string]string{" f10 ": "121", "a": "65", "Esc": "27", "Insert": "45", "Space": "32", "121": "121"} {
		if got, err := configFieldStoredValue(field, name); err != nil || got != want {
			t.Errorf("store %q = %q, %v; want %s", name, got, err, want)
		}
	}
	for _, name := range []string{"", "256", "-1", "Unknown key", "Ctrl+F10", "F25"} {
		if _, err := configFieldStoredValue(field, name); err == nil {
			t.Errorf("invalid name %q accepted", name)
		}
	}
	for _, kind := range []string{"int", "float", "bool", "string"} {
		field.Type = kind
		if got := configFieldDisplayValue(field, "121"); got != "121" {
			t.Errorf("%s display changed: %q", kind, got)
		}
		if got, err := configFieldStoredValue(field, "121"); err != nil || got != "121" {
			t.Errorf("%s storage changed: %q, %v", kind, got, err)
		}
	}
}

func TestKeyConfigFunctionAndKeypadNames(t *testing.T) {
	field := plugins.ConfigField{Type: "key"}
	for code := uint32(0x70); code <= 0x87; code++ {
		label := win32.KeyName(code)
		if got, err := configFieldStoredValue(field, label); err != nil || got != strconv.FormatUint(uint64(code), 10) {
			t.Errorf("function key %q = %q, %v", label, got, err)
		}
	}
	for code := uint32(0x60); code <= 0x69; code++ {
		label := win32.KeyName(code)
		if got, err := configFieldStoredValue(field, label); err != nil || got != strconv.FormatUint(uint64(code), 10) {
			t.Errorf("keypad key %q = %q, %v", label, got, err)
		}
	}
}

func TestKeyConfigRecognizedNamesRoundTrip(t *testing.T) {
	field := plugins.ConfigField{Type: "key"}
	for code := uint32(1); code <= 255; code++ {
		if win32.KeyName(code) == "Unknown key" {
			continue
		}
		stored := strconv.FormatUint(uint64(code), 10)
		label := configFieldDisplayValue(field, stored)
		got, err := configFieldStoredValue(field, " "+strings.ToLower(label)+" ")
		if err != nil || got != stored {
			t.Errorf("code %s: label %q round-trips to %q, %v", stored, label, got, err)
		}
	}
}

func TestKeyConfigEditorDisplaysSavesAndRestoresNames(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	parent, err := win32.CreateControl("STATIC", "", 0, 0, 0, 800, 600, 0, 0, win32.ModuleHandle())
	if err != nil {
		t.Fatal(err)
	}
	defer win32.DestroyWindow(parent)
	modules := t.TempDir()
	directory := filepath.Join(modules, plugins.FufuMainTargetID)
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "config.ini")
	if err := os.WriteFile(path, []byte("; keep comment\r\n[Hotkey]\r\nValue = 121\r\nUnknown = keep\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	field := plugins.ConfigField{ID: "hotkey", Section: "Hotkey", Key: "Value", Name: "Hotkey", Type: "key", Default: "121"}
	logger, err := diagnostics.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	app := application{
		logger: logger,
		hwnd:   parent, instance: win32.ModuleHandle(), dpi: 96,
		texts: localization.New(localization.EN, ""), pluginLayout: plugins.Layout{Modules: modules},
		fufuTargetInstalled: true,
		fufuTarget: plugins.FufuTargetConfig{
			Schema:   plugins.ConfigSchema{SchemaVersion: 1, Fields: []plugins.ConfigField{field}},
			Settings: []plugins.FufuSetting{{Field: field}},
		},
	}
	app.rebuildFufuControls()
	control := app.fufuEdits[field.ID]
	if control == 0 || win32.GetWindowText(control) != "F10" {
		t.Fatalf("initial editor = %q, status = %s", win32.GetWindowText(control), app.pluginStatus)
	}
	t.Log("baseline input: Value = 121; editor: F10")
	if !app.saveFufuField(field.ID) {
		t.Fatal("unchanged value failed to save")
	}
	win32.SetWindowText(control, "f11")
	if !app.saveFufuField(field.ID) || win32.GetWindowText(control) != "F11" || app.fufuValues[field.ID] != "122" {
		t.Fatalf("save editor = %q, values = %v, status = %s", win32.GetWindowText(control), app.fufuValues, app.pluginStatus)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "Value = 122") || !strings.Contains(string(data), "Unknown = keep") || !strings.Contains(string(data), "; keep comment") {
		t.Fatalf("saved INI = %q, %v", data, err)
	}
	t.Log("modified input: f11; editor: F11; persisted: Value = 122; unknown fields and comments preserved")
	win32.SetWindowText(control, "not a key")
	if app.saveFufuField(field.ID) || win32.GetWindowText(control) != "F11" || app.fufuValues[field.ID] != "122" {
		t.Fatal("invalid key did not restore the previous name")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatalf("invalid input modified INI: %q, %v", after, err)
	}
	t.Log("invalid input: not a key; restored editor: F11; INI unchanged")
	app.rebuildFufuControls()
	if got := win32.GetWindowText(app.fufuEdits[field.ID]); got != "F11" {
		t.Fatalf("reopened editor = %q, want F11", got)
	}
	// A valid name must also roll back its display and memory on disk failure.
	retained := path + ".retained"
	if err := os.Rename(path, retained); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	control = app.fufuEdits[field.ID]
	win32.SetWindowText(control, "F12")
	if app.saveFufuField(field.ID) || win32.GetWindowText(control) != "F11" || app.fufuValues[field.ID] != "122" || app.pluginStatus == "" {
		t.Fatalf("disk failure did not restore name/memory: %q, %v, %s", win32.GetWindowText(control), app.fufuValues, app.pluginStatus)
	}
	if retainedData, err := os.ReadFile(retained); err != nil || string(retainedData) != string(data) {
		t.Fatalf("disk failure changed original INI: %q, %v", retainedData, err)
	}
	t.Log("disk failure: editor and memory restored to F11/122; original INI retained")
}
