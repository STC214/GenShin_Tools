package injection

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"genshintools/internal/game"
)

type moduleFixture struct {
	root      string
	candidate game.Candidate
	manifest  Manifest
}

func newModuleFixture(t *testing.T) moduleFixture {
	t.Helper()
	root := t.TempDir()
	gameRoot := filepath.Join(root, "game")
	moduleDir := filepath.Join(root, "modules", "fixture")
	if err := os.MkdirAll(gameRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	gameExecutable := filepath.Join(gameRoot, "YuanShen.exe")
	copyTestFile(t, testExecutable, gameExecutable)
	if err := os.WriteFile(filepath.Join(gameRoot, "config.ini"), []byte("game_version=6.7.0\nchannel=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	systemRoot := os.Getenv("SystemRoot")
	modulePath := filepath.Join(moduleDir, "module.dll")
	copyTestFile(t, filepath.Join(systemRoot, "System32", "comdlg32.dll"), modulePath)
	hash, err := fileSHA256(modulePath)
	if err != nil {
		t.Fatal(err)
	}
	version, err := fileVersion(modulePath)
	if err != nil || version == "" {
		t.Fatalf("system comdlg32.dll version = %q, err=%v", version, err)
	}
	metadata, err := inspectPE(modulePath)
	if err != nil || len(metadata.Exports) == 0 {
		t.Fatalf("system comdlg32.dll exports: %v, %v", metadata.Exports, err)
	}
	manifest := Manifest{
		SchemaVersion:   ManifestSchemaVersion,
		ID:              "fixture",
		Name:            "Fixture Module",
		SourceURL:       "https://example.invalid/source",
		License:         "Test-Only",
		AdapterAPI:      AdapterAPIVersion,
		DLL:             "module.dll",
		SHA256:          hash,
		Architecture:    "amd64",
		FileVersion:     version,
		GameVersions:    []string{"6.7.0"},
		GameExecutables: []string{"YuanShen.exe"},
		RequiredExports: []string{metadata.Exports[0]},
	}
	fixture := moduleFixture{root: filepath.Join(root, "modules"), candidate: game.Candidate{Root: gameRoot, Executable: gameExecutable, ExeName: "YuanShen.exe", Version: "6.7.0", Server: game.ServerCNOfficial}, manifest: manifest}
	fixture.write(t)
	return fixture
}

func (fixture moduleFixture) write(t *testing.T) {
	t.Helper()
	data, err := json.MarshalIndent(fixture.manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "fixture", "module.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyTestFile(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestAuditModuleAcceptsExactFixture(t *testing.T) {
	fixture := newModuleFixture(t)
	audit, err := AuditModule(fixture.root, "fixture", fixture.candidate)
	if err != nil {
		t.Fatal(err)
	}
	if audit.SHA256 != fixture.manifest.SHA256 || audit.FileVersion != fixture.manifest.FileVersion || len(audit.Exports) == 0 {
		t.Fatalf("unexpected audit: %+v", audit)
	}
}

func TestAuditModuleFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		change func(*moduleFixture)
	}{
		{name: "hash mismatch", change: func(f *moduleFixture) { f.manifest.SHA256 = "00" + f.manifest.SHA256[2:] }},
		{name: "unknown game version", change: func(f *moduleFixture) { f.candidate.Version = "9.9.9" }},
		{name: "missing export", change: func(f *moduleFixture) { f.manifest.RequiredExports = []string{"DefinitelyMissingExport"} }},
		{name: "wrong file version", change: func(f *moduleFixture) { f.manifest.FileVersion = "0.0.0.0" }},
		{name: "unversioned lie", change: func(f *moduleFixture) { f.manifest.FileVersion, f.manifest.AllowUnversioned = "", true }},
		{name: "candidate path mismatch", change: func(f *moduleFixture) {
			f.candidate.ExeName = "GenshinImpact.exe"
			f.manifest.GameExecutables = []string{"GenshinImpact.exe"}
		}},
		{name: "adjacent dependency shadow", change: func(f *moduleFixture) {
			_ = os.WriteFile(filepath.Join(f.root, "fixture", "KERNEL32.dll"), []byte("shadow"), 0o644)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newModuleFixture(t)
			test.change(&fixture)
			fixture.write(t)
			if _, err := AuditModule(fixture.root, "fixture", fixture.candidate); err == nil {
				t.Fatal("unsafe module combination unexpectedly passed")
			}
		})
	}
}
