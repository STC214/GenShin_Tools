// Package resources implements verified game-resource acquisition and repair.
package resources

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
)

const (
	ManifestSchemaVersion = 1
	MaxManifestBytes      = 32 << 20
)

type Manifest struct {
	SchemaVersion int            `json:"schema_version"`
	Version       string         `json:"version"`
	Kind          string         `json:"kind"`
	Files         []ManifestFile `json:"files"`
}

type ManifestFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Hash Hash   `json:"hash"`
	URL  string `json:"url"`
}

type Hash struct {
	Algorithm string `json:"algorithm"`
	Digest    string `json:"digest"`
}

func ParseManifest(reader io.Reader) (Manifest, error) {
	limited := io.LimitReader(reader, MaxManifestBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	if len(data) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("manifest exceeds %d bytes", MaxManifestBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return Manifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("manifest contains trailing JSON value")
		}
		return fmt.Errorf("decode manifest trailer: %w", err)
	}
	return nil
}

func (m *Manifest) Validate() error {
	if m.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("unsupported manifest schema %d", m.SchemaVersion)
	}
	if strings.TrimSpace(m.Version) == "" || strings.TrimSpace(m.Kind) == "" {
		return errors.New("manifest version and kind are required")
	}
	if len(m.Files) == 0 {
		return errors.New("manifest contains no files")
	}
	seen := make(map[string]struct{}, len(m.Files))
	for i := range m.Files {
		file := &m.Files[i]
		normalized, err := NormalizeRelativePath(file.Path)
		if err != nil {
			return fmt.Errorf("file %d path: %w", i, err)
		}
		key := strings.ToLower(normalized)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate file path %q", file.Path)
		}
		seen[key] = struct{}{}
		file.Path = normalized
		if file.Size < 0 {
			return fmt.Errorf("file %q has negative size", file.Path)
		}
		if err := file.Hash.Validate(); err != nil {
			return fmt.Errorf("file %q hash: %w", file.Path, err)
		}
		parsed, err := url.Parse(file.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return fmt.Errorf("file %q has invalid HTTP(S) URL", file.Path)
		}
	}
	sort.Slice(m.Files, func(i, j int) bool { return strings.ToLower(m.Files[i].Path) < strings.ToLower(m.Files[j].Path) })
	return nil
}

func (h Hash) Validate() error {
	algorithm := strings.ToLower(strings.TrimSpace(h.Algorithm))
	want := 0
	switch algorithm {
	case "md5":
		want = md5.Size * 2
	case "sha256":
		want = sha256.Size * 2
	default:
		return fmt.Errorf("unsupported algorithm %q", h.Algorithm)
	}
	if len(h.Digest) != want {
		return fmt.Errorf("%s digest must contain %d hex characters", algorithm, want)
	}
	if _, err := hex.DecodeString(h.Digest); err != nil {
		return fmt.Errorf("invalid digest: %w", err)
	}
	return nil
}

func NormalizeRelativePath(value string) (string, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "/", `\`)
	if value == "" || filepath.IsAbs(value) || filepath.VolumeName(value) != "" || strings.Contains(value, ":") {
		return "", fmt.Errorf("path %q is not a safe relative path", value)
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, `..\`) {
		return "", fmt.Errorf("path %q escapes its root", value)
	}
	for _, part := range strings.Split(clean, `\`) {
		trimmed := strings.TrimRight(part, ". ")
		if trimmed == "" || trimmed != part || isWindowsDeviceName(trimmed) {
			return "", fmt.Errorf("path %q contains an unsafe component", value)
		}
	}
	return clean, nil
}

func isWindowsDeviceName(part string) bool {
	base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$" {
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}
