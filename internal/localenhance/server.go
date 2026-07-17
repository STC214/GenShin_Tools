package localenhance

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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"genshintools/internal/resources"
)

const BilibiliSDKURL = "https://hyp-api.mihoyo.com/hyp/hyp-connect/api/getGameChannelSDKs?channel=14&game_ids[]=T2S0Gz4Dr2&launcher_id=umfgRO5gh5&sub_channel=0"

type QuickServer uint8

const (
	QuickOfficial QuickServer = iota + 1
	QuickBilibili
)

func (s QuickServer) String() string {
	if s == QuickBilibili {
		return "国服 B 服"
	}
	return "国服官服"
}

type sdkResponse struct {
	Retcode int     `json:"retcode"`
	Message string  `json:"message"`
	Data    sdkData `json:"data"`
}

type sdkData struct {
	SDKs []sdkEntry `json:"game_channel_sdks"`
}

type sdkEntry struct {
	Game               sdkGame    `json:"game"`
	Version            string     `json:"version"`
	Package            sdkPackage `json:"channel_sdk_pkg"`
	PackageVersionFile string     `json:"pkg_version_file_name"`
}

type sdkGame struct {
	ID  string `json:"id"`
	Biz string `json:"biz"`
}

type sdkPackage struct {
	URL              string `json:"url"`
	MD5              string `json:"md5"`
	Size             string `json:"size"`
	DecompressedSize string `json:"decompressed_size"`
}

func PrepareQuickServerSwitch(ctx context.Context, client *http.Client, gameRoot, stagingRoot string, target QuickServer) (resources.RepairPlan, error) {
	if target != QuickOfficial && target != QuickBilibili {
		return resources.RepairPlan{}, errors.New("unsupported quick server target")
	}
	if _, err := os.Stat(filepath.Join(gameRoot, "YuanShen.exe")); err != nil {
		return resources.RepairPlan{}, errors.New("quick official/Bilibili switch requires YuanShen.exe")
	}
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	metadata, err := fetchSDKMetadata(ctx, client)
	if err != nil {
		return resources.RepairPlan{}, err
	}
	configData, err := updateServerConfig(filepath.Join(gameRoot, "config.ini"), target)
	if err != nil {
		return resources.RepairPlan{}, err
	}
	var plan resources.RepairPlan
	configItem, changed, err := stagePlanFile(gameRoot, stagingRoot, "config.ini", configData, "server channel configuration")
	if err != nil {
		return resources.RepairPlan{}, err
	}
	if changed {
		plan.Items = append(plan.Items, configItem)
	}

	deprecated := []string{
		`YuanShen_Data\Plugins\PCGameSDK.dll`,
		`YuanShen_Data\Plugins\EOSSDK-Win64-Shipping.dll`,
		`GenshinImpact_Data\Plugins\PCGameSDK.dll`,
		`GenshinImpact_Data\Plugins\EOSSDK-Win64-Shipping.dll`,
		metadata.PackageVersionFile,
	}
	if target == QuickOfficial {
		for _, path := range deprecated {
			if _, err := os.Stat(filepath.Join(gameRoot, path)); err == nil {
				plan.Items = append(plan.Items, resources.PlanItem{File: resources.ManifestFile{Path: path}, Action: resources.ActionDelete, Reason: "remove Bilibili channel SDK"})
			}
		}
		return plan, nil
	}

	cacheRelative := `.server-cache\bilibili-sdk.zip`
	archiveManifest := resources.Manifest{SchemaVersion: resources.ManifestSchemaVersion, Version: metadata.Version, Kind: "bilibili-sdk", Files: []resources.ManifestFile{{
		Path: cacheRelative, Size: metadata.Size, Hash: resources.Hash{Algorithm: "md5", Digest: metadata.MD5}, URL: metadata.URL,
	}}}
	downloader := resources.NewDownloader()
	downloader.Client = client
	if err := downloader.Download(ctx, archiveManifest, stagingRoot); err != nil {
		return resources.RepairPlan{}, fmt.Errorf("download Bilibili SDK: %w", err)
	}
	archivePath := filepath.Join(stagingRoot, cacheRelative)
	items, err := extractSDKArchive(gameRoot, stagingRoot, archivePath, metadata.DecompressedSize)
	if err != nil {
		return resources.RepairPlan{}, err
	}
	_ = os.RemoveAll(filepath.Join(stagingRoot, ".server-cache"))
	plan.Items = append(plan.Items, items...)
	plan.DownloadBytes = metadata.Size
	return plan, nil
}

