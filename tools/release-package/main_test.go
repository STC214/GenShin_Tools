package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageReleaseCreatesVerifiedDeterministicPortableZIP(t *testing.T) {
	root := t.TempDir()
	dist := filepath.Join(root, "dist")
	for _, directory := range []string{"LICENSES", "SOURCES"} {
		if err := os.MkdirAll(filepath.Join(dist, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"AHK_F.exe":                                "project-owner supplied legacy utility",
		"GenshinTools.exe":                         "main",
		"GenshinTools-injector.exe":                "injector",
		"GenshinTools-input.exe":                   "input",
		"GenshinTools-updater.exe":                 "updater",
		"SOURCES/AutoHotkey-v1.0.48.05-source.zip": "corresponding source",
		"build-info.json":                          `{"version":"1.2.3","target":"windows/amd64"}`,
		"LICENSE":                                  "MIT",
		"LICENSES/AutoHotkey-v1.0-GPL-2.0.txt":     "GPL-2.0",
		"LICENSES/User-AHK_F-NOTICE.md":            "project owner distribution notice",
		"LICENSE_POLICY.md":                        "portable release policy",
		"THIRD_PARTY_NOTICES.md":                   "notices",
		"LICENSES/dependency.txt":                  "license",
	}
	for name, data := range files {
		path := filepath.Join(dist, filepath.FromSlash(name))
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(root, "portable.zip")
	options := options{dist: dist, output: output, version: "1.2.3"}
	if err := packageRelease(options); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := packageRelease(options); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("portable ZIP is not deterministic")
	}
	archive, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.File) != len(files)+1 || archive.File[0].Name != "release.json" {
		t.Fatalf("unexpected archive entries: %d", len(archive.File))
	}
	checksum, err := os.ReadFile(output + ".sha256")
	if err != nil || !strings.HasSuffix(string(checksum), "  portable.zip\n") {
		t.Fatalf("invalid checksum sidecar: %q err=%v", checksum, err)
	}
}

func TestPackageReleaseRejectsMissingLicenseDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	err := packageRelease(options{dist: root, output: filepath.Join(root, "portable.zip"), version: "1.2.3"})
	if err == nil {
		t.Fatal("missing license directory was accepted")
	}
}

func TestVerifyBuildInfoAcceptsUTF8BOMAndRejectsTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "build-info.json")
	valid := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"version":"1.2.3","target":"windows/amd64","commit":"fixture"}`)...)
	if err := os.WriteFile(path, valid, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyBuildInfo(path, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(valid, []byte(` {}`)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyBuildInfo(path, "1.2.3"); err == nil {
		t.Fatal("trailing build-info JSON was accepted")
	}
}
