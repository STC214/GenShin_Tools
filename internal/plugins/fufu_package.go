package plugins

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"genshintools/internal/game"
	"genshintools/internal/injection"
)

const maxDownloadTokenBytes = 4096

func ParseFufuDownloadToken(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "{") {
		var response struct {
			Retcode int `json:"retcode"`
			Data    struct {
				DownloadToken string `json:"dl_token"`
			} `json:"data"`
		}
		decoder := json.NewDecoder(strings.NewReader(value))
		if err := decoder.Decode(&response); err != nil {
			return "", errors.New("Fufu verification JSON is invalid")
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return "", errors.New("Fufu verification JSON contains trailing data")
		}
		if response.Retcode != 0 {
			return "", fmt.Errorf("Fufu verification retcode is %d", response.Retcode)
		}
		value = response.Data.DownloadToken
	}
	if len(value) < 8 || len(value) > maxDownloadTokenBytes {
		return "", errors.New("Fufu download token length is invalid")
	}
	for _, char := range value {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return "", errors.New("Fufu download token contains whitespace or control characters")
		}
	}
	return value, nil
}

func DownloadFufuPackage(ctx context.Context, client *http.Client, item CatalogItem, downloadToken, destination string) error {
	return downloadFufuPackage(ctx, client, item, downloadToken, destination, FufuStoreBaseURL)
}