type sdkMetadata struct {
	Version            string
	URL                string
	MD5                string
	Size               int64
	DecompressedSize   int64
	PackageVersionFile string
}

func fetchSDKMetadata(ctx context.Context, client *http.Client) (sdkMetadata, error) {
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, BilibiliSDKURL, nil)
	if err != nil {
		return sdkMetadata{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return sdkMetadata{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return sdkMetadata{}, fmt.Errorf("Bilibili SDK API returned %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20+1))
	if err != nil || len(data) > 2<<20 {
		return sdkMetadata{}, errors.New("Bilibili SDK API response is invalid or too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result sdkResponse
	if err := decoder.Decode(&result); err != nil {
		return sdkMetadata{}, fmt.Errorf("decode Bilibili SDK schema: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return sdkMetadata{}, fmt.Errorf("decode Bilibili SDK schema: %w", err)
	}
	if result.Retcode != 0 || result.Message != "OK" || len(result.Data.SDKs) != 1 {
		return sdkMetadata{}, errors.New("Bilibili SDK API returned an unexpected result")
	}
	entry := result.Data.SDKs[0]
	if entry.Game.ID != "T2S0Gz4Dr2" || entry.Game.Biz != "hk4e_cn" || entry.Version == "" || entry.PackageVersionFile != "sdk_pkg_version" {
		return sdkMetadata{}, errors.New("Bilibili SDK identity does not match the audited game")
	}
	size, err := strconv.ParseInt(entry.Package.Size, 10, 64)
	if err != nil || size <= 0 || size > 512<<20 {
		return sdkMetadata{}, errors.New("Bilibili SDK compressed size is invalid")
	}
	decompressed, err := strconv.ParseInt(entry.Package.DecompressedSize, 10, 64)
	if err != nil || decompressed <= 0 || decompressed > 1<<30 {
		return sdkMetadata{}, errors.New("Bilibili SDK decompressed size is invalid")
	}
	if err := (resources.Hash{Algorithm: "md5", Digest: entry.Package.MD5}).Validate(); err != nil {
		return sdkMetadata{}, err
	}
	return sdkMetadata{Version: entry.Version, URL: entry.Package.URL, MD5: entry.Package.MD5, Size: size, DecompressedSize: decompressed, PackageVersionFile: entry.PackageVersionFile}, nil
}

func extractSDKArchive(gameRoot, stagingRoot, archivePath string, maximum int64) ([]resources.PlanItem, error) {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open Bilibili SDK ZIP: %w", err)
	}
	defer archive.Close()
	if len(archive.File) == 0 || len(archive.File) > 2048 {
		return nil, errors.New("Bilibili SDK ZIP entry count is invalid")
	}
	var total int64
	var items []resources.PlanItem
	seen := make(map[string]struct{})
	for _, entry := range archive.File {
		if entry.FileInfo().Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("Bilibili SDK ZIP contains a link: %s", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		relative, err := resources.NormalizeRelativePath(entry.Name)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(relative)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("duplicate Bilibili SDK ZIP path %s", relative)
		}
		seen[key] = struct{}{}
		total += int64(entry.UncompressedSize64)
		if total > maximum || entry.UncompressedSize64 > 512<<20 {
			return nil, errors.New("Bilibili SDK ZIP exceeds declared safety limit")
		}
		destination := filepath.Join(stagingRoot, relative)
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return nil, err
		}
		input, err := entry.Open()
		if err != nil {
			return nil, err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			input.Close()
			return nil, err
		}
		_, copyErr := io.Copy(output, io.LimitReader(input, int64(entry.UncompressedSize64)+1))
		closeErr := errors.Join(input.Close(), output.Sync(), output.Close())
		if copyErr != nil || closeErr != nil {
			return nil, errors.Join(copyErr, closeErr)
		}
		item, changed, err := stageExistingPlanFile(gameRoot, relative, destination, "install Bilibili channel SDK")
		if err != nil {
			return nil, err
		}
		if changed {
			items = append(items, item)
		}
	}
	if total != maximum {
		return nil, fmt.Errorf("Bilibili SDK decompressed size %d, want %d", total, maximum)
	}
	return items, nil
}

func stageExistingPlanFile(gameRoot, relative, staged, reason string) (resources.PlanItem, bool, error) {
	file, err := os.Open(staged)
	if err != nil {
		return resources.PlanItem{}, false, err
	}
	digest := sha256.New()
	_, hashErr := io.Copy(digest, file)
	closeErr := file.Close()
	if hashErr != nil || closeErr != nil {
		return resources.PlanItem{}, false, errors.Join(hashErr, closeErr)
	}
	info, err := os.Stat(staged)
	if err != nil {
		return resources.PlanItem{}, false, err
	}
	manifestFile := resources.ManifestFile{Path: relative, Size: info.Size(), Hash: resources.Hash{Algorithm: "sha256", Digest: hex.EncodeToString(digest.Sum(nil))}}
	target := filepath.Join(gameRoot, relative)
	if err := resources.VerifyFile(target, manifestFile.Size, manifestFile.Hash); err == nil {
		return resources.PlanItem{}, false, nil
	}
	action := resources.ActionRepair
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		action = resources.ActionInstall
	}
	return resources.PlanItem{File: manifestFile, Action: action, Reason: reason}, true, nil
}

func stagePlanFile(gameRoot, stagingRoot, relative string, data []byte, reason string) (resources.PlanItem, bool, error) {
	digest := sha256.Sum256(data)
	file := resources.ManifestFile{Path: relative, Size: int64(len(data)), Hash: resources.Hash{Algorithm: "sha256", Digest: hex.EncodeToString(digest[:])}}
	destination := filepath.Join(stagingRoot, relative)
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return resources.PlanItem{}, false, err
	}
	if err := os.WriteFile(destination, data, 0o644); err != nil {
		return resources.PlanItem{}, false, err
	}
	target := filepath.Join(gameRoot, relative)
	if err := resources.VerifyFile(target, file.Size, file.Hash); err == nil {
		return resources.PlanItem{}, false, nil
	}
	action := resources.ActionRepair
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		action = resources.ActionInstall
	}
	return resources.PlanItem{File: file, Action: action, Reason: reason}, true, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func updateServerConfig(path string, target QuickServer) ([]byte, error) {
	values := map[string]string{"channel": "1", "sub_channel": "1", "cps": "mihoyo"}
	if target == QuickBilibili {
		values = map[string]string{"channel": "14", "sub_channel": "0", "cps": "bilibili"}
	}
	return updateServerConfigValues(path, values)
}

func updateServerConfigValues(path string, values map[string]string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, errors.New("config.ini exceeds safety limit")
	}
	text := strings.ReplaceAll(strings.TrimPrefix(string(data), "\ufeff"), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	inGeneral := false
	foundGeneral := false
	found := make(map[string]bool)
	insertAt := len(lines)
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inGeneral {
				insertAt = index
			}
			inGeneral = strings.EqualFold(trimmed, "[General]")
			foundGeneral = foundGeneral || inGeneral
			continue
		}
		if !inGeneral {
			continue
		}
		key, _, ok := strings.Cut(trimmed, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if replacement, exists := values[key]; ok && exists {
			lines[index] = key + "=" + replacement
			found[key] = true
		}
	}
	var additions []string
	if !foundGeneral {
		additions = append(additions, "[General]")
		insertAt = len(lines)
	}
	orderedKeys := []string{"channel", "sub_channel", "cps"}
	if _, exists := values["game_version"]; exists {
		orderedKeys = append(orderedKeys, "game_version")
	}
	for _, key := range orderedKeys {
		if !found[key] {
			additions = append(additions, key+"="+values[key])
		}
	}
	lines = append(lines, make([]string, len(additions))...)
	copy(lines[insertAt+len(additions):], lines[insertAt:])
	copy(lines[insertAt:], additions)
	return []byte(strings.Join(lines, "\r\n")), nil
}
