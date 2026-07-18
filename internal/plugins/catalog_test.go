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

func TestCatalogSyncQueryAndCache(t *testing.T) {
	var payload []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(payload)
	}))
	defer server.Close()
	item := validCatalogItem(server.URL)
	second := item
	second.ID, second.Name, second.Category, second.Downloads = "other", "Other", "utility", 1
	catalog := Catalog{SchemaVersion: 1, ID: "fixture", Name: "Fixture", SourceURL: server.URL + "/catalog.json", License: "Test", GeneratedUTC: time.Now().UTC().Format(time.RFC3339), Plugins: []CatalogItem{second, item}}
	payload, _ = json.Marshal(catalog)
	cache := filepath.Join(t.TempDir(), "catalog.json")
	synced, err := SyncCatalog(t.Context(), server.Client(), server.URL+"/catalog.json", cache)
	if err != nil {
		t.Fatal(err)
	}
	page, err := QueryCatalog(synced, Config{SafeMode: true, Category: "visuals", Search: "fixture", Sort: "popular", Page: 1, PageSize: 20})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "fixture" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	loaded, err := LoadCatalog(cache)
	if err != nil || len(loaded.Plugins) != 2 {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	if _, err := LoadCatalogForSource(cache, server.URL+"/configured.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCatalogForSource(cache, "https://different.invalid/catalog.json"); err == nil {
		t.Fatal("cached catalog from another origin was accepted")
	}
}

func TestCatalogRejectsExcludedScopeAndPreservesCache(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "catalog.json")
	original := []byte("trusted-cache")
	if err := os.WriteFile(cache, original, 0o644); err != nil {
		t.Fatal(err)
	}
	item := validCatalogItem("https://example.invalid")
	item.Capabilities = []string{"account.login"}
	catalog := Catalog{SchemaVersion: 1, ID: "fixture", Name: "Fixture", SourceURL: "https://example.invalid/catalog.json", License: "Test", GeneratedUTC: time.Now().UTC().Format(time.RFC3339), Plugins: []CatalogItem{item}}
	data, _ := json.Marshal(catalog)
	if _, err := decodeCatalog(data); err == nil {
		t.Fatal("excluded catalog capability accepted")
	}
	got, _ := os.ReadFile(cache)
	if string(got) != string(original) {
		t.Fatal("invalid catalog replaced cache")
	}
}

func validCatalogItem(origin string) CatalogItem {
	return CatalogItem{ID: "fixture", Name: "Fixture", Developer: "Tests", Description: "Fixture plugin", Version: "1.0.0", Category: "visuals", Tags: []string{"fixture"}, Capabilities: []string{"visual"}, SourceURL: origin + "/source", License: "Test", PackageURL: origin + "/fixture.zip", PackageSize: 123, PackageSHA256: strings.Repeat("a", 64), Rating: 4.5, Downloads: 100, UpdatedUTC: time.Now().UTC().Format(time.RFC3339)}
}
