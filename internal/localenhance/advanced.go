package localenhance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"genshintools/internal/resources"
)

type AdvancedServer uint8

const (
	AdvancedMainland AdvancedServer = iota + 1
	AdvancedGlobal
)

func (server AdvancedServer) String() string {
	if server == AdvancedGlobal {
		return "国际服"
	}
	return "国服官服"
}

type advancedLayout struct {
	Server  AdvancedServer
	EXE     string
	DataDir string
	Region  resources.SophonRegion
}

var (
	mainlandLayout = advancedLayout{Server: AdvancedMainland, EXE: "YuanShen.exe", DataDir: "YuanShen_Data", Region: resources.SophonMainland}
	globalLayout   = advancedLayout{Server: AdvancedGlobal, EXE: "GenshinImpact.exe", DataDir: "GenshinImpact_Data", Region: resources.SophonGlobal}
)

type AdvancedConversion struct {
	SourceVersion string
	TargetVersion string
	Source        AdvancedServer
	Target        AdvancedServer
	Plan          resources.RepairPlan
}

func PrepareAdvancedServerConversion(ctx context.Context, client *http.Client, gameRoot, stagingRoot string, target AdvancedServer) (AdvancedConversion, error) {
	source, targetLayout, err := advancedLayouts(gameRoot, target)
	if err != nil {
		return AdvancedConversion{}, err
	}
	provider := resources.NewSophonProviderForRegion(targetLayout.Region)
	if client != nil {
		provider.Client = client
	}
	branches, err := provider.FetchBranches(ctx)
	if err != nil {
		return AdvancedConversion{}, err
	}
	provider.BuildURL, err = provider.BuildURLFor(branches.Current)
	if err != nil {
		return AdvancedConversion{}, err
	}
	catalog, err := provider.FetchCatalog(ctx)
	if err != nil {
		return AdvancedConversion{}, err
	}
	manifest, err := provider.LoadManifest(ctx, catalog, "game")
	if err != nil {
		return AdvancedConversion{}, err
	}
	plan, err := buildAdvancedPlan(ctx, gameRoot, stagingRoot, manifest, source, targetLayout)
	if err != nil {
		return AdvancedConversion{}, err
	}
	var changes []resources.ManifestFile
	for _, item := range plan.Items {
		if (item.Action == resources.ActionInstall || item.Action == resources.ActionRepair) && (item.File.URL != "" || len(item.File.Chunks) > 0) {
			changes = append(changes, item.File)
		}
	}
	if len(changes) > 0 {
		downloadManifest := resources.Manifest{SchemaVersion: resources.ManifestSchemaVersion, Version: manifest.Version, Kind: "advanced-server-conversion", Files: changes}
		downloader := resources.NewDownloader()
		if client != nil {
			downloader.Client = client
		}
		if err := downloader.Download(ctx, downloadManifest, stagingRoot); err != nil {
			return AdvancedConversion{}, fmt.Errorf("download target server resources: %w", err)
		}
	}
	return AdvancedConversion{SourceVersion: readGameVersion(filepath.Join(gameRoot, "config.ini")), TargetVersion: manifest.Version, Source: source.Server, Target: target, Plan: plan}, nil
}

func advancedLayouts(gameRoot string, target AdvancedServer) (advancedLayout, advancedLayout, error) {
	if target != AdvancedMainland && target != AdvancedGlobal {
		return advancedLayout{}, advancedLayout{}, errors.New("unsupported advanced server target")
	}
	mainlandExists := pathExists(filepath.Join(gameRoot, mainlandLayout.EXE)) && pathExists(filepath.Join(gameRoot, mainlandLayout.DataDir))
	globalExists := pathExists(filepath.Join(gameRoot, globalLayout.EXE)) && pathExists(filepath.Join(gameRoot, globalLayout.DataDir))
	if mainlandExists == globalExists {
		return advancedLayout{}, advancedLayout{}, errors.New("game layout is ambiguous; exactly one complete mainland/global layout is required")
	}
	source := mainlandLayout
	if globalExists {
		source = globalLayout
	}
	targetLayout := mainlandLayout
	if target == AdvancedGlobal {
		targetLayout = globalLayout
	}
	if source.Server == targetLayout.Server {
		return advancedLayout{}, advancedLayout{}, fmt.Errorf("game is already %s", targetLayout.Server)
	}
	if pathExists(filepath.Join(gameRoot, targetLayout.EXE)) || pathExists(filepath.Join(gameRoot, targetLayout.DataDir)) {
		return advancedLayout{}, advancedLayout{}, errors.New("target server layout already exists; refusing to overwrite an ambiguous installation")
	}
	return source, targetLayout, nil
}

