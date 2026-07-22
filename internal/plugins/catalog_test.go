package plugins

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFufuCatalogSyncMapsAPIAndQueries(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != fufuStoreListPath || request.URL.Query().Get("page_size") != "100" || request.URL.Query().Get("lang") != "zh-CN" {
			t.Errorf("unexpected request %s", request.URL.String())
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(fufuStoreResponse{Retcode: 0, Message: "OK", Data: fufuStoreData{Total: 2, Page: 1, Plugins: []fufuPlugin{
			validFufuPlugin(server.URL, "FSR-Bridge-Plugin", "visuals", 100),
			validFufuPlugin(server.URL, "other", "utility", 1),
		}}})
	}))
	defer server.Close()
	cache := filepath.Join(t.TempDir(), "catalog.json")
	synced, err := syncCatalog(t.Context(), server.Client(), server.URL, cache)
	if err != nil {
		t.Fatal(err)
	}
	page, err := QueryCatalog(synced, Config{SafeMode: true, Category: "visuals", Search: "fixture", Sort: "popular", Page: 1, PageSize: 20})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "FSR-Bridge-Plugin" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if page.Items[0].LuaInstallURL != server.URL+"/scripts/FSR-Bridge-Plugin-install.lua" || page.Items[0].SourceURL != FufuStoreRepository {
		t.Fatalf("mapped item = %+v", page.Items[0])
	}
	if info, err := os.Stat(cache); err != nil || info.Size() == 0 {
		t.Fatalf("cache not written: %v", err)
	}
}

func TestLoadCatalogOnlyAcceptsFufuSnapshot(t *testing.T) {
	item := mapFufuPlugin(validFufuPlugin(FufuStoreBaseURL, "fixture", "visuals", 100), FufuStoreBaseURL)
	catalog := Catalog{SchemaVersion: 1, ID: "fufu-launcher", Name: "FufuLauncher Plugin Store", SourceURL: FufuStoreBaseURL + fufuStoreListPath, License: "upstream", GeneratedUTC: time.Now().UTC().Format(time.RFC3339), Plugins: []CatalogItem{item}}
	data, _ := json.Marshal(catalog)
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCatalog(path); err != nil {
		t.Fatal(err)
	}
	catalog.SourceURL = "https://different.invalid/api/v1/plugins/list"
	data, _ = json.Marshal(catalog)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCatalog(path); err == nil {
		t.Fatal("non-Fufu cache was accepted")
	}
}

func TestMapFufuPluginUsesPublishedPluginSourceWhenPresent(t *testing.T) {
	upstream := validFufuPlugin(FufuStoreBaseURL, "Drop", "utility", 1)
	upstream.LongDescription = "链接:https://github.com/ChengChe-yi/Drop/"
	item := mapFufuPlugin(upstream, FufuStoreBaseURL)
	if item.SourceURL != "https://github.com/ChengChe-yi/Drop/" || item.License != "UNSPECIFIED-FUFU-STORE" {
		t.Fatalf("mapped attribution = %+v", item)
	}
}

func TestFufuCatalogRejectsMissingHashAndPreservesCache(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "catalog.json")
	original := []byte("trusted-cache")
	if err := os.WriteFile(cache, original, 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		item := validFufuPlugin("https://example.invalid", "fixture", "visuals", 1)
		item.FileHash = ""
		_ = json.NewEncoder(response).Encode(fufuStoreResponse{Retcode: 0, Data: fufuStoreData{Total: 1, Page: 1, Plugins: []fufuPlugin{item}}})
	}))
	defer server.Close()
	if _, err := syncCatalog(t.Context(), server.Client(), server.URL, cache); err == nil {
		t.Fatal("missing hash was accepted")
	}
	got, _ := os.ReadFile(cache)
	if string(got) != string(original) {
		t.Fatal("invalid response replaced cache")
	}
}

func TestFufuCatalogLiveIntegration(t *testing.T) {
	if os.Getenv("GENSHINTOOLS_LIVE_FUFU_STORE") != "1" {
		t.Skip("set GENSHINTOOLS_LIVE_FUFU_STORE=1 to query the official Fufu store")
	}
	catalog, err := SyncCatalog(t.Context(), nil, filepath.Join(t.TempDir(), "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ID != "fufu-launcher" || len(catalog.Plugins) == 0 {
		t.Fatalf("unexpected live catalog: id=%q plugins=%d", catalog.ID, len(catalog.Plugins))
	}
}

func validFufuPlugin(origin, id, category string, downloads int64) fufuPlugin {
	return fufuPlugin{ID: id, Name: strings.ToUpper(id), Developer: "Tests", Description: "Fixture plugin", Version: "1.0.0", Category: category, Tags: []string{"fixture"}, Downloads: downloads, SizeBytes: 123, UpdatedAt: time.Now().UTC().Format(time.RFC3339), LuaInstallURL: origin + "/scripts/" + id + "-install.lua", LuaUninstallURL: origin + "/scripts/" + id + "-uninstall.lua", DownloadURL: origin + "/plugins/files/" + id + ".zip", FileHash: strings.Repeat("a", 64), LuaHash: strings.Repeat("b", 64), DLLFileName: id + ".dll", Visibility: "public", UpdateType: "full"}
}
