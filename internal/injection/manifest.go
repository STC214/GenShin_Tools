// Package injection implements the versioned, fail-closed S09 module adapter.
package injection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"genshintools/internal/game"
)

const (
	ManifestSchemaVersion = 1
	AdapterAPIVersion     = 1
	maxModuleSize         = 512 << 20
)

var moduleIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

type Manifest struct {
	SchemaVersion    int      `json:"schemaVersion"`
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	SourceURL        string   `json:"sourceUrl"`
	License          string   `json:"license"`
	AdapterAPI       int      `json:"adapterApi"`
	DLL              string   `json:"dll"`
	SHA256           string   `json:"sha256"`
	Architecture     string   `json:"architecture"`
	FileVersion      string   `json:"fileVersion,omitempty"`
	AllowUnversioned bool     `json:"allowUnversioned,omitempty"`
	GameVersions     []string `json:"gameVersions"`
	GameExecutables  []string `json:"gameExecutables"`
	RequiredExports  []string `json:"requiredExports,omitempty"`
}

type Audit struct {
	Manifest    Manifest
	ModuleDir   string
	DLLPath     string
	SHA256      string
	FileVersion string
	Exports     []string
}

func AuditModule(modulesRoot, id string, candidate game.Candidate) (Audit, error) {
	if !moduleIDPattern.MatchString(id) {
		return Audit{}, errors.New("module id must contain only lowercase ASCII letters, digits, dot, dash or underscore")
	}
	root, err := filepath.Abs(modulesRoot)
	if err != nil {
		return Audit{}, err
	}
	directory := filepath.Join(root, id)
	if err := rejectReparse(directory); err != nil {
		return Audit{}, fmt.Errorf("module directory: %w", err)
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil {
		return Audit{}, fmt.Errorf("stat module directory: %w", err)
	}
	if !directoryInfo.IsDir() {
		return Audit{}, errors.New("module directory path is not a directory")
	}
	manifestPath := filepath.Join(directory, "module.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return Audit{}, fmt.Errorf("read module manifest: %w", err)
	}
	if len(data) > 1<<20 {
		return Audit{}, errors.New("module manifest exceeds 1 MiB")
	}
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Audit{}, fmt.Errorf("decode module manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Audit{}, errors.New("module manifest contains trailing JSON data")
	}
	if err := validateManifest(manifest, id); err != nil {
		return Audit{}, err
	}
	dllPath := filepath.Join(directory, manifest.DLL)
	if err := rejectReparse(dllPath); err != nil {
		return Audit{}, fmt.Errorf("module DLL: %w", err)
	}
	info, err := os.Stat(dllPath)
	if err != nil {
		return Audit{}, fmt.Errorf("stat module DLL: %w", err)
	}
	if info.IsDir() {
		return Audit{}, errors.New("module DLL path is a directory")
	}
	if info.Size() <= 0 || info.Size() > maxModuleSize {
		return Audit{}, fmt.Errorf("module DLL size %d is outside 1..%d", info.Size(), maxModuleSize)
	}
	hash, err := fileSHA256(dllPath)
	if err != nil {
		return Audit{}, err
	}
	if !strings.EqualFold(hash, manifest.SHA256) {
		return Audit{}, fmt.Errorf("module SHA-256 mismatch: got %s", hash)
	}
	peInfo, err := inspectPE(dllPath)
	if err != nil {
		return Audit{}, fmt.Errorf("inspect module PE: %w", err)
	}
	if peInfo.Architecture != manifest.Architecture {
		return Audit{}, fmt.Errorf("module architecture is %s, manifest requires %s", peInfo.Architecture, manifest.Architecture)
	}
	if !peInfo.IsDLL {
		return Audit{}, errors.New("module PE is not marked as a DLL")
	}
	for _, imported := range peInfo.Imports {
		if filepath.Base(imported) != imported {
			return Audit{}, fmt.Errorf("module import %q is not a DLL file name", imported)
		}
		if exists(filepath.Join(directory, imported)) || exists(filepath.Join(candidate.Root, imported)) {
			return Audit{}, fmt.Errorf("module import %q is shadowed outside System32", imported)
		}
		lower := strings.ToLower(imported)
		if strings.HasPrefix(lower, "api-ms-win-") || strings.HasPrefix(lower, "ext-ms-") {
			continue
		}
		if !exists(filepath.Join(os.Getenv("SystemRoot"), "System32", imported)) {
			return Audit{}, fmt.Errorf("module import %q is not an audited System32 dependency", imported)
		}
	}
	for _, required := range manifest.RequiredExports {
		if !slices.Contains(peInfo.Exports, required) {
			return Audit{}, fmt.Errorf("module export %q is missing", required)
		}
	}
	version, err := fileVersion(dllPath)
	if err != nil {
		return Audit{}, fmt.Errorf("read module file version: %w", err)
	}
	if manifest.FileVersion != "" && version != manifest.FileVersion {
		return Audit{}, fmt.Errorf("module file version is %q, want %q", version, manifest.FileVersion)
	}
	if manifest.AllowUnversioned && version != "" {
		return Audit{}, fmt.Errorf("module declares unversioned but has file version %q", version)
	}
	if candidate.Version == "" || !containsFold(manifest.GameVersions, candidate.Version) {
		return Audit{}, fmt.Errorf("game version %q is not explicitly compatible", candidate.Version)
	}
	if !containsFold(manifest.GameExecutables, candidate.ExeName) {
		return Audit{}, fmt.Errorf("game executable %q is not explicitly compatible", candidate.ExeName)
	}
	if !strings.EqualFold(filepath.Base(candidate.Executable), candidate.ExeName) {
		return Audit{}, errors.New("game candidate executable name does not match its path")
	}
	gamePE, err := inspectPE(candidate.Executable)
	if err != nil {
		return Audit{}, fmt.Errorf("inspect game PE: %w", err)
	}
	if gamePE.Architecture != "amd64" {
		return Audit{}, fmt.Errorf("game architecture is %s, want amd64", gamePE.Architecture)
	}
	if gamePE.IsDLL {
		return Audit{}, errors.New("game executable PE is marked as a DLL")
	}
	return Audit{Manifest: manifest, ModuleDir: directory, DLLPath: dllPath, SHA256: hash, FileVersion: version, Exports: peInfo.Exports}, nil
}

func validateManifest(manifest Manifest, requestedID string) error {
	if manifest.SchemaVersion != ManifestSchemaVersion || manifest.AdapterAPI != AdapterAPIVersion {
		return errors.New("unsupported module manifest schema or adapter API")
	}
	if manifest.ID != requestedID || !moduleIDPattern.MatchString(manifest.ID) {
		return errors.New("module manifest id does not match its directory")
	}
	if strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.License) == "" {
		return errors.New("module name and license are required")
	}
	source, err := url.Parse(manifest.SourceURL)
	if err != nil || !strings.EqualFold(source.Scheme, "https") || source.Host == "" || source.User != nil {
		return errors.New("module sourceUrl must use HTTPS")
	}
	if manifest.DLL == "" || filepath.Base(manifest.DLL) != manifest.DLL || !strings.EqualFold(filepath.Ext(manifest.DLL), ".dll") {
		return errors.New("module DLL must be one relative DLL file name")
	}
	if _, err := hex.DecodeString(manifest.SHA256); err != nil || len(manifest.SHA256) != 64 {
		return errors.New("module SHA-256 must contain 64 hexadecimal characters")
	}
	if manifest.Architecture != "amd64" {
		return errors.New("only amd64 injection modules are supported")
	}
	if (manifest.FileVersion == "") == !manifest.AllowUnversioned {
		return errors.New("declare exactly one of fileVersion or allowUnversioned=true")
	}
	if len(manifest.GameVersions) == 0 || len(manifest.GameExecutables) == 0 {
		return errors.New("explicit gameVersions and gameExecutables are required")
	}
	for _, executable := range manifest.GameExecutables {
		if !strings.EqualFold(executable, "YuanShen.exe") && !strings.EqualFold(executable, "GenshinImpact.exe") {
			return fmt.Errorf("unsupported game executable %q", executable)
		}
	}
	for _, name := range manifest.RequiredExports {
		if name == "" || strings.ContainsAny(name, "\x00/\\") {
			return errors.New("required export name is invalid")
		}
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(hash.Sum(nil))), nil
}

func containsFold(values []string, value string) bool {
	for _, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
