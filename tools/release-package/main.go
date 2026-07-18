package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"genshintools/internal/selfupdate"
	"golang.org/x/sys/windows"
)

const (
	maxReleaseFileBytes = 128 << 20
	maxReleaseBytes     = 512 << 20
)

var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$`)

type options struct {
	dist    string
	output  string
	version string
}

type sourceFile struct {
	name string
	path string
	size int64
}

func main() {
	var options options
	flag.StringVar(&options.dist, "dist", "dist", "verified build output directory")
	flag.StringVar(&options.output, "output", "", "candidate ZIP output path")
	flag.StringVar(&options.version, "version", "", "product SemVer")
	flag.Parse()
	if err := packageRelease(options); err != nil {
		fmt.Fprintln(os.Stderr, "release package failed:", err)
		os.Exit(1)
	}
}

func packageRelease(options options) error {
	if !versionPattern.MatchString(options.version) {
		return errors.New("--version must be SemVer compatible")
	}
	if strings.TrimSpace(options.output) == "" || !strings.EqualFold(filepath.Ext(options.output), ".zip") {
		return errors.New("--output must name a ZIP file")
	}
	dist, err := filepath.Abs(options.dist)
	if err != nil {
		return err
	}
	output, err := filepath.Abs(options.output)
	if err != nil {
		return err
	}
	if err := rejectReparse(dist); err != nil {
		return fmt.Errorf("dist directory: %w", err)
	}
	files, manifest, err := collectReleaseFiles(dist, options.version)
	if err != nil {
		return err
	}
	if err := writeArchive(output, manifest, files); err != nil {
		return err
	}
	packageSize, packageHash, err := hashFile(output, maxReleaseBytes)
	if err != nil {
		return err
	}
	verificationRoot, err := os.MkdirTemp(filepath.Dir(output), ".release-verify-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(verificationRoot)
	artifact := selfupdate.Artifact{OS: "windows", Arch: "amd64", URL: "https://updates.example.invalid/" + filepath.Base(output), Size: packageSize, SHA256: packageHash}
	staged, err := selfupdate.StagePackage(context.Background(), output, verificationRoot, options.version, artifact)
	if err != nil {
		_ = os.Remove(output)
		return fmt.Errorf("reopen candidate ZIP: %w", err)
	}
	if len(staged.Manifest.Files) != len(manifest.Files) {
		_ = os.Remove(output)
		return errors.New("reopened candidate ZIP file count differs")
	}
	checksum := []byte(packageHash + "  " + filepath.Base(output) + "\n")
	if err := writeAtomic(output+".sha256", checksum, 0o644); err != nil {
		return err
	}
	fmt.Printf("Packaged %s\nSHA-256 %s\nFiles %d\n", output, packageHash, len(files)+1)
	return nil
}

func collectReleaseFiles(dist, version string) ([]sourceFile, selfupdate.PackageManifest, error) {
	names := []string{"GenshinTools.exe", "GenshinTools-injector.exe", "GenshinTools-updater.exe", "build-info.json", "LICENSE_POLICY.md", "THIRD_PARTY_NOTICES.md"}
	licensesRoot := filepath.Join(dist, "LICENSES")
	if err := rejectReparse(licensesRoot); err != nil {
		return nil, selfupdate.PackageManifest{}, fmt.Errorf("licenses directory: %w", err)
	}
	licenses, err := os.ReadDir(licensesRoot)
	if err != nil {
		return nil, selfupdate.PackageManifest{}, err
	}
	for _, entry := range licenses {
		if entry.IsDir() {
			return nil, selfupdate.PackageManifest{}, fmt.Errorf("nested license directory %q is not allowed", entry.Name())
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if extension != ".txt" && extension != ".md" {
			return nil, selfupdate.PackageManifest{}, fmt.Errorf("license file %q has an unsupported extension", entry.Name())
		}
		names = append(names, filepath.ToSlash(filepath.Join("LICENSES", entry.Name())))
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	manifest := selfupdate.PackageManifest{SchemaVersion: selfupdate.PackageSchemaVersion, Version: version}
	files := make([]sourceFile, 0, len(names))
	var total int64
	for _, name := range names {
		path := filepath.Join(dist, filepath.FromSlash(name))
		if err := rejectReparse(path); err != nil {
			return nil, selfupdate.PackageManifest{}, fmt.Errorf("release file %s: %w", name, err)
		}
		size, digest, err := hashFile(path, maxReleaseFileBytes)
		if err != nil {
			return nil, selfupdate.PackageManifest{}, fmt.Errorf("release file %s: %w", name, err)
		}
		if size <= 0 || size > maxReleaseFileBytes {
			return nil, selfupdate.PackageManifest{}, fmt.Errorf("release file %s has unsafe size %d", name, size)
		}
		total += size
		if total > maxReleaseBytes {
			return nil, selfupdate.PackageManifest{}, errors.New("release files exceed 512 MiB")
		}
		files = append(files, sourceFile{name: name, path: path, size: size})
		manifest.Files = append(manifest.Files, selfupdate.PackageFile{Path: name, Size: size, SHA256: digest})
	}
	if err := verifyBuildInfo(filepath.Join(dist, "build-info.json"), version); err != nil {
		return nil, selfupdate.PackageManifest{}, err
	}
	return files, manifest, nil
}

func verifyBuildInfo(path, version string) error {
	data, err := readBounded(path, 1<<20)
	if err != nil {
		return err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	var info struct {
		Version string `json:"version"`
		Target  string `json:"target"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	// build-info intentionally carries additional audited build fields.
	if err := decoder.Decode(&info); err != nil || info.Version != version || info.Target != "windows/amd64" {
		return errors.New("build-info.json does not match package version/target")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("build-info.json contains trailing JSON")
	}
	return nil
}

