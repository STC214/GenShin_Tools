package shell

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"genshintools/internal/capture"
	"genshintools/internal/config"
	"genshintools/internal/localization"
	"genshintools/internal/paths"
)

func newMediaSettingsTestApp(t *testing.T, configPath string) *application {
	t.Helper()
	settings := config.Default()
	root := t.TempDir()
	manager := capture.NewManager(nil, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Close(ctx); err != nil {
			t.Errorf("close capture manager: %v", err)
		}
	})
	app := &application{
		settings:       settings,
		layout:         paths.Layout{Root: root, Config: configPath},
		texts:          localization.New(localization.EN, ""),
		captureManager: manager,
	}
	if err := manager.Configure(app.runtimeCaptureConfig()); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestCommitCaptureSettingsCommitsAfterSave(t *testing.T) {
	app := newMediaSettingsTestApp(t, filepath.Join(t.TempDir(), "config.json"))
	next := app.settings.Capture
	next.SaveDir = filepath.Join("data", "captures")
	if !app.commitCaptureSettings(next) {
		t.Fatalf("commit failed: %s", app.captureStatus)
	}
	if app.settings.Capture.SaveDir != next.SaveDir {
		t.Fatal("saved capture settings were not committed in memory")
	}
}

func TestCommitCaptureSettingsRestoresMemoryOnSaveFailure(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "config-as-directory")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	app := newMediaSettingsTestApp(t, destination)
	previous := app.settings.Capture
	next := previous
	next.SaveDir = filepath.Join("data", "different")
	if app.commitCaptureSettings(next) {
		t.Fatal("commit unexpectedly succeeded")
	}
	if app.settings.Capture.SaveDir != previous.SaveDir {
		t.Fatal("failed capture save leaked into memory")
	}
}

func TestCommitOverlaySettingsRestoresMemoryOnSaveFailure(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "config-as-directory")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	app := newMediaSettingsTestApp(t, destination)
	previous := app.settings.Overlay
	next := previous
	next.Enabled = true
	if app.commitOverlaySettings(next) {
		t.Fatal("commit unexpectedly succeeded")
	}
	if app.settings.Overlay.Enabled != previous.Enabled {
		t.Fatal("failed overlay save leaked into memory")
	}
}

func TestAdjustInputIntervalReachesOneMillisecond(t *testing.T) {
	interval := 50 * time.Millisecond
	for range 5 {
		interval = adjustInputInterval(interval, false)
	}
	if interval != 9*time.Millisecond {
		t.Fatalf("decremented interval = %s, want 9ms", interval)
	}
	for range 20 {
		interval = adjustInputInterval(interval, false)
	}
	if interval != time.Millisecond {
		t.Fatalf("minimum interval = %s, want 1ms", interval)
	}
	if got := adjustInputInterval(interval, true); got != 2*time.Millisecond {
		t.Fatalf("increment from minimum = %s, want 2ms", got)
	}
	if got := adjustInputInterval(5*time.Second, true); got != 5*time.Second {
		t.Fatalf("maximum interval = %s, want 5s", got)
	}
}

func TestInputIntervalSymbolsUseTheExpectedDirection(t *testing.T) {
	const left = 252
	if inputIntervalIncreaseAt(left+24, left, 96) {
		t.Fatal("minus symbol was classified as increase")
	}
	if !inputIntervalIncreaseAt(left+180, left, 96) {
		t.Fatal("plus symbol was classified as decrease")
	}
}
