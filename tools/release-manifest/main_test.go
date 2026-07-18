package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"genshintools/internal/selfupdate"
)

func TestRunGeneratesAndSelfVerifiesSignedManifest(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	packagePath := filepath.Join(root, "release.zip")
	if err := os.WriteFile(packagePath, []byte("fixture package"), 0o644); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(root, "private.key")
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(privateKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(root, "manifest.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--package", packagePath, "--output", outputPath, "--version", "1.1.0",
		"--minimum-version", "1.0.0", "--url", "https://updates.example.invalid/release.zip",
		"--key-id", "fixture-1", "--private-key", keyPath,
		"--published-utc", time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := selfupdate.DecodeAndVerify(data, map[string]ed25519.PublicKey{"fixture-1": publicKey}, "1.0.0", "windows", "amd64", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{
		"--package", packagePath, "--output", outputPath, "--version", "1.1.0",
		"--minimum-version", "1.0.0", "--url", "https://updates.example.invalid/release.zip",
		"--key-id", "fixture-1", "--private-key", keyPath,
	}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "overwrite") {
		t.Fatalf("overwrite result code=%d stderr=%q", code, stderr.String())
	}
}

func TestReadPrivateKeyRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.key")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, (1<<10)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateKey(path); err == nil {
		t.Fatal("oversized private key was accepted")
	}
}

func TestRunRejectsMissingRequiredOption(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"--version", "1.1.0"}, &bytes.Buffer{}, &stderr); code != 2 || !strings.Contains(stderr.String(), "--package is required") {
		t.Fatalf("missing option result code=%d stderr=%q", code, stderr.String())
	}
}