func writeArchive(output string, manifest selfupdate.PackageManifest, files []sourceFile) error {
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(output), ".release-package-*.tmp")
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
	archive := zip.NewWriter(temporary)
	manifestData, err := json.Marshal(manifest)
	if err == nil {
		manifestData = append(manifestData, '\n')
		err = writeZIPBytes(archive, "release.json", manifestData)
	}
	for _, file := range files {
		if err != nil {
			break
		}
		err = writeZIPFile(archive, file)
	}
	err = errors.Join(err, archive.Close(), temporary.Sync(), temporary.Close())
	if err != nil {
		return err
	}
	if err := os.Remove(output); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(temporaryPath, output); err != nil {
		return err
	}
	committed = true
	return nil
}

func writeZIPBytes(archive *zip.Writer, name string, data []byte) error {
	header := zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0o644)
	header.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	writer, err := archive.CreateHeader(&header)
	if err != nil {
		return err
	}
	_, err = writer.Write(data)
	return err
}

func writeZIPFile(archive *zip.Writer, file sourceFile) error {
	header := zip.FileHeader{Name: file.name, Method: zip.Deflate}
	header.SetMode(0o644)
	header.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	writer, err := archive.CreateHeader(&header)
	if err != nil {
		return err
	}
	input, err := os.Open(file.path)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(writer, io.LimitReader(input, file.size+1))
	closeErr := input.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if written != file.size {
		return fmt.Errorf("release file %s changed while packaging", file.name)
	}
	return nil
}

func hashFile(path string, maximum int64) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, io.LimitReader(file, maximum+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return 0, "", errors.Join(copyErr, closeErr)
	}
	if size > maximum {
		return 0, "", fmt.Errorf("file exceeds %d bytes", maximum)
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

func readBounded(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, errors.New("file exceeds safety limit")
	}
	return data, nil
}

func rejectReparse(path string) error {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("reparse points are not allowed")
	}
	return nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".checksum-*.tmp")
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
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}
