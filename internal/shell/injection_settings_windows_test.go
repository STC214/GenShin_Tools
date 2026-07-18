package shell

import (
	"os"
	"path/filepath"
	"testing"

	"genshintools/internal/config"
	"genshintools/internal/localization"
	"genshintools/internal/paths"
)

func TestCommitInjectionSettingsCommitsAfterSave(t *testing.T) {
	app := application{
		settings: config.Default(),
		layout:   paths.Layout{Config: filepath.Join(t.TempDir(), "config.json")},
		texts:    localization.New(localization.EN, ""),
	}
	next := app.settings.Injection
	next.Enabled = true
	if !app.commitInjectionSettings(next) {
		t.Fatalf("commit failed: %s", app.injectionStatus)
	}
	if !app.settings.Injection.Enabled {
		t.Fatal("saved injection setting was not committed in memory")
	}
}

func TestCommitInjectionSettingsRestoresMemoryOnSaveFailure(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "config-as-directory")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	app := application{
		settings: config.Default(),
		layout:   paths.Layout{Config: destination},
		texts:    localization.New(localization.EN, ""),
	}
	next := app.settings.Injection
	next.RiskAcknowledged = true
	if app.commitInjectionSettings(next) {
		t.Fatal("commit unexpectedly succeeded")
	}
	if app.settings.Injection.RiskAcknowledged {
		t.Fatal("failed injection save leaked into memory")
	}
}
