package plugins

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"genshintools/internal/game"
	"genshintools/internal/injection"
)

type packageFixture struct {
	root      string
	layout    Layout
	candidate game.Candidate
	dll       []byte
	module    injection.Manifest
}

func newPackageFixture(t *testing.T) packageFixture {
	t.Helper()
	root := t.TempDir()
	layout, err := NewLayout(filepath.Join(root, "data"), filepath.Join(root, "data", "injection", "modules"))
	if err != nil {
		t.Fatal(err)
	}
	gameRoot := filepath.Join(root, "game")
	if err := os.MkdirAll(gameRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	testExecutable, _ := os.Executable()
	gameExecutable := filepath.Join(gameRoot, "YuanShen.exe")
	copyFileForPluginTest(t, testExecutable, gameExecutable)
	if err := os.WriteFile(filepath.Join(gameRoot, "config.ini"), []byte("game_version=6.7.0\nchannel=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	systemDLL := filepath.Join(os.Getenv("SystemRoot"), "System32", "comdlg32.dll")
	dll, err := os.ReadFile(systemDLL)
	if err != nil {
		t.Fatal(err)
	}
	fixtureDLL := filepath.Join(root, "module.dll")
	if err := os.WriteFile(fixtureDLL, dll, 0o644); err != nil {
		t.Fatal(err)
	}
	metadata, err := injection.InspectModuleFile(fixtureDLL)
	if err != nil || !metadata.IsDLL || metadata.FileVersion == "" || len(metadata.Exports) == 0 {
		t.Fatalf("system DLL metadata=%+v err=%v", metadata, err)
	}
	module := injection.Manifest{SchemaVersion: injection.ManifestSchemaVersion, ID: "fixture", Name: "Fixture", SourceURL: "https://example.invalid/source", License: "Test", AdapterAPI: injection.AdapterAPIVersion, DLL: "module.dll", SHA256: metadata.SHA256, Architecture: "amd64", FileVersion: metadata.FileVersion, GameVersions: []string{"6.7.0"}, GameExecutables: []string{"YuanShen.exe"}, RequiredExports: []string{metadata.Exports[0]}}
	return packageFixture{root: root, layout: layout, candidate: game.Candidate{Root: gameRoot, Executable: gameExecutable, ExeName: "YuanShen.exe", Version: "6.7.0", Server: game.ServerCNOfficial}, dll: dll, module: module}
}

func (fixture packageFixture) packageFile(t *testing.T, version, unsafeEntry string) (string, CatalogItem) {
	return fixture.packageFileRevision(t, version, "", unsafeEntry)
}

func (fixture packageFixture) packageFileRevision(t *testing.T, version, revision, unsafeEntry string) (string, CatalogItem) {
	t.Helper()
	moduleData, _ := json.MarshalIndent(fixture.module, "", "  ")
	description := "Owned fixture plugin"
	if revision != "" {
		description += " " + revision
	}
	manifest := Manifest{SchemaVersion: 1, ID: "fixture", Name: "Fixture Plugin", Developer: "Tests", Description: description, Version: version, Category: "visuals", Tags: []string{"fixture"}, Capabilities: []string{"visual"}, SourceURL: "https://example.invalid/source", License: "Test-Only", ModuleFile: "module.json", Files: []PackageFile{
		{Path: "module.json", Size: int64(len(moduleData)), SHA256: bytesSHA256(moduleData)},
		{Path: "module.dll", Size: int64(len(fixture.dll)), SHA256: bytesSHA256(fixture.dll)},
	}}
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	packagePath := filepath.Join(fixture.root, "fixture-"+version+revision+".zip")
	file, err := os.Create(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, data := range map[string][]byte{"plugin.json": manifestData, "module.json": moduleData, "module.dll": fixture.dll} {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if unsafeEntry != "" {
		entry, _ := archive.Create(unsafeEntry)
		_, _ = entry.Write([]byte("escape"))
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(packagePath)
	hash, _ := fileSHA256(packagePath)
	item := CatalogItem{ID: manifest.ID, Name: manifest.Name, Developer: manifest.Developer, Description: manifest.Description, Version: manifest.Version, Category: manifest.Category, Tags: manifest.Tags, Capabilities: manifest.Capabilities, SourceURL: manifest.SourceURL, License: manifest.License, PackageURL: "https://example.invalid/fixture.zip", PackageSize: info.Size(), PackageSHA256: hash, UpdatedUTC: time.Now().UTC().Format(time.RFC3339)}
	return packagePath, item
}

func TestInstallSameVersionRevisionKeepsAndRestoresRollback(t *testing.T) {
	fixture := newPackageFixture(t)
	state := DefaultState()
	firstPath, firstItem := fixture.packageFileRevision(t, "1.0.0", "-a", "")
	if _, err := InstallLocalPackage(t.Context(), firstPath, firstItem, fixture.layout, fixture.candidate, &state); err != nil {
		t.Fatal(err)
	}
	firstRevision := state.Installed["fixture"].ActiveRevision
	secondPath, secondItem := fixture.packageFileRevision(t, "1.0.0", "-b", "")
	if _, err := InstallLocalPackage(t.Context(), secondPath, secondItem, fixture.layout, fixture.candidate, &state); err != nil {
		t.Fatal(err)
	}
	installed := state.Installed["fixture"]
	if installed.ActiveRevision == firstRevision || !containsExact(installed.RollbackVersions, firstRevision) {
		t.Fatalf("same-version revision was not retained: %+v", installed)
	}
	if _, err := Rollback(t.Context(), fixture.layout, &state, "fixture", firstRevision, fixture.candidate); err != nil {
		t.Fatal(err)
	}
	if state.Installed["fixture"].ActiveRevision != firstRevision {
		t.Fatalf("same-version rollback did not restore revision: %+v", state.Installed["fixture"])
	}
}

func TestRepairIdenticalRevisionDoesNotCreateRollbackCopy(t *testing.T) {
	fixture := newPackageFixture(t)
	state := DefaultState()
	packagePath, item := fixture.packageFileRevision(t, "1.0.0", "-same", "")
	if _, err := InstallLocalPackage(t.Context(), packagePath, item, fixture.layout, fixture.candidate, &state); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallLocalPackage(t.Context(), packagePath, item, fixture.layout, fixture.candidate, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Installed["fixture"].RollbackVersions) != 0 {
		t.Fatalf("identical repair created rollback state: %+v", state.Installed["fixture"])
	}
	versions := filepath.Join(fixture.layout.Versions, "fixture")
	entries, err := os.ReadDir(versions)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("identical repair left rollback directories: %v", entries)
	}
}

func TestInstallPackageUpdateKeepsRollback(t *testing.T) {
	fixture := newPackageFixture(t)
	state := DefaultState()
	firstPath, firstItem := fixture.packageFile(t, "1.0.0", "")
	if _, err := InstallLocalPackage(t.Context(), firstPath, firstItem, fixture.layout, fixture.candidate, &state); err != nil {
		t.Fatal(err)
	}
	secondPath, secondItem := fixture.packageFile(t, "1.1.0", "")
	result, err := InstallLocalPackage(t.Context(), secondPath, secondItem, fixture.layout, fixture.candidate, &state)
	if err != nil {
		t.Fatal(err)
	}
	if result.PreviousVersion != "1.0.0" || !result.RollbackReady || state.Installed["fixture"].ActiveVersion != "1.1.0" {
		t.Fatalf("result=%+v state=%+v", result, state)
	}
	if _, err := os.Stat(filepath.Join(fixture.layout.Versions, "fixture", "1.0.0", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	items, warnings, err := Discover(fixture.layout.Modules, state)
	if err != nil || len(warnings) != 0 || len(items) != 1 || items[0].Manifest.Version != "1.1.0" {
		t.Fatalf("items=%+v warnings=%v err=%v", items, warnings, err)
	}
	rollback, err := Rollback(t.Context(), fixture.layout, &state, "fixture", "1.0.0", fixture.candidate)
	if err != nil {
		t.Fatal(err)
	}
	if rollback.Manifest.Version != "1.0.0" || state.Installed["fixture"].ActiveVersion != "1.0.0" || !containsExact(state.Installed["fixture"].RollbackVersions, "1.1.0") {
		t.Fatalf("rollback=%+v state=%+v", rollback, state.Installed["fixture"])
	}
}

func TestInspectLocalPackageDerivesVerifiedIdentity(t *testing.T) {
	fixture := newPackageFixture(t)
	packagePath, want := fixture.packageFile(t, "1.2.3", "")
	got, err := InspectLocalPackage(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Version != want.Version || got.PackageSize != want.PackageSize || !strings.EqualFold(got.PackageSHA256, want.PackageSHA256) {
		t.Fatalf("local identity=%+v want=%+v", got, want)
	}
}

func TestUninstallRemovesActiveVersionsAndState(t *testing.T) {
	fixture := newPackageFixture(t)
	state := DefaultState()
	firstPath, firstItem := fixture.packageFile(t, "1.0.0", "")
	if _, err := InstallLocalPackage(t.Context(), firstPath, firstItem, fixture.layout, fixture.candidate, &state); err != nil {
		t.Fatal(err)
	}
	secondPath, secondItem := fixture.packageFile(t, "1.1.0", "")
	if _, err := InstallLocalPackage(t.Context(), secondPath, secondItem, fixture.layout, fixture.candidate, &state); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(&state, "fixture", true); err != nil {
		t.Fatal(err)
	}
	if err := SetAlias(&state, "fixture", "Test Alias"); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(fixture.layout.State, state); err != nil {
		t.Fatal(err)
	}
	manifest, err := Uninstall(fixture.layout, &state, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "1.1.0" || containsExact(state.Enabled, "fixture") || containsExact(state.Order, "fixture") || state.Aliases["fixture"] != "" {
		t.Fatalf("manifest=%+v state=%+v", manifest, state)
	}
	for _, path := range []string{filepath.Join(fixture.layout.Modules, "fixture"), filepath.Join(fixture.layout.Versions, "fixture"), fixture.layout.Transaction} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("uninstall left %s: %v", path, err)
		}
	}
}

func TestRecoverUninstallRestoresBeforeStateCommit(t *testing.T) {
	fixture := newPackageFixture(t)
	if err := fixture.layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	stageName := "fixture-uninstall-crash"
	stageRoot := filepath.Join(fixture.layout.Staging, stageName)
	backup := filepath.Join(stageRoot, "removed")
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "marker.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	journal := installJournal{SchemaVersion: 1, Operation: "uninstall", Phase: "old_moved", PluginID: "fixture", NewVersion: "1.0.0", OldVersion: "1.0.0", StageName: stageName, Backup: filepath.Join("staging", stageName, "removed")}
	if err := saveJournal(fixture.layout.Transaction, journal); err != nil {
		t.Fatal(err)
	}
	state := DefaultState()
	state.Installed["fixture"] = InstalledState{ActiveVersion: "1.0.0"}
	if err := RecoverTransaction(fixture.layout, &state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(fixture.layout.Modules, "fixture", "marker.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fixture.layout.Transaction); !os.IsNotExist(err) {
		t.Fatalf("transaction journal remains: %v", err)
	}
}

func TestRecoverRejectsUninstallBackupOutsideItsStage(t *testing.T) {
	fixture := newPackageFixture(t)
	if err := fixture.layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	stageName := "fixture-uninstall-tampered"
	if err := os.MkdirAll(filepath.Join(fixture.layout.Staging, stageName), 0o755); err != nil {
		t.Fatal(err)
	}
	journal := installJournal{SchemaVersion: 1, Operation: "uninstall", Phase: "old_moved", PluginID: "fixture", NewVersion: "1.0.0", OldVersion: "1.0.0", StageName: stageName, Backup: filepath.Join("versions", "fixture", "1.0.0")}
	if err := saveJournal(fixture.layout.Transaction, journal); err != nil {
		t.Fatal(err)
	}
	state := DefaultState()
	state.Installed["fixture"] = InstalledState{ActiveVersion: "1.0.0"}
	if err := RecoverTransaction(fixture.layout, &state); err == nil {
		t.Fatal("tampered uninstall backup path was accepted")
	}
}

func TestRecoverRejectsOperationSpecificPhase(t *testing.T) {
	fixture := newPackageFixture(t)
	if err := fixture.layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	stageName := "fixture-uninstall-phase"
	stageRoot := filepath.Join(fixture.layout.Staging, stageName)
	if err := os.MkdirAll(stageRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	journal := installJournal{SchemaVersion: 1, Operation: "uninstall", Phase: "new_moved", PluginID: "fixture", NewVersion: "1.0.0", OldVersion: "1.0.0", StageName: stageName, Backup: filepath.Join("staging", stageName, "removed")}
	if err := saveJournal(fixture.layout.Transaction, journal); err != nil {
		t.Fatal(err)
	}
	state := DefaultState()
	state.Installed["fixture"] = InstalledState{ActiveVersion: "1.0.0"}
	if err := RecoverTransaction(fixture.layout, &state); err == nil {
		t.Fatal("uninstall transaction accepted an install-only phase")
	}
}

func TestInstallRejectsZipSlipWithoutActiveMutation(t *testing.T) {
	fixture := newPackageFixture(t)
	state := DefaultState()
	packagePath, item := fixture.packageFile(t, "1.0.0", "../escape.txt")
	if _, err := InstallLocalPackage(t.Context(), packagePath, item, fixture.layout, fixture.candidate, &state); err == nil {
		t.Fatal("zip-slip package installed")
	}
	if _, err := os.Stat(filepath.Join(fixture.layout.Modules, "fixture")); !os.IsNotExist(err) {
		t.Fatalf("unsafe package created active module: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.root, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("zip-slip escaped staging: %v", err)
	}
}

func TestRecoverTransactionRestoresOldActive(t *testing.T) {
	fixture := newPackageFixture(t)
	if err := fixture.layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(fixture.layout.Modules, "fixture")
	backup := filepath.Join(fixture.layout.Versions, "fixture", "1.0.0")
	stage := filepath.Join(fixture.layout.Staging, "fixture-crash")
	if err := os.MkdirAll(filepath.Dir(backup), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(active, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "old.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	journal := installJournal{SchemaVersion: 1, Phase: "new_moved", PluginID: "fixture", NewVersion: "1.1.0", OldVersion: "1.0.0", StageName: "fixture-crash", Backup: filepath.Join("versions", "fixture", "1.0.0")}
	if err := saveJournal(fixture.layout.Transaction, journal); err != nil {
		t.Fatal(err)
	}
	state := DefaultState()
	state.Installed["fixture"] = InstalledState{ActiveVersion: "1.0.0"}
	if err := RecoverTransaction(fixture.layout, &state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(active, "old.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(active, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("uncommitted active survived recovery: %v", err)
	}
}

func TestRecoverTransactionDistinguishesSameVersionRevisions(t *testing.T) {
	fixture := newPackageFixture(t)
	if err := fixture.layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	const oldRevision = "1.0.0-rev.1111111111111111"
	const newRevision = "1.0.0-rev.2222222222222222"
	active := filepath.Join(fixture.layout.Modules, "fixture")
	backup := filepath.Join(fixture.layout.Versions, "fixture", oldRevision)
	stage := filepath.Join(fixture.layout.Staging, "fixture-same-crash")
	for _, directory := range []string{active, backup, stage} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(active, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "old.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	journal := installJournal{SchemaVersion: 1, Phase: "new_moved", PluginID: "fixture", NewVersion: newRevision, OldVersion: oldRevision, StageName: "fixture-same-crash", Backup: filepath.Join("versions", "fixture", oldRevision)}
	if err := saveJournal(fixture.layout.Transaction, journal); err != nil {
		t.Fatal(err)
	}
	state := DefaultState()
	state.Installed["fixture"] = InstalledState{ActiveVersion: "1.0.0", ActiveRevision: oldRevision}
	if err := RecoverTransaction(fixture.layout, &state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(active, "old.txt")); err != nil {
		t.Fatalf("old same-version revision was not restored: %v", err)
	}
}

func bytesSHA256(data []byte) string {
	value := sha256.Sum256(data)
	return strings.ToUpper(hex.EncodeToString(value[:]))
}

func copyFileForPluginTest(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o755); err != nil {
		t.Fatal(err)
	}
}
