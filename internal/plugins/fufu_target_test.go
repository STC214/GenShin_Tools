package plugins

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"genshintools/internal/game"
)

func TestDownloadFufuMainPackageWritesAndHashesBoundedResponse(t *testing.T) {
	payload := []byte("PK fixture")
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/zip")
		_, _ = writer.Write(payload)
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "main.zip")
	hash, size, err := downloadFufuMainPackage(context.Background(), server.Client(), destination, server.URL, map[string]bool{parsed.Hostname(): true})
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(payload)) || hash != fufuBytesSHA256(payload) {
		t.Fatalf("unexpected download metadata: size=%d hash=%s", size, hash)
	}
}

func TestLiveFufuMainPackageDownloadAndInstall(t *testing.T) {
	if os.Getenv("GENSHINTOOLS_LIVE_FUFU_MAIN") != "1" {
		t.Skip("set GENSHINTOOLS_LIVE_FUFU_MAIN=1 to audit the current official Fufu main bundle")
	}
	root := t.TempDir()
	modules := filepath.Join(root, "modules")
	layout, err := NewLayout(filepath.Join(root, "data"), modules)
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(layout.Staging, "FuFuPlugin.zip")
	if _, _, err := DownloadFufuMainPackage(context.Background(), nil, packagePath); err != nil {
		t.Fatal(err)
	}
	gameRoot := filepath.Join(root, "game")
	if err := os.MkdirAll(gameRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	state := DefaultState()
	executable := filepath.Join(gameRoot, "YuanShen.exe")
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copyFileForPluginTest(t, testExecutable, executable)
	result, err := InstallFufuMainPackage(context.Background(), packagePath, layout, game.Candidate{Root: gameRoot, Executable: executable, ExeName: "YuanShen.exe", Version: "live-audit"}, &state)
	if err != nil {
		t.Fatal(err)
	}
	target, err := LoadFufuTargetConfig(filepath.Join(modules, FufuMainTargetID, "config.ini"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.ID != FufuMainTargetID || target.DLL != FufuMainDLL || len(target.Settings) == 0 {
		t.Fatalf("unexpected live Fufu target: manifest=%+v target=%+v", result.Manifest, target)
	}
}

func TestLoadFufuTargetConfigAndUpdatePreservesUnknownData(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.ini")
	input := "; upstream comment\r\n[General]\r\nName = FufuLauncher-Plugin\r\nDescription = build\r\nDeveloper = ME46231\r\nFile = FufuLauncher.UnlockerIsland.dll\r\nVersion = 1.4.0\r\n\r\n[FpsUnlock]\r\nName = FPS Unlock\r\nType = bool\r\nValue = 1\r\nhelp = Assets/help.gif\r\nUnknown = keep-me\r\n"
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	target, err := LoadFufuTargetConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if target.Name != "FufuLauncher-Plugin" || target.DLL != FufuMainDLL || len(target.Settings) != 1 || target.Settings[0].Help != "Assets/help.gif" {
		t.Fatalf("unexpected target: %+v", target)
	}
	if err := UpdateConfig(path, target.Schema, "fufu.fpsunlock", "0"); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updated)
	if !strings.Contains(text, "; upstream comment") || !strings.Contains(text, "Unknown = keep-me") || !strings.Contains(text, "Value = 0") {
		t.Fatalf("upstream content was not preserved:\n%s", text)
	}
}

func TestSetFufuTargetEnabledUsesDisabledSuffix(t *testing.T) {
	directory := t.TempDir()
	dll := filepath.Join(directory, FufuMainDLL)
	if err := os.WriteFile(dll, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetFufuTargetEnabled(directory, FufuMainDLL, false); err != nil {
		t.Fatal(err)
	}
	enabled, installed, err := FufuTargetEnabled(directory, FufuMainDLL)
	if err != nil || enabled || !installed {
		t.Fatalf("unexpected disabled state: enabled=%v installed=%v err=%v", enabled, installed, err)
	}
	if err := SetFufuTargetEnabled(directory, FufuMainDLL, true); err != nil {
		t.Fatal(err)
	}
	enabled, installed, err = FufuTargetEnabled(directory, FufuMainDLL)
	if err != nil || !enabled || !installed {
		t.Fatalf("unexpected enabled state: enabled=%v installed=%v err=%v", enabled, installed, err)
	}
}

func TestPreserveFufuTargetValuesKeepsExistingAndLeavesNewDefaults(t *testing.T) {
	root := t.TempDir()
	active := filepath.Join(root, "active")
	candidate := filepath.Join(root, "candidate")
	if err := os.MkdirAll(active, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	general := "[General]\r\nName=FuFuPlugin\r\nDeveloper=Tests\r\nFile=FufuLauncher.UnlockerIsland.dll\r\nVersion=1.0.0\r\n"
	oldConfig := general + "[Existing]\r\nName=Existing\r\nType=int\r\nValue=144\r\n"
	newConfig := general + "[Existing]\r\nName=Existing\r\nType=int\r\nValue=60\r\n[Added]\r\nName=Added\r\nType=bool\r\nValue=1\r\n"
	if err := os.WriteFile(filepath.Join(active, "config.ini"), []byte(oldConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidate, "config.ini"), []byte(newConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := preserveFufuTargetValues(active, candidate); err != nil {
		t.Fatal(err)
	}
	target, err := LoadFufuTargetConfig(filepath.Join(candidate, "config.ini"))
	if err != nil {
		t.Fatal(err)
	}
	values, err := ReadConfig(filepath.Join(candidate, "config.ini"), target.Schema)
	if err != nil {
		t.Fatal(err)
	}
	if values["fufu.existing"] != "144" || values["fufu.added"] != "1" {
		t.Fatalf("unexpected preserved values: %+v", values)
	}
}

func TestFufuTargetRejectsReparseDirectory(t *testing.T) {
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.MkdirAll(realDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	config := "[General]\r\nName=FuFuPlugin\r\nDeveloper=Tests\r\nFile=FufuLauncher.UnlockerIsland.dll\r\nVersion=1.0.0\r\n[Enabled]\r\nName=Enabled\r\nType=bool\r\nValue=1\r\n"
	if err := os.WriteFile(filepath.Join(realDirectory, "config.ini"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDirectory, FufuMainDLL), []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(realDirectory, link); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}
	if _, err := LoadFufuTargetConfig(filepath.Join(link, "config.ini")); err == nil {
		t.Fatal("Fufu config under a reparse directory was accepted")
	}
	if err := SetFufuTargetEnabled(link, FufuMainDLL, false); err == nil {
		t.Fatal("Fufu DLL under a reparse directory was renamed")
	}
}
