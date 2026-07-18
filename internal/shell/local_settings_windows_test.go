package shell

import (
	"os"
	"path/filepath"
	"testing"

	"genshintools/internal/config"
	"genshintools/internal/localization"
	"genshintools/internal/paths"
)

func TestSaveLocalEnhanceCommitsOnlyAfterDiskSave(t *testing.T) {
	settings := config.Default()
	app := application{
		settings: settings,
		layout:   paths.Layout{Config: filepath.Join(t.TempDir(), "config.json")},
		texts:    localization.New(localization.EN, ""),
	}
	next := settings.LocalEnhance
	next.BetterGIEnabled = true
	if !app.saveLocalEnhance(next) {
		t.Fatalf("save failed: %s", app.localStatus)
	}
	if !app.settings.LocalEnhance.BetterGIEnabled {
		t.Fatal("successful disk save did not commit in-memory settings")
	}
}

func TestSaveLocalEnhanceRollsBackOnDiskFailure(t *testing.T) {
	settings := config.Default()
	directory := filepath.Join(t.TempDir(), "config-as-directory")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	app := application{
		settings: settings,
		layout:   paths.Layout{Config: directory},
		texts:    localization.New(localization.EN, ""),
	}
	next := settings.LocalEnhance
	next.StartupSoundEnabled = true
	if app.saveLocalEnhance(next) {
		t.Fatal("save unexpectedly succeeded for a directory destination")
	}
	if app.settings.LocalEnhance.StartupSoundEnabled {
		t.Fatal("failed disk save leaked into in-memory settings")
	}
}
