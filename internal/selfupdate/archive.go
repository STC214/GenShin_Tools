package selfupdate

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
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	PackageSchemaVersion = 1
	maxPackageFiles      = 128
	maxPackageFileBytes  = 128 << 20
	maxPackageTotalBytes = 512 << 20
	maxPackageManifest   = 1 << 20
)

var windowsDevicePattern = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\..*)?$`)

type PackageManifest struct {
	SchemaVersion int           `json:"schemaVersion"`
	Version       string        `json:"version"`
	Files         []PackageFile `json:"files"`
}

type PackageFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type StagedRelease struct {
	Directory      string
	Manifest       PackageManifest
	ManifestSHA256 string
}

func StagePackage(ctx context.Context, packagePath, stagingRoot, expectedVersion string, artifact Artifact) (StagedRelease, error) {
	if _, ok := parseVersion(expectedVersion); !ok {
		return StagedRelease{}, errors.New("expected update version is invalid")
	}
	if err := verifyDownloadedArtifact(packagePath, artifact); err != nil {
		return StagedRelease{}, err
	}
	stagingRoot, err := filepath.Abs(stagingRoot)
	if err != nil {
		return StagedRelease{}, err
	}
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return StagedRelease{}, err
	}
	if err := rejectReparse(stagingRoot); err != nil {
		return StagedRelease{}, fmt.Errorf("update staging root: %w", err)
	}
	archive, err := zip.OpenReader(packagePath)
	if err != nil {
		return StagedRelease{}, err
	}
	defer archive.Close()
	if len(archive.File) < 2 || len(archive.File) > maxPackageFiles+1 {
		return StagedRelease{}, errors.New("update ZIP entry count is outside limits")
	}
	entries := make(map[string]*zip.File, len(archive.File))
	var manifestData []byte
	var total uint64
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return StagedRelease{}, err
		}
		name, err := safeReleasePath(entry.Name)
		if err != nil {
			return StagedRelease{}, err
		}
		key := strings.ToLower(name)
		if _, exists := entries[key]; exists {
			return StagedRelease{}, fmt.Errorf("duplicate update ZIP entry %q", name)
		}
		entries[key] = entry
		if entry.FileInfo().IsDir() || entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() {
			return StagedRelease{}, fmt.Errorf("update ZIP entry %q is not a regular file", name)
		}
		if entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > maxPackageFileBytes {
			return StagedRelease{}, fmt.Errorf("update ZIP entry %q size is outside limits", name)
		}
		total += entry.UncompressedSize64
		if total > maxPackageTotalBytes {
			return StagedRelease{}, errors.New("update ZIP expands beyond 512 MiB")
		}
		if entry.UncompressedSize64 > 1<<20 && (entry.CompressedSize64 == 0 || entry.UncompressedSize64/entry.CompressedSize64 > 1000) {
			return StagedRelease{}, fmt.Errorf("update ZIP entry %q has an unsafe expansion ratio", name)
		}
		if name == "release.json" {
			reader, openErr := entry.Open()
			if openErr != nil {
				return StagedRelease{}, openErr
			}
			manifestData, err = io.ReadAll(io.LimitReader(reader, maxPackageManifest+1))
			closeErr := reader.Close()
			if err != nil || closeErr != nil || len(manifestData) > maxPackageManifest {
				return StagedRelease{}, errors.New("read release.json failed or exceeded 1 MiB")
			}
		}
	}
	if manifestData == nil {
		return StagedRelease{}, errors.New("update ZIP has no root release.json")
	}
	manifest, err := decodePackageManifest(manifestData, expectedVersion)
	if err != nil {
		return StagedRelease{}, err
	}
	declared := make(map[string]PackageFile, len(manifest.Files))
	for _, file := range manifest.Files {
		declared[strings.ToLower(file.Path)] = file
	}
	for key, entry := range entries {
		if key == "release.json" {
			continue
		}
		file, ok := declared[key]
		if !ok {
			return StagedRelease{}, fmt.Errorf("update ZIP contains undeclared file %q", entry.Name)
		}
		if int64(entry.UncompressedSize64) != file.Size {
			return StagedRelease{}, fmt.Errorf("update ZIP file %q length differs from release.json", entry.Name)
		}
	}
	for key, file := range declared {
		if _, ok := entries[key]; !ok {
			return StagedRelease{}, fmt.Errorf("declared update file %q is missing", file.Path)
		}
	}
	temporary, err := os.MkdirTemp(stagingRoot, ".version-")
	if err != nil {
		return StagedRelease{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()
	for _, file := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return StagedRelease{}, err
		}
		if err := extractPackageFile(entries[strings.ToLower(file.Path)], temporary, file); err != nil {
			return StagedRelease{}, err
		}
	}
	normalizedManifest := append(bytes.TrimSpace(manifestData), '\n')
	if err := writeExclusive(filepath.Join(temporary, "release.json"), normalizedManifest); err != nil {
		return StagedRelease{}, err
	}
	destination := filepath.Join(stagingRoot, expectedVersion)
	if _, err := os.Lstat(destination); err == nil {
		return StagedRelease{}, errors.New("staged update version already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return StagedRelease{}, err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return StagedRelease{}, err
	}
	committed = true
	manifestDigest := sha256.Sum256(normalizedManifest)
	return StagedRelease{Directory: destination, Manifest: manifest, ManifestSHA256: hex.EncodeToString(manifestDigest[:])}, nil
}

func VerifyStaged(ctx context.Context, directory, expectedVersion, expectedManifestSHA256 string) (PackageManifest, error) {
	if err := rejectReparse(directory); err != nil {
		return PackageManifest{}, err
	}
	manifestFile, err := os.Open(filepath.Join(directory, "release.json"))
	if err != nil {
		return PackageManifest{}, err
	}
	data, readErr := io.ReadAll(io.LimitReader(manifestFile, maxPackageManifest+1))
	closeErr := manifestFile.Close()
	if readErr != nil || closeErr != nil {
		return PackageManifest{}, errors.Join(readErr, closeErr)
	}
	if len(data) > maxPackageManifest {
		return PackageManifest{}, errors.New("staged release.json exceeds 1 MiB")
	}
	digest := sha256.Sum256(data)
	if !shaPattern.MatchString(expectedManifestSHA256) || hex.EncodeToString(digest[:]) != expectedManifestSHA256 {
		return PackageManifest{}, errors.New("staged release.json does not match the prepared update transaction")
	}
	manifest, err := decodePackageManifest(data, expectedVersion)
	if err != nil {
		return PackageManifest{}, err
	}
	expected := map[string]PackageFile{"release.json": {Path: "release.json"}}
	allowedDirectories := make(map[string]bool)
	for _, file := range manifest.Files {
		expected[strings.ToLower(file.Path)] = file
		parent := path.Dir(file.Path)
		for parent != "." {
			allowedDirectories[strings.ToLower(parent)] = true
			parent = path.Dir(parent)
		}
	}
	err = filepath.WalkDir(directory, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if filePath == directory {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("staged update contains a symbolic link")
		}
		if err := rejectReparse(filePath); err != nil {
			return err
		}
		if entry.IsDir() {
			relative, relativeErr := filepath.Rel(directory, filePath)
			if relativeErr != nil || !allowedDirectories[strings.ToLower(filepath.ToSlash(relative))] {
				return fmt.Errorf("staged update contains undeclared directory %q", relative)
			}
			return nil
		}
		relative, err := filepath.Rel(directory, filePath)
		if err != nil {
			return err
		}
		key := strings.ToLower(filepath.ToSlash(relative))
		file, ok := expected[key]
		if !ok {
			return fmt.Errorf("staged update contains undeclared file %q", relative)
		}
		delete(expected, key)
		if key == "release.json" {
			return nil
		}
		return verifyPackageFile(filePath, file)
	})
	if err != nil {
		return PackageManifest{}, err
	}
	if len(expected) != 0 {
		return PackageManifest{}, errors.New("staged update is missing declared files")
	}
	return manifest, nil
}

func verifyDownloadedArtifact(packagePath string, artifact Artifact) error {
	if err := validateDownloadArtifact(artifact); err != nil {
		return err
	}
	if err := rejectReparse(packagePath); err != nil {
		return err
	}
	info, err := os.Stat(packagePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() != artifact.Size {
		return errors.New("downloaded update package size or type does not match the signed manifest")
	}
	input, err := os.Open(packagePath)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, input)
	closeErr := input.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		return errors.New("downloaded update package SHA-256 does not match the signed manifest")
	}
	return nil
}

func decodePackageManifest(data []byte, expectedVersion string) (PackageManifest, error) {
	if len(data) > maxPackageManifest {
		return PackageManifest{}, errors.New("release.json exceeds 1 MiB")
	}
	var manifest PackageManifest
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return PackageManifest{}, fmt.Errorf("decode release.json: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return PackageManifest{}, errors.New("release.json contains trailing JSON")
	}
	if manifest.SchemaVersion != PackageSchemaVersion || manifest.Version != expectedVersion {
		return PackageManifest{}, errors.New("release.json schema or version does not match the signed update")
	}
	if len(manifest.Files) == 0 || len(manifest.Files) > maxPackageFiles {
		return PackageManifest{}, errors.New("release.json file count is outside limits")
	}
	seen := make(map[string]bool, len(manifest.Files))
	previous := ""
	var total int64
	for _, file := range manifest.Files {
		name, err := safeReleasePath(file.Path)
		if err != nil || name == "release.json" || !allowedReleasePath(name) {
			return PackageManifest{}, fmt.Errorf("release.json contains disallowed path %q", file.Path)
		}
		key := strings.ToLower(name)
		if seen[key] || (previous != "" && key <= previous) {
			return PackageManifest{}, errors.New("release.json files must be unique and case-insensitively sorted")
		}
		seen[key], previous = true, key
		if file.Size <= 0 || file.Size > maxPackageFileBytes || !shaPattern.MatchString(file.SHA256) {
			return PackageManifest{}, fmt.Errorf("release.json size/hash is invalid for %q", file.Path)
		}
		total += file.Size
		if total > maxPackageTotalBytes {
			return PackageManifest{}, errors.New("release.json files exceed 512 MiB")
		}
	}
	for _, required := range []string{"build-info.json", "genshintools-injector.exe", "genshintools-updater.exe", "genshintools.exe", "license", "license_policy.md", "third_party_notices.md"} {
		if !seen[required] {
			return PackageManifest{}, fmt.Errorf("release.json is missing required file %q", required)
		}
	}
	return manifest, nil
}

func safeReleasePath(name string) (string, error) {
	if name == "" || len(name) > 240 || strings.Contains(name, "\\") || strings.Contains(name, ":") || strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("unsafe update archive path %q", name)
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != name {
		return "", fmt.Errorf("unsafe update archive path %q", name)
	}
	for _, component := range strings.Split(clean, "/") {
		if component == "" || component != strings.TrimRight(component, ". ") || windowsDevicePattern.MatchString(component) {
			return "", fmt.Errorf("unsafe Windows update path component %q", component)
		}
	}
	return clean, nil
}

func allowedReleasePath(name string) bool {
	switch name {
	case "GenshinTools.exe", "GenshinTools-injector.exe", "GenshinTools-updater.exe", "build-info.json", "LICENSE", "THIRD_PARTY_NOTICES.md", "LICENSE_POLICY.md":
		return true
	}
	if !strings.HasPrefix(name, "LICENSES/") || strings.Count(name, "/") != 1 {
		return false
	}
	extension := strings.ToLower(path.Ext(name))
	return extension == ".txt" || extension == ".md"
}

func extractPackageFile(entry *zip.File, root string, file PackageFile) error {
	destination := filepath.Join(root, filepath.FromSlash(file.Path))
	rootAbsolute, _ := filepath.Abs(root)
	destinationAbsolute, err := filepath.Abs(destination)
	if err != nil || !strings.HasPrefix(destinationAbsolute, rootAbsolute+string(filepath.Separator)) {
		return errors.New("update extraction path escapes staging root")
	}
	if err := os.MkdirAll(filepath.Dir(destinationAbsolute), 0o755); err != nil {
		return err
	}
	reader, err := entry.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	output, err := os.OpenFile(destinationAbsolute, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(reader, file.Size+1))
	if copyErr == nil && written != file.Size {
		copyErr = fmt.Errorf("update file %q length is %d, want %d", file.Path, written, file.Size)
	}
	if copyErr == nil && hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
		copyErr = fmt.Errorf("update file %q SHA-256 mismatch", file.Path)
	}
	if copyErr == nil {
		copyErr = output.Sync()
	}
	if closeErr := output.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}

func verifyPackageFile(filePath string, file PackageFile) error {
	info, err := os.Stat(filePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size {
		return fmt.Errorf("staged update file %q size or type is invalid", file.Path)
	}
	input, err := os.Open(filePath)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, input)
	closeErr := input.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
		return fmt.Errorf("staged update file %q SHA-256 mismatch", file.Path)
	}
	return nil
}

func fileSHA256(filePath string) (string, error) {
	input, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer input.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeExclusive(filePath string, data []byte) error {
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}
