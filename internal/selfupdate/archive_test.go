package selfupdate

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestStagePackageExtractsOnlyDeclaredVerifiedFiles(t *testing.T) {
	packagePath := filepath.Join(t.TempDir(), "release.zip")
	writeReleaseZIP(t, packagePath, nil, false, false)
	artifact := artifactForFile(t, packagePath)
	staged, err := StagePackage(context.Background(), packagePath, filepath.Join(t.TempDir(), "versions"), "1.1.0", artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyStaged(context.Background(), staged.Directory, "1.1.0", staged.ManifestSHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(staged.Directory, "GenshinTools.exe")); err != nil {
		t.Fatal(err)
	}
}

func TestStagePackageRejectsUnsafePathsLinksAndUndeclaredFiles(t *testing.T) {
	tests := []struct {
		name    string
		extra   map[string][]byte
		symlink bool
	}{
		{"parent escape", map[string][]byte{"../escape.exe": []byte("bad")}, false},
		{"backslash", map[string][]byte{`LICENSES\bad.txt`: []byte("bad")}, false},
		{"device", map[string][]byte{"CON": []byte("bad")}, false},
		{"undeclared", map[string][]byte{"extra.txt": []byte("bad")}, false},
		{"symlink", map[string][]byte{"link.txt": []byte("target")}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			packagePath := filepath.Join(t.TempDir(), "release.zip")
			writeReleaseZIP(t, packagePath, test.extra, test.symlink, false)
			if _, err := StagePackage(context.Background(), packagePath, filepath.Join(t.TempDir(), "versions"), "1.1.0", artifactForFile(t, packagePath)); err == nil {
				t.Fatal("unsafe update package accepted")
			}
		})
	}
}

func TestStagePackageRejectsHashMismatchWithoutPublishingVersion(t *testing.T) {
	packagePath := filepath.Join(t.TempDir(), "release.zip")
	writeReleaseZIP(t, packagePath, nil, false, true)
	root := filepath.Join(t.TempDir(), "versions")
	if _, err := StagePackage(context.Background(), packagePath, root, "1.1.0", artifactForFile(t, packagePath)); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("hash mismatch was not rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "1.1.0")); !os.IsNotExist(err) {
		t.Fatal("failed package published a staged version")
	}
}

func TestVerifyStagedRejectsManifestTamperingAndExtraDirectories(t *testing.T) {
	packagePath := filepath.Join(t.TempDir(), "release.zip")
	writeReleaseZIP(t, packagePath, nil, false, false)
	staged, err := StagePackage(context.Background(), packagePath, filepath.Join(t.TempDir(), "versions"), "1.1.0", artifactForFile(t, packagePath))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(staged.Directory, "unexpected"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyStaged(context.Background(), staged.Directory, "1.1.0", staged.ManifestSHA256); err == nil || !strings.Contains(err.Error(), "undeclared directory") {
		t.Fatalf("extra staged directory was not rejected: %v", err)
	}
	if err := os.Remove(filepath.Join(staged.Directory, "unexpected")); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(staged.Directory, "release.json")
	data, _ := os.ReadFile(manifestPath)
	if err := os.WriteFile(manifestPath, append(data, ' '), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyStaged(context.Background(), staged.Directory, "1.1.0", staged.ManifestSHA256); err == nil || !strings.Contains(err.Error(), "prepared update transaction") {
		t.Fatalf("staged manifest tampering was not rejected: %v", err)
	}
}

func artifactForFile(t *testing.T, filePath string) Artifact {
	t.Helper()
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return Artifact{OS: "windows", Arch: "amd64", URL: "https://updates.example.invalid/release.zip", Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}
}

func writeReleaseZIP(t *testing.T, destination string, extras map[string][]byte, symlink, badHash bool) {
	t.Helper()
	files := map[string][]byte{
		"build-info.json":           []byte(`{"version":"1.1.0"}`),
		"GenshinTools-injector.exe": []byte("injector"),
		"GenshinTools-updater.exe":  []byte("updater"),
		"GenshinTools.exe":          []byte("main"),
		"LICENSE_POLICY.md":         []byte("policy"),
		"THIRD_PARTY_NOTICES.md":    []byte("notices"),
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	manifest := PackageManifest{SchemaVersion: 1, Version: "1.1.0"}
	for _, name := range names {
		digest := sha256.Sum256(files[name])
		hash := hex.EncodeToString(digest[:])
		if badHash && name == "GenshinTools.exe" {
			hash = strings.Repeat("0", 64)
		}
		manifest.Files = append(manifest.Files, PackageFile{Path: name, Size: int64(len(files[name])), SHA256: hash})
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(output)
	entries := map[string][]byte{"release.json": manifestData}
	for name, data := range files {
		entries[name] = data
	}
	for name, data := range extras {
		entries[name] = data
	}
	for name, data := range entries {
		if symlink && name == "link.txt" {
			header := &zip.FileHeader{Name: name, Method: zip.Store}
			header.SetMode(os.ModeSymlink | 0o777)
			writer, createErr := archive.CreateHeader(header)
			if createErr != nil {
				t.Fatal(createErr)
			}
			_, _ = writer.Write(data)
			continue
		}
		writer, createErr := archive.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		_, _ = writer.Write(data)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}
