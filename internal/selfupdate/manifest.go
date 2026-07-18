// Package selfupdate verifies signed release manifests and update payloads.
package selfupdate

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	ManifestSchemaVersion = 1
	maxManifestBytes      = 1 << 20
	maxArtifactBytes      = 2 << 30
)

var (
	semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
	keyIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	shaPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)

	// Set only with -ldflags from a protected release environment. It contains
	// key IDs mapped to base64 Ed25519 public keys, never private keys.
	trustedPublicKeysJSON   = "{}"
	trustedPublicKeysBase64 = ""
	updateManifestURL       = ""
)

type Manifest struct {
	SchemaVersion  int        `json:"schemaVersion"`
	Channel        string     `json:"channel"`
	Version        string     `json:"version"`
	PublishedUTC   string     `json:"publishedUtc"`
	MinimumVersion string     `json:"minimumVersion"`
	Artifacts      []Artifact `json:"artifacts"`
	KeyID          string     `json:"keyId"`
	Signature      string     `json:"signature"`
}

type Artifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Release struct {
	Manifest Manifest
	Artifact Artifact
}

type signedPayload struct {
	SchemaVersion  int        `json:"schemaVersion"`
	Channel        string     `json:"channel"`
	Version        string     `json:"version"`
	PublishedUTC   string     `json:"publishedUtc"`
	MinimumVersion string     `json:"minimumVersion"`
	Artifacts      []Artifact `json:"artifacts"`
	KeyID          string     `json:"keyId"`
}

func BuiltInKeys() (map[string]ed25519.PublicKey, error) {
	data := []byte(trustedPublicKeysJSON)
	if trustedPublicKeysBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(trustedPublicKeysBase64)
		if err != nil {
			return nil, errors.New("decode built-in update public keys")
		}
		data = decoded
	}
	return ParseTrustedKeys(data)
}

func BuiltInManifestURL() (string, error) {
	parsed, err := url.Parse(updateManifestURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("built-in update manifest URL is not configured")
	}
	return updateManifestURL, nil
}

func ParseTrustedKeys(data []byte) (map[string]ed25519.PublicKey, error) {
	var encoded map[string]string
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&encoded); err != nil {
		return nil, fmt.Errorf("decode trusted update keys: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("trusted update keys contain trailing JSON")
	}
	if len(encoded) > 16 {
		return nil, errors.New("too many trusted update keys")
	}
	keys := make(map[string]ed25519.PublicKey, len(encoded))
	for id, value := range encoded {
		if !keyIDPattern.MatchString(id) {
			return nil, errors.New("trusted update key ID is invalid")
		}
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("trusted update key %q is invalid", id)
		}
		keys[id] = ed25519.PublicKey(append([]byte(nil), decoded...))
	}
	return keys, nil
}

