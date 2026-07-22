package plugins

import (
	"archive/zip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseFufuDownloadTokenAcceptsTokenAndGateJSON(t *testing.T) {
	const token = "abc.DEF_123-token"
	for _, input := range []string{token, `{"retcode":0,"message":"success","data":{"dl_token":"` + token + `"}}`} {
		got, err := ParseFufuDownloadToken(input)
		if err != nil || got != token {
			t.Fatalf("token=%q err=%v", got, err)
		}
	}
	for _, input := range []string{"short", "has space token", `{"retcode":403,"data":{"dl_token":"abc.DEF_123-token"}}`, `{"retcode":0,"data":{}} trailing`} {
		if _, err := ParseFufuDownloadToken(input); err == nil {
			t.Fatalf("invalid token input accepted: %q", input)
		}
	}
}

func TestDownloadFufuPackageUsesTokenAndVerifiesBytes(t *testing.T) {
	payload := []byte("PK fixture bytes")
	const token = "abc.DEF_123-token"
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("dl_token") != token {
			t.Errorf("download token was not sent")
		}
		response.Header().Set("Content-Type", "application/zip")
		_, _ = response.Write(payload)
	}))
	defer server.Close()
	item := CatalogItem{ID: "Drop", Name: "Drop", Developer: "Tests", Description: "Fixture", Version: "1.0.3", Category: "utility", Capabilities: []string{"game.plugin"}, SourceURL: FufuStoreRepository, License: "terms", PackageURL: server.URL + "/plugins/files/Drop.zip", PackageSize: int64(len(payload)), PackageSHA256: fufuBytesSHA256(payload), UpdatedUTC: time.Now().UTC().Format(time.RFC3339)}
	destination := filepath.Join(t.TempDir(), "Drop.zip")
	if err := downloadFufuPackage(t.Context(), server.Client(), item, token, destination, server.URL); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("download=%q err=%v", got, err)
	}
}

func TestDownloadFufuPackageRefusesCrossOriginTokenRedirect(t *testing.T) {
	var attackerCalled atomic.Bool
	attacker := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		attackerCalled.Store(true)
		_, _ = response.Write([]byte("stolen"))
	}))
	defer attacker.Close()
	trusted := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, attacker.URL+"/steal", http.StatusFound)
	}))
	defer trusted.Close()
	payload := []byte("unused")
	item := CatalogItem{ID: "Drop", Name: "Drop", Developer: "Tests", Description: "Fixture", Version: "1.0.3", Category: "utility", Capabilities: []string{"game.plugin"}, SourceURL: FufuStoreRepository, License: "UNSPECIFIED-FUFU-STORE", PackageURL: trusted.URL + "/plugins/files/Drop.zip", PackageSize: int64(len(payload)), PackageSHA256: fufuBytesSHA256(payload), UpdatedUTC: time.Now().UTC().Format(time.RFC3339)}
	err := downloadFufuPackage(t.Context(), trusted.Client(), item, "abc.DEF_123-token", filepath.Join(t.TempDir(), "Drop.zip"), trusted.URL)
	if err == nil || attackerCalled.Load() {
		t.Fatalf("cross-origin redirect err=%v attackerCalled=%v", err, attackerCalled.Load())
	}
}

