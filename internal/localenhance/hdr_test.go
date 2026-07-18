package localenhance

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

type fakeRegistry struct {
	values map[string]RegistryValue
	failOn string
}

func (f *fakeRegistry) Read(name string) (RegistryValue, error) {
	value, exists := f.values[name]
	if !exists {
		return RegistryValue{}, nil
	}
	value.Data = append([]byte(nil), value.Data...)
	return value, nil
}

func (f *fakeRegistry) Write(name string, value RegistryValue) error {
	if name == f.failOn {
		f.failOn = ""
		return errors.New("injected registry failure")
	}
	if !value.Exists {
		delete(f.values, name)
		return nil
	}
	value.Data = append([]byte(nil), value.Data...)
	f.values[name] = value
	return nil
}

func TestHDRApplyPreservesJSONAndRollback(t *testing.T) {
	state := make([]byte, 4)
	binary.LittleEndian.PutUint32(state, 0)
	originalGeneral := []byte(`{"customSetting":"keep","maxLuminosity":900,"scenePaperWhite":250,"uiPaperWhite":300}` + "\x00")
	store := &fakeRegistry{values: map[string]RegistryValue{
		HDRStateName:    {Exists: true, Kind: registry.DWORD, Data: state},
		GeneralDataName: {Exists: true, Kind: registry.BINARY, Data: originalGeneral},
	}}
	snapshot, err := ApplyHDR(store, HDRConfig{Enabled: true, MaxLuminance: 1200, SceneLuminance: 320, UILuminance: 380})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(store.values[GeneralDataName].Data), `"customSetting":"keep"`) {
		t.Fatalf("unknown JSON field was lost: %s", store.values[GeneralDataName].Data)
	}
	if binary.LittleEndian.Uint32(store.values[HDRStateName].Data) != 1 {
		t.Fatal("HDR state was not enabled")
	}
	if err := RestoreHDR(store, snapshot); err != nil {
		t.Fatal(err)
	}
	if string(store.values[GeneralDataName].Data) != string(originalGeneral) {
		t.Fatal("HDR restore did not reproduce original bytes")
	}
}

func TestHDRSecondWriteFailureRestoresBothValues(t *testing.T) {
	store := &fakeRegistry{values: map[string]RegistryValue{}, failOn: HDRStateName}
	_, err := ApplyHDR(store, DefaultHDRConfig())
	if err == nil {
		t.Fatal("injected registry failure succeeded")
	}
	if len(store.values) != 0 {
		t.Fatalf("registry values remained after rollback: %+v", store.values)
	}
}

func TestHDRWrongRegistryTypeFailsBeforeWrite(t *testing.T) {
	store := &fakeRegistry{values: map[string]RegistryValue{HDRStateName: {Exists: true, Kind: registry.SZ, Data: []byte("bad")}}}
	if _, err := ApplyHDR(store, DefaultHDRConfig()); err == nil {
		t.Fatal("wrong registry type was overwritten")
	}
	if store.values[HDRStateName].Kind != registry.SZ {
		t.Fatal("wrong registry type was mutated")
	}
}

func TestHDRRejectsTrailingRegistryJSON(t *testing.T) {
	store := &fakeRegistry{values: map[string]RegistryValue{
		GeneralDataName: {Exists: true, Kind: registry.BINARY, Data: []byte(`{"maxLuminosity":900} {}`)},
	}}
	if _, _, err := ReadHDR(store); err == nil {
		t.Fatal("trailing HDR registry JSON was accepted")
	}
}

func TestExecutableFromCommand(t *testing.T) {
	for command, want := range map[string]string{
		`"C:\Program Files\BetterGI\BetterGI.exe" "%1"`: `C:\Program Files\BetterGI\BetterGI.exe`,
		`C:\BetterGI\BetterGI.exe %1`:                   `C:\BetterGI\BetterGI.exe`,
	} {
		got, err := executableFromCommand(command)
		if err != nil || got != want {
			t.Fatalf("executableFromCommand(%q) = %q, %v", command, got, err)
		}
	}
}

func TestHDRBackupIsWrittenBeforeApplyAndRestorable(t *testing.T) {
	store := &fakeRegistry{values: map[string]RegistryValue{}}
	backup := filepath.Join(t.TempDir(), "hdr.json")
	if err := ApplyHDRWithBackup(store, DefaultHDRConfig(), backup); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatal(err)
	}
	if err := RestoreHDRBackup(store, backup); err != nil {
		t.Fatal(err)
	}
	if len(store.values) != 0 {
		t.Fatalf("restore did not remove newly created values: %+v", store.values)
	}
}
