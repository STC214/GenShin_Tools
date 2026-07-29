package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
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
		"GenshinTools-updater.exe":                 "updater",
		"SOURCES/AutoHotkey-v1.0.48.05-source.zip": "corresponding source",
		"build-info.json":                          `{"version":"1.2.3","target":"windows/amd64","commit":"0123456789ab"}`,
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
	valid := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"version":"1.2.3","target":"windows/amd64","commit":"0123456789ab"}`)...)
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
	if err := os.WriteFile(path, []byte(`{"version":"1.2.3","target":"windows/amd64","commit":"0123456789ab-dirty"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyBuildInfo(path, "1.2.3"); err == nil {
		t.Fatal("dirty build provenance was accepted")
	}
}

func TestPublishPackageChecksumFailurePreservesExistingPackage(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "portable.zip")
	candidate := filepath.Join(root, "candidate.zip")
	content := []byte("verified package")
	if err := os.WriteFile(output, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output+".sha256", 0o755); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if err := publishPackage(candidate, output, hex.EncodeToString(digest[:]), []byte("hash\n")); err == nil {
		t.Fatal("checksum replacement over a directory unexpectedly succeeded")
	}
	if data, err := os.ReadFile(output); err != nil || string(data) != string(content) {
		t.Fatalf("existing package changed after checksum failure: %q err=%v", data, err)
	}
	if info, err := os.Stat(output + ".sha256"); err != nil || !info.IsDir() {
		t.Fatalf("non-file checksum target was modified: info=%v err=%v", info, err)
	}
}

func TestPublishPackageRefusesDifferentSameVersionContent(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "portable.zip")
	candidate := filepath.Join(root, "candidate.zip")
	if err := os.WriteFile(output, []byte("published"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("different"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("different"))
	if err := publishPackage(candidate, output, hex.EncodeToString(digest[:]), []byte("hash\n")); err == nil {
		t.Fatal("different same-version package was accepted")
	}
	if data, err := os.ReadFile(output); err != nil || string(data) != "published" {
		t.Fatalf("published package changed: %q err=%v", data, err)
	}
}