func DecodeAndVerify(data []byte, keys map[string]ed25519.PublicKey, currentVersion, targetOS, targetArch string, now time.Time) (Release, error) {
	if len(data) > maxManifestBytes {
		return Release{}, errors.New("update manifest exceeds 1 MiB")
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Release{}, fmt.Errorf("decode update manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Release{}, errors.New("update manifest contains trailing JSON")
	}
	if err := validateManifest(manifest, now); err != nil {
		return Release{}, err
	}
	if _, ok := parseVersion(currentVersion); !ok {
		return Release{}, errors.New("current application version is not valid SemVer")
	}
	if CompareVersions(manifest.Version, currentVersion) <= 0 {
		return Release{}, errors.New("update manifest does not contain a newer version")
	}
	if CompareVersions(currentVersion, manifest.MinimumVersion) < 0 {
		return Release{}, errors.New("current version is below the update's minimum supported version")
	}
	key, ok := keys[manifest.KeyID]
	if !ok || len(key) != ed25519.PublicKeySize {
		return Release{}, errors.New("update manifest key is not trusted")
	}
	signature, err := base64.StdEncoding.DecodeString(manifest.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return Release{}, errors.New("update manifest signature encoding is invalid")
	}
	payload, err := CanonicalPayload(manifest)
	if err != nil {
		return Release{}, err
	}
	if !ed25519.Verify(key, payload, signature) {
		return Release{}, errors.New("update manifest signature is invalid")
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.OS == targetOS && artifact.Arch == targetArch {
			return Release{Manifest: manifest, Artifact: artifact}, nil
		}
	}
	return Release{}, errors.New("update manifest has no artifact for this platform")
}

func CanonicalPayload(manifest Manifest) ([]byte, error) {
	if err := validateManifest(manifest, time.Time{}); err != nil {
		return nil, err
	}
	payload := signedPayload{SchemaVersion: manifest.SchemaVersion, Channel: manifest.Channel, Version: manifest.Version, PublishedUTC: manifest.PublishedUTC, MinimumVersion: manifest.MinimumVersion, Artifacts: manifest.Artifacts, KeyID: manifest.KeyID}
	return json.Marshal(payload)
}

func validateManifest(manifest Manifest, now time.Time) error {
	if manifest.SchemaVersion != ManifestSchemaVersion || manifest.Channel != "stable" || !keyIDPattern.MatchString(manifest.KeyID) {
		return errors.New("unsupported update manifest schema, channel, or key ID")
	}
	if _, ok := parseVersion(manifest.Version); !ok {
		return errors.New("update version is not valid SemVer")
	}
	if _, ok := parseVersion(manifest.MinimumVersion); !ok || CompareVersions(manifest.MinimumVersion, manifest.Version) > 0 {
		return errors.New("update minimumVersion is invalid")
	}
	published, err := time.Parse(time.RFC3339, manifest.PublishedUTC)
	if err != nil || (!now.IsZero() && published.After(now.Add(5*time.Minute))) {
		return errors.New("update publishedUtc is invalid or in the future")
	}
	if len(manifest.Artifacts) == 0 || len(manifest.Artifacts) > 16 {
		return errors.New("update manifest requires 1..16 artifacts")
	}
	seen := make(map[string]bool, len(manifest.Artifacts))
	previous := ""
	for _, artifact := range manifest.Artifacts {
		identity := artifact.OS + "/" + artifact.Arch
		if seen[identity] || (previous != "" && identity <= previous) {
			return errors.New("update artifacts must be unique and sorted by os/arch")
		}
		seen[identity], previous = true, identity
		if artifact.OS == "" || artifact.Arch == "" || len(artifact.OS) > 32 || len(artifact.Arch) > 32 || artifact.Size <= 0 || artifact.Size > maxArtifactBytes || !shaPattern.MatchString(artifact.SHA256) {
			return errors.New("update artifact identity, size, or SHA-256 is invalid")
		}
		parsed, err := url.Parse(artifact.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			return errors.New("update artifact URL must be an absolute HTTPS URL without credentials or fragment")
		}
		if _, err := hex.DecodeString(artifact.SHA256); err != nil {
			return errors.New("update artifact SHA-256 is invalid")
		}
	}
	return nil
}

type semanticVersion struct {
	core [3]uint64
	pre  []string
}

func parseVersion(value string) (semanticVersion, bool) {
	match := semverPattern.FindStringSubmatch(value)
	if match == nil {
		return semanticVersion{}, false
	}
	var version semanticVersion
	for index := range version.core {
		parsed, err := strconv.ParseUint(match[index+1], 10, 64)
		if err != nil {
			return semanticVersion{}, false
		}
		version.core[index] = parsed
	}
	if match[4] != "" {
		version.pre = strings.Split(match[4], ".")
		for _, identifier := range version.pre {
			if len(identifier) > 1 && identifier[0] == '0' && allDigits(identifier) {
				return semanticVersion{}, false
			}
		}
	}
	return version, true
}

func CompareVersions(left, right string) int {
	a, aOK := parseVersion(left)
	b, bOK := parseVersion(right)
	if !aOK || !bOK {
		return 0
	}
	for index := range a.core {
		if a.core[index] < b.core[index] {
			return -1
		}
		if a.core[index] > b.core[index] {
			return 1
		}
	}
	if len(a.pre) == 0 && len(b.pre) > 0 {
		return 1
	}
	if len(a.pre) > 0 && len(b.pre) == 0 {
		return -1
	}
	for index := 0; index < len(a.pre) && index < len(b.pre); index++ {
		if comparison := compareIdentifier(a.pre[index], b.pre[index]); comparison != 0 {
			return comparison
		}
	}
	return sign(len(a.pre) - len(b.pre))
}

func compareIdentifier(left, right string) int {
	leftNumeric, rightNumeric := allDigits(left), allDigits(right)
	if leftNumeric && rightNumeric {
		if len(left) != len(right) {
			return sign(len(left) - len(right))
		}
		return strings.Compare(left, right)
	}
	if leftNumeric {
		return -1
	}
	if rightNumeric {
		return 1
	}
	return strings.Compare(left, right)
}

func allDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != ""
}

func sign(value int) int {
	if value < 0 {
		return -1
	}
	if value > 0 {
		return 1
	}
	return 0
}

func SortedKeyIDs(keys map[string]ed25519.PublicKey) []string {
	result := make([]string, 0, len(keys))
	for id := range keys {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}
