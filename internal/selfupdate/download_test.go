package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadArtifactVerifiesAndAtomicallyReplaces(t *testing.T) {
	payload := []byte("verified update payload")
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(payload)
	}))
	defer server.Close()
	digest := sha256.Sum256(payload)
	artifact := Artifact{OS: "windows", Arch: "amd64", URL: server.URL + "/release.zip", Size: int64(len(payload)), SHA256: hex.EncodeToString(digest[:])}
	destination := filepath.Join(t.TempDir(), "staging", "release.zip")
	if err := DownloadArtifact(context.Background(), server.Client(), artifact, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("downloaded=%q err=%v", got, err)
	}
}

func TestDownloadArtifactFailurePreservesExistingDestination(t *testing.T) {
	payload := []byte("untrusted payload")
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(payload)
	}))
	defer server.Close()
	artifact := Artifact{OS: "windows", Arch: "amd64", URL: server.URL + "/release.zip", Size: int64(len(payload)), SHA256: strings.Repeat("0", 64)}
	destination := filepath.Join(t.TempDir(), "release.zip")
	if err := os.WriteFile(destination, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DownloadArtifact(context.Background(), server.Client(), artifact, destination); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("bad artifact was not rejected: %v", err)
	}
	got, _ := os.ReadFile(destination)
	if string(got) != "old" {
		t.Fatalf("failed download replaced destination: %q", got)
	}
}
