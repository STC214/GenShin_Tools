package main

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"genshintools/internal/selfupdate"
)

func TestRunAuditsSignedManifestAndPackage(t *testing.T) {
	root := t.TempDir()
	packagePath := filepath.Join(root, "release.zip")
	writeAuditZIP(t, packagePath)
	packageData, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(packageData)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := selfupdate.Manifest{
		SchemaVersion: 1, Channel: "stable", Version: "1.1.0",
		PublishedUTC: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), MinimumVersion: "1.0.0",
		Artifacts: []selfupdate.Artifact{{OS: "windows", Arch: "amd64", URL: "https://updates.example.invalid/release.zip", Size: int64(len(packageData)), SHA256: hex.EncodeToString(digest[:])}},
		KeyID:     "fixture-1",
	}
	payload, err := selfupdate.CanonicalPayload(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	keyPath := filepath.Join(root, "public.key")
	if err := os.WriteFile(manifestPath, manifestData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(publicKey)), 0o644); err != nil {
		t.Fatal(err)
	}
	var auditOut, auditErr bytes.Buffer
	if code := run([]string{"--package", packagePath, "--manifest", manifestPath, "--public-key", keyPath}, &auditOut, &auditErr); code != 0 {
		t.Fatalf("audit code=%d stdout=%q stderr=%q", code, auditOut.String(), auditErr.String())
	}
	manifestData = []byte(strings.Replace(string(manifestData), "1.1.0", "1.2.0", 1))
	if err := os.WriteFile(manifestPath, manifestData, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"--package", packagePath, "--manifest", manifestPath, "--public-key", keyPath}, &bytes.Buffer{}, &bytes.Buffer{}); code == 0 {
		t.Fatal("tampered release manifest passed audit")
	}
}

func TestReadPublicKeyRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "public.key")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, (1<<10)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPublicKey(path); err == nil {
		t.Fatal("oversized public key was accepted")
	}
}

func TestVerifyBuildInfoRejectsTrailingJSON(t *testing.T) {
	if err := verifyBuildInfo([]byte(`{"version":"1.1.0"} {}`), "1.1.0"); err == nil {
		t.Fatal("build-info trailing JSON was accepted")
	}
}

func writeAuditZIP(t *testing.T, path string) {
	t.Helper()
	files := map[string][]byte{
		"build-info.json":           []byte(`{"version":"1.1.0","target":"windows/amd64"}`),
		"GenshinTools-injector.exe": []byte("injector"),
		"GenshinTools-updater.exe":  []byte("updater"),
		"GenshinTools.exe":          []byte("main"),
		"LICENSE_POLICY.md":         []byte("policy"),
		"THIRD_PARTY_NOTICES.md":    []byte("notices"),
	}
	manifest := selfupdate.PackageManifest{SchemaVersion: 1, Version: "1.1.0"}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	for _, name := range names {
		digest := sha256.Sum256(files[name])
		manifest.Files = append(manifest.Files, selfupdate.PackageFile{Path: name, Size: int64(len(files[name])), SHA256: hex.EncodeToString(digest[:])})
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(output)
	entries := map[string][]byte{"release.json": manifestData}
	for name, data := range files {
		entries[name] = data
	}
	for name, data := range entries {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
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
