package selfupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCoordinatorRequiresConfiguredTrustRoot(t *testing.T) {
	coordinator := Coordinator{InstallRoot: t.TempDir(), CurrentVersion: "1.0.0"}
	if _, err := coordinator.Check(context.Background()); err == nil {
		t.Fatal("unconfigured update endpoint was accepted")
	}
}

func TestCoordinatorRejectsOlderStagedRelease(t *testing.T) {
	coordinator := Coordinator{InstallRoot: t.TempDir(), CurrentVersion: "2.0.0"}
	release := Release{Manifest: Manifest{Version: "1.0.0"}}
	if _, err := coordinator.DownloadAndStage(context.Background(), release); err == nil || !strings.Contains(err.Error(), "not newer") {
		t.Fatalf("older release error = %v", err)
	}
}

func TestCoordinatorSignedManifestDownloadAndStageFixture(t *testing.T) {
	packagePath := filepath.Join(t.TempDir(), "release.zip")
	writeReleaseZIP(t, packagePath, nil, false, false)
	packageData, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(packageData)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var manifestData []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/manifest.json":
			_, _ = response.Write(manifestData)
		case "/release.zip":
			_, _ = response.Write(packageData)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	manifest := Manifest{
		SchemaVersion: 1, Channel: "stable", Version: "1.1.0",
		PublishedUTC: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), MinimumVersion: "1.0.0",
		Artifacts: []Artifact{{OS: "windows", Arch: "amd64", URL: server.URL + "/release.zip", Size: int64(len(packageData)), SHA256: hex.EncodeToString(digest[:])}},
		KeyID:     "fixture-1",
	}
	payload, err := CanonicalPayload(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	manifestData, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	coordinator := Coordinator{
		InstallRoot: t.TempDir(), CurrentVersion: "1.0.0",
		ManifestURL: server.URL + "/manifest.json", HTTPClient: server.Client(),
		TrustedKeys: map[string]ed25519.PublicKey{"fixture-1": publicKey},
	}
	release, err := coordinator.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	staged, err := coordinator.DownloadAndStage(context.Background(), release)
	if err != nil {
		t.Fatal(err)
	}
	if staged.Manifest.Version != "1.1.0" {
		t.Fatalf("staged version=%q", staged.Manifest.Version)
	}
	if _, err := os.Stat(filepath.Join(staged.Directory, "GenshinTools.exe")); err != nil {
		t.Fatal(err)
	}
}
