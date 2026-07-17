package localenhance

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"genshintools/internal/resources"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestBilibiliSDKPlanAndTransaction(t *testing.T) {
	zipBytes, uncompressed := sdkFixtureZip(t)
	digest := md5.Sum(zipBytes)
	api := fmt.Sprintf(`{"retcode":0,"message":"OK","data":{"game_channel_sdks":[{"game":{"id":"T2S0Gz4Dr2","biz":"hk4e_cn"},"version":"5.0.4","channel_sdk_pkg":{"url":"https://fixture.invalid/sdk.zip","md5":"%s","size":"%d","decompressed_size":"%d"},"pkg_version_file_name":"sdk_pkg_version"}]}}`, hex.EncodeToString(digest[:]), len(zipBytes), uncompressed)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := []byte(api)
		if request.URL.Host == "fixture.invalid" {
			body = zipBytes
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
	})}
	gameRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(gameRoot, "YuanShen.exe"), []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameRoot, "config.ini"), []byte("[General]\r\ngame_version=6.7.0\r\nchannel=1\r\nsub_channel=1\r\ncps=mihoyo\r\n[Keep]\r\nvalue=yes\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tx, err := resources.NewTransaction(t.TempDir(), gameRoot, "bili-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Prepare(); err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareQuickServerSwitch(context.Background(), client, gameRoot, tx.StagingRoot, QuickBilibili)
	if err != nil {
		t.Fatal(err)
	}
	if plan.DownloadBytes != int64(len(zipBytes)) || len(plan.Items) != 3 {
		t.Fatalf("plan = %+v", plan)
	}
	if err := tx.Commit(plan); err != nil {
		t.Fatal(err)
	}
	config, _ := os.ReadFile(filepath.Join(gameRoot, "config.ini"))
	if !strings.Contains(string(config), "channel=14") || !strings.Contains(string(config), "game_version=6.7.0") || !strings.Contains(string(config), "value=yes") {
		t.Fatalf("config was not safely updated: %q", config)
	}
	for path, want := range map[string]string{
		`YuanShen_Data\Plugins\PCGameSDK.dll`: "sdk-dll",
		"sdk_pkg_version":                     "5.0.4",
	} {
		got, err := os.ReadFile(filepath.Join(gameRoot, path))
		if err != nil || string(got) != want {
			t.Fatalf("installed %s = %q, %v", path, got, err)
		}
	}
}

func TestAdvancedConversionPlanReusesRenamedLayout(t *testing.T) {
	gameRoot := t.TempDir()
	stagingBase := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gameRoot, "YuanShen_Data"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		"YuanShen.exe":                      "same-exe",
		`YuanShen_Data\keep.bin`:            "same-data",
		`YuanShen_Data\region-specific.bin`: "mainland-data",
		"config.ini":                        "[General]\r\ngame_version=6.6.0\r\nchannel=1\r\nsub_channel=1\r\ncps=mihoyo\r\n[Keep]\r\nvalue=yes\r\n",
	} {
		full := filepath.Join(gameRoot, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	targetRegionData := []byte("global-data")
	manifest := resources.Manifest{SchemaVersion: resources.ManifestSchemaVersion, Version: "6.7.0", Kind: "sophon", Files: []resources.ManifestFile{
		testMD5ManifestFile("GenshinImpact.exe", []byte("same-exe")),
		testMD5ManifestFile(`GenshinImpact_Data\keep.bin`, []byte("same-data")),
		testMD5ManifestFile(`GenshinImpact_Data\region-specific.bin`, targetRegionData),
	}}
	for index := range manifest.Files {
		manifest.Files[index].URL = "https://fixture.invalid/" + fmt.Sprint(index)
	}
	tx, err := resources.NewTransaction(stagingBase, gameRoot, "advanced-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Prepare(); err != nil {
		t.Fatal(err)
	}
	plan, err := buildAdvancedPlan(context.Background(), gameRoot, tx.StagingRoot, manifest, mainlandLayout, globalLayout)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) < 6 || plan.Items[0].Action != resources.ActionMove || plan.Items[1].Action != resources.ActionMove {
		t.Fatalf("advanced plan does not begin with reversible moves: %+v", plan.Items)
	}
	if plan.Items[2].Action != resources.ActionKeep || plan.Items[3].Action != resources.ActionKeep || plan.Items[4].Action != resources.ActionRepair {
		t.Fatalf("mapped actions = %s, %s, %s", plan.Items[2].Action, plan.Items[3].Action, plan.Items[4].Action)
	}
	stagedRegion := filepath.Join(tx.StagingRoot, `GenshinImpact_Data\region-specific.bin`)
	if err := os.MkdirAll(filepath.Dir(stagedRegion), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagedRegion, targetRegionData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(plan); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(gameRoot, `GenshinImpact_Data\region-specific.bin`))
	if err != nil || string(got) != string(targetRegionData) {
		t.Fatalf("converted region data = %q, %v", got, err)
	}
	config, err := os.ReadFile(filepath.Join(gameRoot, "config.ini"))
	if err != nil || !strings.Contains(string(config), "sub_channel=0") || !strings.Contains(string(config), "game_version=6.7.0") || !strings.Contains(string(config), "value=yes") {
		t.Fatalf("converted config = %q, %v", config, err)
	}
}

func testMD5ManifestFile(path string, data []byte) resources.ManifestFile {
	digest := md5.Sum(data)
	return resources.ManifestFile{Path: path, Size: int64(len(data)), Hash: resources.Hash{Algorithm: "md5", Digest: hex.EncodeToString(digest[:])}}
}

func sdkFixtureZip(t *testing.T) ([]byte, int64) {
	t.Helper()
	files := map[string]string{"YuanShen_Data/Plugins/PCGameSDK.dll": "sdk-dll", "sdk_pkg_version": "5.0.4"}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	var total int64
	for name, data := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
		total += int64(len(data))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes(), total
}
