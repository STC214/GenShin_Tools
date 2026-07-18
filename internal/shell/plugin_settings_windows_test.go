package shell

import (
	"os"
	"path/filepath"
	"testing"

	"genshintools/internal/config"
	"genshintools/internal/localization"
	"genshintools/internal/paths"
	"genshintools/internal/plugins"
)

func TestCommitPluginSettingsCommitsAfterSave(t *testing.T) {
	app := application{
		settings: config.Default(),
		layout:   paths.Layout{Config: filepath.Join(t.TempDir(), "config.json")},
		texts:    localization.New(localization.EN, ""),
	}
	next := app.settings.Plugins
	next.SafeMode = false
	if !app.commitPluginSettings(next) {
		t.Fatalf("commit failed: %s", app.pluginStatus)
	}
	if app.settings.Plugins.SafeMode {
		t.Fatal("saved plugin setting was not committed in memory")
	}
}

func TestApplyPluginStoreConfigCommitsSettingsAndPageTogether(t *testing.T) {
	app := application{
		settings: config.Default(),
		layout:   paths.Layout{Config: filepath.Join(t.TempDir(), "config.json")},
		texts:    localization.New(localization.EN, ""),
		pluginCatalog: plugins.Catalog{
			SchemaVersion: 1,
			Plugins:       []plugins.CatalogItem{{ID: "utility", Category: "utility"}},
		},
	}
	next := app.settings.Plugins
	next.Category = "utility"
	if !app.applyPluginStoreConfig(next) {
		t.Fatalf("apply failed: %s", app.pluginStatus)
	}
	if app.settings.Plugins.Category != "utility" || app.pluginCatalogPage.Total != 1 {
		t.Fatalf("settings and page were not committed together: settings=%+v page=%+v", app.settings.Plugins, app.pluginCatalogPage)
	}
}

func TestApplyPluginStoreConfigRestoresPageOnSaveFailure(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "config-as-directory")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	originalPage := plugins.CatalogPage{Page: 3, Total: 99, TotalPages: 5}
	app := application{
		settings:          config.Default(),
		layout:            paths.Layout{Config: destination},
		texts:             localization.New(localization.EN, ""),
		pluginCatalog:     plugins.Catalog{SchemaVersion: 1, Plugins: []plugins.CatalogItem{{ID: "utility", Category: "utility"}}},
		pluginCatalogPage: originalPage,
	}
	next := app.settings.Plugins
	next.Category = "utility"
	if app.applyPluginStoreConfig(next) {
		t.Fatal("apply unexpectedly succeeded")
	}
	if app.settings.Plugins.Category != "" {
		t.Fatal("failed store settings save leaked into memory")
	}
	if app.pluginCatalogPage.Page != originalPage.Page || app.pluginCatalogPage.Total != originalPage.Total || app.pluginCatalogPage.TotalPages != originalPage.TotalPages || len(app.pluginCatalogPage.Items) != 0 {
		t.Fatalf("failed store settings save changed the visible page: got %+v want %+v", app.pluginCatalogPage, originalPage)
	}
}

func TestApplyPluginStoreConfigDoesNotChangeFiltersWhileTaskIsBusy(t *testing.T) {
	app := application{
		settings:   config.Default(),
		layout:     paths.Layout{Config: filepath.Join(t.TempDir(), "config.json")},
		texts:      localization.New(localization.EN, ""),
		pluginBusy: true,
	}
	next := app.settings.Plugins
	next.Category = "visuals"
	if app.applyPluginStoreConfig(next) {
		t.Fatal("busy store configuration unexpectedly succeeded")
	}
	if app.settings.Plugins.Category != "" {
		t.Fatal("busy store configuration changed filters")
	}
}

func TestCommitPluginSettingsRestoresMemoryOnSaveFailure(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "config-as-directory")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	app := application{
		settings: config.Default(),
		layout:   paths.Layout{Config: destination},
		texts:    localization.New(localization.EN, ""),
	}
	next := app.settings.Plugins
	next.SafeMode = false
	if app.commitPluginSettings(next) {
		t.Fatal("commit unexpectedly succeeded")
	}
	if !app.settings.Plugins.SafeMode {
		t.Fatal("failed plugin save leaked into memory")
	}
}
