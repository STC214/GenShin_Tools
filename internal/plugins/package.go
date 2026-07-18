package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"genshintools/internal/game"
	"genshintools/internal/injection"
)

const (
	maxPackageSize       = 256 << 20
	maxPackageFiles      = 256
	maxUncompressedBytes = 512 << 20
	maxPackageFileSize   = 256 << 20
)

type InstallResult struct {
	Manifest        Manifest
	PreviousVersion string
	RollbackReady   bool
}

// InspectLocalPackage derives the immutable identity used by the normal
// installer. InstallLocalPackage still performs the complete archive, hash,
// PE and game compatibility audit before activating anything.
func InspectLocalPackage(packagePath string) (CatalogItem, error) {
	info, err := os.Stat(packagePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxPackageSize {
		return CatalogItem{}, errors.New("local plugin package is not a regular ZIP within 256 MiB")
	}
	archive, err := zip.OpenReader(packagePath)
	if err != nil {
		return CatalogItem{}, err
	}
	defer archive.Close()
	var manifestData []byte
	for _, entry := range archive.File {
		name, pathErr := safeArchiveName(entry.Name)
		if pathErr != nil {
			return CatalogItem{}, pathErr
		}
		if name != "plugin.json" || entry.FileInfo().IsDir() {
			continue
		}
		if manifestData != nil {
			return CatalogItem{}, errors.New("local plugin package contains duplicate plugin.json")
		}
		reader, openErr := entry.Open()
		if openErr != nil {
			return CatalogItem{}, openErr
		}
		manifestData, err = io.ReadAll(io.LimitReader(reader, 1<<20+1))
		closeErr := reader.Close()
		if err != nil || closeErr != nil || len(manifestData) > 1<<20 {
			return CatalogItem{}, errors.New("read local plugin manifest failed or exceeded 1 MiB")
		}
	}
	if manifestData == nil {
		return CatalogItem{}, errors.New("local plugin package has no root plugin.json")
	}
	var identity struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(manifestData, &identity); err != nil || !idPattern.MatchString(identity.ID) {
		return CatalogItem{}, errors.New("local plugin id is invalid")
	}
	manifest, err := decodePluginManifest(manifestData, identity.ID)
	if err != nil {
		return CatalogItem{}, err
	}
	hash, err := fileSHA256(packagePath)
	if err != nil {
		return CatalogItem{}, err
	}
	item := CatalogItem{ID: manifest.ID, Name: manifest.Name, Developer: manifest.Developer, Description: manifest.Description, Version: manifest.Version, Category: manifest.Category, Tags: manifest.Tags, Capabilities: manifest.Capabilities, SourceURL: manifest.SourceURL, License: manifest.License, PackageURL: "https://local.invalid/" + manifest.ID + ".zip", PackageSize: info.Size(), PackageSHA256: hash, UpdatedUTC: time.Unix(0, 0).UTC().Format(time.RFC3339)}
	if err := validateCatalogItem(item); err != nil {
		return CatalogItem{}, err
	}
	return item, nil
}

func DownloadPackage(ctx context.Context, client *http.Client, item CatalogItem, destination string) error {
	if err := validateCatalogItem(item); err != nil {
		return err
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, item.PackageURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/zip, application/octet-stream")
	request.Header.Set("User-Agent", "GenshinTools-PluginPackage/1")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.Request == nil || response.Request.URL == nil || !strings.EqualFold(response.Request.URL.Scheme, "https") {
		return errors.New("plugin package redirect left HTTPS")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("plugin package HTTP status %d", response.StatusCode)
	}
	if response.ContentLength >= 0 && response.ContentLength != item.PackageSize {
		return fmt.Errorf("plugin package Content-Length is %d, want %d", response.ContentLength, item.PackageSize)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".plugin-download-*.tmp")
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
	written, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(response.Body, item.PackageSize+1))
	if err != nil {
		return err
	}
	if written != item.PackageSize {
		return fmt.Errorf("plugin package length is %d, want %d", written, item.PackageSize)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), item.PackageSHA256) {
		return errors.New("plugin package SHA-256 mismatch")
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

func InstallLocalPackage(ctx context.Context, packagePath string, item CatalogItem, layout Layout, candidate game.Candidate, state *State) (InstallResult, error) {
	if state == nil {
		return InstallResult{}, errors.New("plugin state is required")
	}
	if err := layout.Ensure(); err != nil {
		return InstallResult{}, err
	}
	if err := RecoverTransaction(layout, state); err != nil {
		return InstallResult{}, err
	}
	if err := validateCatalogItem(item); err != nil {
		return InstallResult{}, err
	}
	info, err := os.Stat(packagePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() != item.PackageSize {
		return InstallResult{}, errors.New("plugin package file size does not match catalog")
	}
	hash, err := fileSHA256(packagePath)
	if err != nil || !strings.EqualFold(hash, item.PackageSHA256) {
		return InstallResult{}, errors.New("plugin package file hash does not match catalog")
	}
	stageRoot, err := os.MkdirTemp(layout.Staging, item.ID+"-")
	if err != nil {
		return InstallResult{}, err
	}
	stageName := filepath.Base(stageRoot)
	candidateRoot := filepath.Join(stageRoot, "candidate")
	candidateDirectory := filepath.Join(candidateRoot, item.ID)
	if err := os.MkdirAll(candidateDirectory, 0o755); err != nil {
		_ = safeRemoveAll(layout.Staging, stageRoot)
		return InstallResult{}, err
	}
	manifest, err := extractAndAuditPackage(ctx, packagePath, item, candidateRoot, candidateDirectory, candidate)
	if err != nil {
		_ = safeRemoveAll(layout.Staging, stageRoot)
		return InstallResult{}, err
	}
	result, err := commitInstall(layout, state, manifest, stageName, candidateDirectory)
	if err != nil {
		return InstallResult{}, err
	}
	return result, nil
}

func extractAndAuditPackage(ctx context.Context, packagePath string, item CatalogItem, candidateRoot, candidateDirectory string, candidate game.Candidate) (Manifest, error) {
	archive, err := zip.OpenReader(packagePath)
	if err != nil {
		return Manifest{}, err
	}
	defer archive.Close()
	if len(archive.File) == 0 || len(archive.File) > maxPackageFiles {
		return Manifest{}, errors.New("plugin ZIP entry count is outside 1..256")
	}
	entries := map[string]*zip.File{}
	var manifestData []byte
	var total uint64
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		name, err := safeArchiveName(entry.Name)
		if err != nil {
			return Manifest{}, err
		}
		key := strings.ToLower(name)
		if _, exists := entries[key]; exists {
			return Manifest{}, fmt.Errorf("duplicate plugin ZIP entry %q", name)
		}
		entries[key] = entry
		if entry.Mode()&os.ModeSymlink != 0 {
			return Manifest{}, fmt.Errorf("plugin ZIP link %q is not allowed", name)
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.UncompressedSize64 > maxPackageFileSize {
			return Manifest{}, fmt.Errorf("plugin ZIP entry %q exceeds 256 MiB", name)
		}
		total += entry.UncompressedSize64
		if total > maxUncompressedBytes {
			return Manifest{}, errors.New("plugin ZIP expands beyond 512 MiB")
		}
		if entry.UncompressedSize64 > 1<<20 && (entry.CompressedSize64 == 0 || entry.UncompressedSize64/entry.CompressedSize64 > 1000) {
			return Manifest{}, fmt.Errorf("plugin ZIP entry %q has an unsafe expansion ratio", name)
		}
		if name == "plugin.json" {
			reader, err := entry.Open()
			if err != nil {
				return Manifest{}, err
			}
			manifestData, err = io.ReadAll(io.LimitReader(reader, 1<<20+1))
			closeErr := reader.Close()
			if err != nil || closeErr != nil || len(manifestData) > 1<<20 {
				return Manifest{}, errors.New("read plugin.json failed or exceeded 1 MiB")
			}
		}
	}
	if manifestData == nil {
		return Manifest{}, errors.New("plugin ZIP has no root plugin.json")
	}
	manifest, err := decodePluginManifest(manifestData, item.ID)
	if err != nil {
		return Manifest{}, err
	}
	if err := matchCatalogManifest(item, manifest); err != nil {
		return Manifest{}, err
	}
	declared := map[string]PackageFile{}
	for _, file := range manifest.Files {
		declared[strings.ToLower(file.Path)] = file
	}
	for key, entry := range entries {
		if entry.FileInfo().IsDir() || key == "plugin.json" {
			continue
		}
		file, ok := declared[key]
		if !ok {
			return Manifest{}, fmt.Errorf("plugin ZIP contains undeclared file %q", entry.Name)
		}
		if int64(entry.UncompressedSize64) != file.Size {
			return Manifest{}, fmt.Errorf("plugin ZIP file %q length differs from manifest", entry.Name)
		}
	}
	for key, file := range declared {
		entry, ok := entries[key]
		if !ok || entry.FileInfo().IsDir() {
			return Manifest{}, fmt.Errorf("declared plugin file %q is missing", file.Path)
		}
		if err := extractVerifiedFile(ctx, entry, candidateDirectory, file); err != nil {
			return Manifest{}, err
		}
	}
	if err := atomicWrite(filepath.Join(candidateDirectory, "plugin.json"), append(manifestData, '\n')); err != nil {
		return Manifest{}, err
	}
	if manifest.ConfigSchema != "" {
		if _, err := LoadConfigSchema(filepath.Join(candidateDirectory, manifest.ConfigSchema)); err != nil {
			return Manifest{}, fmt.Errorf("plugin config schema: %w", err)
		}
	}
	if _, err := injection.AuditModule(candidateRoot, item.ID, candidate); err != nil {
		return Manifest{}, fmt.Errorf("S09 module audit: %w", err)
	}
	return manifest, nil
}

func validatePackageFiles(manifest Manifest) error {
	if len(manifest.Files) == 0 || len(manifest.Files) >= maxPackageFiles {
		return errors.New("plugin manifest requires 1..255 declared files")
	}
	seen := map[string]bool{}
	var total int64
	for _, file := range manifest.Files {
		name, err := safeArchiveName(file.Path)
		if err != nil || name == "plugin.json" || strings.HasSuffix(name, "/") {
			return fmt.Errorf("invalid declared plugin file %q", file.Path)
		}
		if !allowedPackageExtension(name) {
			return fmt.Errorf("plugin file type is not allowed: %q", name)
		}
		key := strings.ToLower(name)
		if seen[key] {
			return fmt.Errorf("duplicate declared plugin file %q", name)
		}
		seen[key] = true
		if file.Size <= 0 || file.Size > maxPackageFileSize || !validSHA256(file.SHA256) {
			return fmt.Errorf("invalid size/hash for plugin file %q", name)
		}
		total += file.Size
		if total > maxUncompressedBytes {
			return errors.New("declared plugin files exceed 512 MiB")
		}
	}
	if !seen[manifest.ModuleFile] {
		return errors.New("plugin module manifest is not declared in files")
	}
	if manifest.ConfigSchema != "" && !seen[manifest.ConfigSchema] {
		return errors.New("plugin config schema is not declared in files")
	}
	return nil
}

func safeArchiveName(name string) (string, error) {
	if name == "" || strings.Contains(name, "\\") || strings.Contains(name, ":") || strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("unsafe plugin archive path %q", name)
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != strings.TrimSuffix(name, "/") {
		if !(strings.HasSuffix(name, "/") && clean == strings.TrimSuffix(name, "/")) {
			return "", fmt.Errorf("unsafe plugin archive path %q", name)
		}
	}
	return clean, nil
}

func allowedPackageExtension(name string) bool {
	extension := strings.ToLower(path.Ext(name))
	return containsExact([]string{".dll", ".json", ".ini", ".md", ".txt", ".png", ".jpg", ".jpeg", ".webp"}, extension)
}

func decodePluginManifest(data []byte, id string) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, errors.New("plugin manifest contains trailing JSON data")
	}
	if err := validateManifest(manifest, id); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func matchCatalogManifest(item CatalogItem, manifest Manifest) error {
	if item.ID != manifest.ID || item.Name != manifest.Name || item.Developer != manifest.Developer || item.Description != manifest.Description || item.Version != manifest.Version || item.Category != manifest.Category || item.SourceURL != manifest.SourceURL || item.License != manifest.License {
		return errors.New("plugin package identity does not match catalog")
	}
	if strings.Join(item.Tags, "\x00") != strings.Join(manifest.Tags, "\x00") || strings.Join(item.Capabilities, "\x00") != strings.Join(manifest.Capabilities, "\x00") {
		return errors.New("plugin package scope does not match catalog")
	}
	return nil
}

func extractVerifiedFile(ctx context.Context, entry *zip.File, destinationRoot string, declared PackageFile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	destination := filepath.Join(destinationRoot, filepath.FromSlash(declared.Path))
	relative, err := filepath.Rel(destinationRoot, destination)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("plugin extraction path escaped staging")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	reader, err := entry.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(reader, declared.Size+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || written != declared.Size {
		return fmt.Errorf("extract plugin file %q failed or length differed", declared.Path)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), declared.SHA256) {
		return fmt.Errorf("plugin file %q SHA-256 mismatch", declared.Path)
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