func downloadFufuPackage(ctx context.Context, client *http.Client, item CatalogItem, downloadToken, destination, trustedOrigin string) error {
	if err := validateFufuCatalogItem(item, trustedOrigin); err != nil {
		return err
	}
	token, err := ParseFufuDownloadToken(downloadToken)
	if err != nil {
		return err
	}
	endpoint, err := url.Parse(item.PackageURL)
	if err != nil {
		return err
	}
	query := endpoint.Query()
	query.Set("dl_token", token)
	endpoint.RawQuery = query.Encode()
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	clientCopy := *client
	originalRedirectCheck := clientCopy.CheckRedirect
	clientCopy.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL == nil || !sameURLOrigin(trustedOrigin, request.URL.String()) {
			return errors.New("refusing to send a Fufu download token across origins")
		}
		if len(via) >= 5 {
			return errors.New("Fufu package redirect limit exceeded")
		}
		if originalRedirectCheck != nil {
			return originalRedirectCheck(request, via)
		}
		return nil
	}
	client = &clientCopy
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/zip, application/octet-stream")
	request.Header.Set("User-Agent", "GenshinTools-FufuStoreAdapter/1")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.Request == nil || response.Request.URL == nil || !sameURLOrigin(trustedOrigin, response.Request.URL.String()) {
		return errors.New("Fufu package redirect changed the trusted HTTPS origin")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Fufu package HTTP status %d", response.StatusCode)
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "json") {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		var gate struct {
			Retcode int    `json:"retcode"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &gate) == nil && gate.Retcode != 0 {
			return fmt.Errorf("Fufu download gate rejected the token (retcode %d): %s", gate.Retcode, strings.TrimSpace(gate.Message))
		}
		return errors.New("Fufu package endpoint returned JSON instead of a ZIP")
	}
	if response.ContentLength >= 0 && response.ContentLength != item.PackageSize {
		return fmt.Errorf("Fufu package Content-Length is %d, want %d", response.ContentLength, item.PackageSize)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".fufu-download-*.tmp")
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
		return fmt.Errorf("Fufu package length is %d, want %d", written, item.PackageSize)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), item.PackageSHA256) {
		return errors.New("Fufu package SHA-256 mismatch")
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

func InstallFufuPackage(ctx context.Context, packagePath string, item CatalogItem, layout Layout, candidate game.Candidate, state *State) (InstallResult, error) {
	if state == nil {
		return InstallResult{}, errors.New("plugin state is required")
	}
	if err := validateFufuCatalogItem(item, FufuStoreBaseURL); err != nil {
		return InstallResult{}, err
	}
	for _, dependency := range item.Dependencies {
		name := strings.TrimSpace(dependency.PluginName)
		project := strings.TrimSpace(dependency.ProjectName)
		version := strings.TrimSpace(dependency.ProjectVersion)
		if (name != "" && name != "无") || (project != "" && project != "无") || (version != "" && version != "无") {
			return InstallResult{}, fmt.Errorf("Fufu plugin dependency is not yet installed transactionally: %s / %s", name, project)
		}
	}
	if err := layout.Ensure(); err != nil {
		return InstallResult{}, err
	}
	if err := RecoverTransaction(layout, state); err != nil {
		return InstallResult{}, err
	}
	info, err := os.Stat(packagePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() != item.PackageSize {
		return InstallResult{}, errors.New("Fufu package file size does not match store metadata")
	}
	hash, err := fileSHA256(packagePath)
	if err != nil || !strings.EqualFold(hash, item.PackageSHA256) {
		return InstallResult{}, errors.New("Fufu package file hash does not match store metadata")
	}
	id := strings.ToLower(item.ID)
	if !idPattern.MatchString(id) {
		return InstallResult{}, errors.New("Fufu plugin id cannot be represented safely by the local module adapter")
	}
	stageRoot, err := os.MkdirTemp(layout.Staging, id+"-")
	if err != nil {
		return InstallResult{}, err
	}
	stageName := filepath.Base(stageRoot)
	candidateRoot := filepath.Join(stageRoot, "candidate")
	candidateDirectory := filepath.Join(candidateRoot, id)
	if err := os.MkdirAll(candidateDirectory, 0o755); err != nil {
		_ = safeRemoveAll(layout.Staging, stageRoot)
		return InstallResult{}, err
	}
	manifest, err := extractAndAdaptFufuPackage(ctx, packagePath, item, candidateRoot, candidateDirectory, candidate)
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

func extractAndAdaptFufuPackage(ctx context.Context, packagePath string, item CatalogItem, candidateRoot, candidateDirectory string, candidate game.Candidate) (Manifest, error) {
	archive, err := zip.OpenReader(packagePath)
	if err != nil {
		return Manifest{}, err
	}
	defer archive.Close()
	if len(archive.File) == 0 || len(archive.File) >= maxPackageFiles {
		return Manifest{}, errors.New("Fufu ZIP entry count is outside 1..255")
	}
	configName, prefix, err := selectFufuConfig(archive.File)
	if err != nil {
		return Manifest{}, err
	}
	var total uint64
	seen := map[string]bool{}
	files := make([]PackageFile, 0, len(archive.File)+1)
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		name, err := safeArchiveName(entry.Name)
		if err != nil {
			return Manifest{}, err
		}
		if prefix != "" {
			if name == prefix && entry.FileInfo().IsDir() {
				continue
			}
			if !strings.HasPrefix(name, prefix+"/") {
				return Manifest{}, fmt.Errorf("Fufu ZIP entry %q is outside the plugin root", name)
			}
			name = strings.TrimPrefix(name, prefix+"/")
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return Manifest{}, fmt.Errorf("Fufu ZIP link %q is not allowed", entry.Name)
		}
		if name == "" || strings.EqualFold(name, "plugin.json") || strings.EqualFold(name, "module.json") || !allowedPackageExtension(name) {
			return Manifest{}, fmt.Errorf("Fufu ZIP file type or reserved path is not allowed: %q", name)
		}
		key := strings.ToLower(name)
		if seen[key] {
			return Manifest{}, fmt.Errorf("duplicate Fufu ZIP entry %q", name)
		}
		seen[key] = true
		if entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > maxPackageFileSize {
			return Manifest{}, fmt.Errorf("Fufu ZIP entry %q size is invalid", name)
		}
		total += entry.UncompressedSize64
		if total > maxUncompressedBytes {
			return Manifest{}, errors.New("Fufu ZIP expands beyond 512 MiB")
		}
		if entry.UncompressedSize64 > 1<<20 && (entry.CompressedSize64 == 0 || entry.UncompressedSize64/entry.CompressedSize64 > 1000) {
			return Manifest{}, fmt.Errorf("Fufu ZIP entry %q has an unsafe expansion ratio", name)
		}
		declared := PackageFile{Path: name, Size: int64(entry.UncompressedSize64)}
		if err := extractFufuFile(ctx, entry, candidateDirectory, &declared); err != nil {
			return Manifest{}, err
		}
		files = append(files, declared)
	}
	configRelative := strings.TrimPrefix(configName, prefix+"/")
	if prefix == "" {
		configRelative = configName
	}
	dllName, err := parseFufuConfigFile(filepath.Join(candidateDirectory, filepath.FromSlash(configRelative)))
	if err != nil {
		return Manifest{}, err
	}
	if path.Base(dllName) != dllName || !strings.EqualFold(path.Ext(dllName), ".dll") {
		return Manifest{}, errors.New("Fufu config File must be one root DLL file name")
	}
	if item.DLLFileName != "" && !strings.EqualFold(item.DLLFileName, dllName) {
		return Manifest{}, fmt.Errorf("Fufu config DLL %q differs from store metadata %q", dllName, item.DLLFileName)
	}
	dllPath := filepath.Join(candidateDirectory, dllName)
	if err := regularFile(dllPath); err != nil {
		return Manifest{}, fmt.Errorf("Fufu config target DLL: %w", err)
	}
	metadata, err := injection.InspectModuleFile(dllPath)
	if err != nil || !metadata.IsDLL || metadata.Architecture != "amd64" {
		return Manifest{}, fmt.Errorf("Fufu plugin DLL is not an auditable amd64 DLL: %w", err)
	}
	id := strings.ToLower(item.ID)
	module := injection.Manifest{SchemaVersion: injection.ManifestSchemaVersion, ID: id, Name: item.Name, SourceURL: item.SourceURL, License: item.License, AdapterAPI: injection.AdapterAPIVersion, DLL: dllName, SHA256: metadata.SHA256, Architecture: metadata.Architecture, FileVersion: metadata.FileVersion, AllowUnversioned: metadata.FileVersion == "", GameVersions: []string{candidate.Version}, GameExecutables: []string{candidate.ExeName}}
	moduleData, err := json.MarshalIndent(module, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err := atomicWrite(filepath.Join(candidateDirectory, "module.json"), append(moduleData, '\n')); err != nil {
		return Manifest{}, err
	}
	modulePayload := append(moduleData, '\n')
	files = append(files, PackageFile{Path: "module.json", Size: int64(len(modulePayload)), SHA256: fufuBytesSHA256(modulePayload)})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ID: id, Name: item.Name, Developer: item.Developer, Description: item.Description, Version: item.Version, Category: item.Category, Tags: append([]string(nil), item.Tags...), Capabilities: []string{"game.plugin"}, SourceURL: item.SourceURL, License: item.License, ModuleFile: "module.json", Files: files}
	if err := validateManifest(manifest, id); err != nil {
		return Manifest{}, fmt.Errorf("adapted Fufu plugin manifest: %w", err)
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err := atomicWrite(filepath.Join(candidateDirectory, "plugin.json"), append(manifestData, '\n')); err != nil {
		return Manifest{}, err
	}
	if _, err := injection.AuditModule(candidateRoot, id, candidate); err != nil {
		return Manifest{}, fmt.Errorf("adapted Fufu module audit: %w", err)
	}
	return manifest, nil
}

func selectFufuConfig(entries []*zip.File) (name, prefix string, err error) {
	bestDepth := int(^uint(0) >> 1)
	for _, entry := range entries {
		normalized, nameErr := safeArchiveName(entry.Name)
		if nameErr != nil {
			return "", "", nameErr
		}
		if entry.FileInfo().IsDir() || !strings.EqualFold(path.Base(normalized), "config.ini") {
			continue
		}
		depth := strings.Count(normalized, "/")
		if depth < bestDepth {
			name, bestDepth = normalized, depth
		} else if depth == bestDepth {
			return "", "", errors.New("Fufu ZIP has multiple top-level config.ini candidates")
		}
	}
	if name == "" {
		return "", "", errors.New("Fufu ZIP has no config.ini")
	}
	prefix = path.Dir(name)
	if prefix == "." {
		prefix = ""
	}
	return name, prefix, nil
}

func extractFufuFile(ctx context.Context, entry *zip.File, root string, declared *PackageFile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	destination, err := safeJoin(root, filepath.FromSlash(declared.Path))
	if err != nil {
		return err
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
	syncErr, closeErr := file.Sync(), file.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || written != declared.Size {
		return fmt.Errorf("extract Fufu file %q failed or length differed", declared.Path)
	}
	declared.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return nil
}

func parseFufuConfigFile(filePath string) (string, error) {
	data, err := readFileBounded(filePath, 1<<20)
	if err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})))
	inGeneral := false
	fileValue := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inGeneral = strings.EqualFold(strings.TrimSpace(line[1:len(line)-1]), "General")
			continue
		}
		if !inGeneral {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "File") {
			value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
			if fileValue != "" && !strings.EqualFold(fileValue, value) {
				return "", errors.New("Fufu config contains multiple File targets")
			}
			fileValue = value
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if fileValue == "" {
		return "", errors.New("Fufu config [General] has no File entry")
	}
	return fileValue, nil
}

func fufuBytesSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
