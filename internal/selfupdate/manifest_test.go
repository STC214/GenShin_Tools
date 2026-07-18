package selfupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecodeAndVerifySignedManifest(t *testing.T) {
	manifest, publicKey, data := signedFixture(t)
	release, err := DecodeAndVerify(data, map[string]ed25519.PublicKey{manifest.KeyID: publicKey}, "1.2.3", "windows", "amd64", time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if release.Manifest.Version != "1.3.0" || release.Artifact.Arch != "amd64" {
		t.Fatalf("unexpected release: %+v", release)
	}
}

func TestDecodeAndVerifyRejectsTamperingUnknownFieldsAndDowngrade(t *testing.T) {
	manifest, publicKey, data := signedFixture(t)
	keys := map[string]ed25519.PublicKey{manifest.KeyID: publicKey}
	tampered := []byte(strings.Replace(string(data), `"size":3`, `"size":4`, 1))
	if _, err := DecodeAndVerify(tampered, keys, "1.2.3", "windows", "amd64", time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("tampered manifest was not rejected by signature: %v", err)
	}
	unknown := []byte(strings.Replace(string(data), `"channel":"stable"`, `"channel":"stable","surprise":true`, 1))
	if _, err := DecodeAndVerify(unknown, keys, "1.2.3", "windows", "amd64", time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("unknown manifest field accepted")
	}
	if _, err := DecodeAndVerify(data, keys, "1.3.0", "windows", "amd64", time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("same-version update accepted: %v", err)
	}
	if _, err := DecodeAndVerify(data, keys, "development", "windows", "amd64", time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "current application version") {
		t.Fatalf("invalid current version accepted: %v", err)
	}
}

func TestSemanticVersionOrdering(t *testing.T) {
	tests := []struct {
		left, right string
		want        int
	}{
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-rc.2", "1.0.0-rc.10", -1},
		{"1.2.0", "1.10.0", -1},
		{"2.0.0+build.1", "2.0.0+build.2", 0},
	}
	for _, test := range tests {
		if got := CompareVersions(test.left, test.right); got != test.want {
			t.Fatalf("CompareVersions(%q, %q)=%d want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestParseTrustedKeys(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{"release-1": base64.StdEncoding.EncodeToString(publicKey)})
	keys, err := ParseTrustedKeys(data)
	if err != nil || len(keys["release-1"]) != ed25519.PublicKeySize {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
	if _, err := ParseTrustedKeys([]byte(`{"release-1":"bad"}`)); err == nil {
		t.Fatal("invalid trusted key accepted")
	}
}

func signedFixture(t *testing.T) (Manifest, ed25519.PublicKey, []byte) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("zip"))
	manifest := Manifest{
		SchemaVersion:  1,
		Channel:        "stable",
		Version:        "1.3.0",
		PublishedUTC:   "2026-07-18T00:00:00Z",
		MinimumVersion: "1.0.0",
		Artifacts:      []Artifact{{OS: "windows", Arch: "amd64", URL: "https://updates.example.invalid/GenshinTools.zip", Size: 3, SHA256: hex.EncodeToString(digest[:])}},
		KeyID:          "release-1",
	}
	payload, err := CanonicalPayload(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return manifest, publicKey, data
}
