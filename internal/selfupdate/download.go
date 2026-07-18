package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func DownloadArtifact(ctx context.Context, client *http.Client, artifact Artifact, destination string) error {
	if err := validateDownloadArtifact(artifact); err != nil {
		return err
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/zip, application/octet-stream")
	request.Header.Set("User-Agent", "GenshinTools-SelfUpdate/1")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("update artifact HTTP status %d", response.StatusCode)
	}
	if response.Request == nil || response.Request.URL == nil || !sameOrigin(artifact.URL, response.Request.URL.String()) {
		return errors.New("update artifact redirect changed the signed HTTPS origin")
	}
	if response.ContentLength >= 0 && response.ContentLength != artifact.Size {
		return fmt.Errorf("update artifact Content-Length is %d, want %d", response.ContentLength, artifact.Size)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".update-download-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(response.Body, artifact.Size+1))
	if err != nil {
		return err
	}
	if written != artifact.Size {
		return fmt.Errorf("update artifact length is %d, want %d", written, artifact.Size)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), artifact.SHA256) {
		return errors.New("update artifact SHA-256 mismatch")
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceFile(temporaryPath, destination); err != nil {
		return err
	}
	committed = true
	return nil
}

func validateDownloadArtifact(artifact Artifact) error {
	manifest := Manifest{SchemaVersion: 1, Channel: "stable", Version: "1.0.1", PublishedUTC: time.Unix(0, 0).UTC().Format(time.RFC3339), MinimumVersion: "1.0.0", Artifacts: []Artifact{artifact}, KeyID: "download"}
	return validateManifest(manifest, time.Time{})
}

func sameOrigin(left, right string) bool {
	a, aErr := url.Parse(left)
	b, bErr := url.Parse(right)
	return aErr == nil && bErr == nil && strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}