func TestInstallFufuPackageAdaptsConfigAndKeepsTransactionalState(t *testing.T) {
	fixture := newPackageFixture(t)
	packagePath, item := fixture.fufuPackageFile(t, "Drop", "1.0.3", "Drop.dll", "Drop/")
	state := DefaultState()
	result, err := InstallFufuPackage(t.Context(), packagePath, item, fixture.layout, fixture.candidate, &state)
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.ID != "drop" || state.Installed["drop"].ActiveVersion != "1.0.3" {
		t.Fatalf("result=%+v state=%+v", result, state)
	}
	active := filepath.Join(fixture.layout.Modules, "drop")
	for _, name := range []string{"Drop.dll", "config.ini", "module.json", "plugin.json"} {
		if _, err := os.Stat(filepath.Join(active, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	items, warnings, err := Discover(fixture.layout.Modules, state)
	if err != nil || len(warnings) != 0 || len(items) != 1 || items[0].Manifest.ID != "drop" {
		t.Fatalf("items=%+v warnings=%v err=%v", items, warnings, err)
	}
}

func TestInstallFufuPackageRejectsZipSlipBeforeActivation(t *testing.T) {
	fixture := newPackageFixture(t)
	_, item := fixture.fufuPackageFile(t, "Drop", "1.0.3", "Drop.dll", "")
	// Rebuild with an escaping entry and refresh the official package hash.
	packagePath := filepath.Join(fixture.root, "fufu-unsafe.zip")
	archiveFile, err := os.Create(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(archiveFile)
	for name, data := range map[string][]byte{"config.ini": []byte("[General]\nFile=Drop.dll\n"), "Drop.dll": fixture.dll, "../escape.txt": []byte("escape")} {
		entry, _ := archive.Create(name)
		_, _ = entry.Write(data)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(packagePath)
	hash, _ := fileSHA256(packagePath)
	item.PackageSize, item.PackageSHA256 = info.Size(), hash
	state := DefaultState()
	if _, err := InstallFufuPackage(t.Context(), packagePath, item, fixture.layout, fixture.candidate, &state); err == nil {
		t.Fatal("zip-slip Fufu package was accepted")
	}
	if _, err := os.Stat(filepath.Join(fixture.layout.Modules, "drop")); !os.IsNotExist(err) {
		t.Fatalf("unsafe package activated: %v", err)
	}
}

func TestInstallFufuPackageRejectsMismatchedDLLAndUnresolvedDependency(t *testing.T) {
	fixture := newPackageFixture(t)
	packagePath, item := fixture.fufuPackageFile(t, "Drop", "1.0.3", "Drop.dll", "")
	state := DefaultState()
	item.DLLFileName = "Other.dll"
	if _, err := InstallFufuPackage(t.Context(), packagePath, item, fixture.layout, fixture.candidate, &state); err == nil {
		t.Fatal("mismatched store DLL was accepted")
	}
	item.DLLFileName = "Drop.dll"
	item.Dependencies = []FufuDependency{{PluginName: "RequiredPlugin", ProjectVersion: "1.0.0"}}
	if _, err := InstallFufuPackage(t.Context(), packagePath, item, fixture.layout, fixture.candidate, &state); err == nil {
		t.Fatal("unresolved Fufu dependency was accepted")
	}
}

func (fixture packageFixture) fufuPackageFile(t *testing.T, id, version, dllName, prefix string) (string, CatalogItem) {
	t.Helper()
	packagePath := filepath.Join(fixture.root, "fufu-"+strings.ToLower(id)+".zip")
	file, err := os.Create(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	if prefix != "" {
		if _, err := archive.Create(prefix); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string][]byte{prefix + "config.ini": []byte("[General]\nFile = " + dllName + "\nName = Fixture\n"), prefix + dllName: fixture.dll} {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(packagePath)
	hash, _ := fileSHA256(packagePath)
	item := CatalogItem{ID: id, Name: "Drop Fixture", Developer: "Tests", Description: "Fufu package fixture", Version: version, Category: "utility", Tags: []string{"fixture"}, Capabilities: []string{"game.plugin"}, SourceURL: FufuStoreRepository, License: "UNSPECIFIED-FUFU-STORE", PackageURL: FufuStoreBaseURL + "/plugins/files/" + id + ".zip", PackageSize: info.Size(), PackageSHA256: hash, LuaSHA256: strings.Repeat("b", 64), DLLFileName: dllName, Downloads: 1, UpdatedUTC: time.Now().UTC().Format(time.RFC3339)}
	return packagePath, item
}
