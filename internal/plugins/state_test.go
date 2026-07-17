package plugins

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStateEnableOrderAliasAndAtomicRoundTrip(t *testing.T) {
	state := DefaultState()
	if err := SetEnabled(&state, "fps", true); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(&state, "visual", true); err != nil {
		t.Fatal(err)
	}
	if err := Move(&state, "visual", -1); err != nil {
		t.Fatal(err)
	}
	if err := SetAlias(&state, "fps", "帧率工具"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := SaveState(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := EnabledInOrder(loaded.State, map[string]bool{"fps": true, "visual": true}); !reflect.DeepEqual(got, []string{"visual", "fps"}) {
		t.Fatalf("enabled order = %v", got)
	}
	if loaded.State.Aliases["fps"] != "帧率工具" {
		t.Fatalf("aliases = %v", loaded.State.Aliases)
	}
}

func TestInvalidStateIsQuarantined(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RecoveredFrom == "" || loaded.State.SchemaVersion != StateSchemaVersion {
		t.Fatalf("recovery = %+v", loaded)
	}
	if _, err := os.Stat(loaded.RecoveredFrom); err != nil {
		t.Fatal(err)
	}
}

func TestStateRejectsUnsafeAlias(t *testing.T) {
	state := DefaultState()
	if err := SetAlias(&state, "fps", "bad\nname"); err == nil {
		t.Fatal("multiline alias accepted")
	}
}
