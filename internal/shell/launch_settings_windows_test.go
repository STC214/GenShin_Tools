package shell

import (
	"os"
	"path/filepath"
	"testing"

	"genshintools/internal/config"
	"genshintools/internal/launch"
	"genshintools/internal/localization"
	"genshintools/internal/paths"
)

func TestExplicitLaunchAlwaysKeepsWindowState(t *testing.T) {
	for _, behavior := range []launch.PostBehavior{launch.PostKeep, launch.PostMinimize, launch.PostExit} {
		settings := launch.DefaultConfig()
		settings.PostBehavior = behavior
		got := explicitLaunchConfig(settings)
		if got.PostBehavior != launch.PostKeep {
			t.Fatalf("explicit launch with behavior %d retained post behavior %d", behavior, got.PostBehavior)
		}
	}
}

func TestCommitLaunchConfigCommitsAfterSave(t *testing.T) {
	app := application{
		settings: config.Default(),
		layout:   paths.Layout{Config: filepath.Join(t.TempDir(), "config.json")},
		texts:    localization.New(localization.EN, ""),
	}
	next := app.settings.Launch
	next.Width, next.Height = 2560, 1440
	if !app.commitLaunchConfig(next) {
		t.Fatalf("commit failed: %s", app.launchUIError)
	}
	if app.settings.Launch.Width != 2560 || app.settings.Launch.Height != 1440 {
		t.Fatal("saved launch settings were not committed in memory")
	}
}

func TestCommitLaunchConfigRestoresMemoryOnSaveFailure(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "config-as-directory")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	app := application{
		settings: config.Default(),
		layout:   paths.Layout{Config: destination},
		texts:    localization.New(localization.EN, ""),
	}
	original := app.settings.Launch
	next := original
	next.Width, next.Height = 2560, 1440
	if app.commitLaunchConfig(next) {
		t.Fatal("commit unexpectedly succeeded")
	}
	if app.settings.Launch != original {
		t.Fatal("failed launch settings save leaked into memory")
	}
}