func buildAdvancedPlan(ctx context.Context, gameRoot, stagingRoot string, manifest resources.Manifest, source, target advancedLayout) (resources.RepairPlan, error) {
	if err := manifest.Validate(); err != nil {
		return resources.RepairPlan{}, err
	}
	plan := resources.RepairPlan{Items: []resources.PlanItem{
		{File: resources.ManifestFile{Path: target.DataDir}, SourcePath: source.DataDir, Action: resources.ActionMove, Reason: "rename game data directory transactionally"},
		{File: resources.ManifestFile{Path: target.EXE}, SourcePath: source.EXE, Action: resources.ActionMove, Reason: "rename game executable transactionally"},
	}}
	for _, file := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return resources.RepairPlan{}, err
		}
		mapped := mapAdvancedPath(file.Path, target, source)
		if err := resources.VerifyFileContext(ctx, filepath.Join(gameRoot, mapped), file.Size, file.Hash); err == nil {
			plan.Items = append(plan.Items, resources.PlanItem{File: file, Action: resources.ActionKeep, Reason: "verified source file will be reused after transactional rename"})
			continue
		}
		action := resources.ActionInstall
		if pathExists(filepath.Join(gameRoot, mapped)) {
			action = resources.ActionRepair
		}
		plan.Items = append(plan.Items, resources.PlanItem{File: file, Action: action, Reason: "target server content differs"})
		plan.DownloadBytes += file.Size
	}
	configData, err := updateAdvancedConfig(filepath.Join(gameRoot, "config.ini"), target.Server, manifest.Version)
	if err != nil {
		return resources.RepairPlan{}, err
	}
	configItem, changed, err := stagePlanFile(gameRoot, stagingRoot, "config.ini", configData, "advanced server configuration")
	if err != nil {
		return resources.RepairPlan{}, err
	}
	if changed {
		plan.Items = append(plan.Items, configItem)
	}
	for _, relative := range []string{
		filepath.Join(target.DataDir, "Plugins", "PCGameSDK.dll"),
		filepath.Join(target.DataDir, "Plugins", "EOSSDK-Win64-Shipping.dll"),
		"sdk_pkg_version",
	} {
		mapped := mapAdvancedPath(relative, target, source)
		if pathExists(filepath.Join(gameRoot, mapped)) {
			plan.Items = append(plan.Items, resources.PlanItem{File: resources.ManifestFile{Path: relative}, Action: resources.ActionDelete, Reason: "remove channel-specific SDK after conversion"})
		}
	}
	return plan, nil
}

func mapAdvancedPath(path string, target, source advancedLayout) string {
	clean := filepath.Clean(path)
	if strings.EqualFold(clean, target.EXE) {
		return source.EXE
	}
	prefix := target.DataDir + string(filepath.Separator)
	if strings.HasPrefix(strings.ToLower(clean), strings.ToLower(prefix)) {
		return filepath.Join(source.DataDir, clean[len(prefix):])
	}
	return clean
}

func updateAdvancedConfig(path string, target AdvancedServer, targetVersion string) ([]byte, error) {
	values := map[string]string{"channel": "1", "sub_channel": "1", "cps": "mihoyo", "game_version": targetVersion}
	if target == AdvancedGlobal {
		values["sub_channel"] = "0"
	}
	return updateServerConfigValues(path, values)
}

func readGameVersion(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "game_version") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
