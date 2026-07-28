package shell

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"genshintools/internal/capture"
	"genshintools/internal/config"
	"genshintools/internal/input"
	"genshintools/internal/localization"
	"genshintools/internal/paths"
	"genshintools/internal/platform/win32"
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

func TestRecordedInputKeyRollsBackWhenSettingsCannotBeSaved(t *testing.T) {
	native, err := input.NewNative(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(native.Close)
	settings := config.Default()
	if err := native.Configure(settings.Input); err != nil {
		t.Fatal(err)
	}
	// Make the aggregate settings invalid after startup so the atomic save
	// fails without relying on filesystem permissions.
	settings.Shell.Language = "invalid-language"
	app := &application{
		settings:    settings,
		layout:      paths.Layout{Config: filepath.Join(t.TempDir(), "config.json")},
		texts:       localization.New(localization.EN, ""),
		inputNative: native,
		recording:   4,
	}
	beforeNative := native.Snapshot().Config
	beforeSettings := app.settings.Input
	app.recordPhysical(input.PhysicalEvent{Kind: input.EventKey, Code: input.EncodeKeyCode('A', false), Down: true})
	if after := native.Snapshot().Config; after != beforeNative {
		t.Fatalf("native input was not rolled back: got %+v want %+v", after, beforeNative)
	}
	if app.settings.Input != beforeSettings {
		t.Fatalf("in-memory input settings were not rolled back: got %+v want %+v", app.settings.Input, beforeSettings)
	}
	if app.inputUIError == "" {
		t.Fatal("save failure was not exposed in the input UI")
	}
}

func TestInputClickRollsBackIntervalWhenSettingsCannotBeSaved(t *testing.T) {
	native, err := input.NewNative(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(native.Close)
	settings := config.Default()
	if err := native.Configure(settings.Input); err != nil {
		t.Fatal(err)
	}
	settings.Shell.Language = "invalid-language"
	app := &application{
		dpi:         96,
		settings:    settings,
		layout:      paths.Layout{Config: filepath.Join(t.TempDir(), "config.json")},
		texts:       localization.New(localization.EN, ""),
		inputNative: native,
	}
	beforeNative := native.Snapshot().Config
	beforeSettings := app.settings.Input
	app.inputClick(400, 580)
	if after := native.Snapshot().Config; after != beforeNative {
		t.Fatalf("native interval was not rolled back: got %+v want %+v", after, beforeNative)
	}
	if app.settings.Input != beforeSettings {
		t.Fatalf("in-memory interval was not rolled back: got %+v want %+v", app.settings.Input, beforeSettings)
	}
	if app.inputUIError == "" {
		t.Fatal("save failure was not exposed in the input UI")
	}
}

func TestInputRepeatHitTestExcludesSpacingAndScrollbarGap(t *testing.T) {
	const dpi = 144
	left := win32.Scale(252, dpi)
	right := win32.Scale(1058, dpi)
	rowRight := right - win32.Scale(22, dpi)

	index, deleteRow, hit := inputRepeatHitTest(
		int(rowRight-win32.Scale(20, dpi)),
		int(win32.Scale(280, dpi)),
		left,
		right,
		dpi,
		3,
		0,
	)
	if !hit || index != 0 || !deleteRow {
		t.Fatalf("delete button hit = index %d delete=%t hit=%t", index, deleteRow, hit)
	}
	if _, _, hit := inputRepeatHitTest(int(rowRight+win32.Scale(3, dpi)), int(win32.Scale(280, dpi)), left, right, dpi, 3, 0); hit {
		t.Fatal("blank gap before the scrollbar hit a repeat row")
	}
	if _, _, hit := inputRepeatHitTest(int(left+win32.Scale(20, dpi)), int(win32.Scale(314, dpi)), left, right, dpi, 3, 0); hit {
		t.Fatal("inter-row spacing hit a repeat row")
	}
	index, _, hit = inputRepeatHitTest(int(left+win32.Scale(20, dpi)), int(win32.Scale(322, dpi)), left, right, dpi, 3, 1)
	if !hit || index != 2 {
		t.Fatalf("DPI-scaled second visible row = index %d hit=%t, want index 2", index, hit)
	}
}

func TestInputFooterRectsReserveIndependentAHKOptionAndStatus(t *testing.T) {
	output, option, status := inputFooterRects(252, 1000, 96)
	if output.Left != 252 || status.Right != 1000 {
		t.Fatalf("footer does not span content: output=%+v status=%+v", output, status)
	}
	if output.Right >= option.Left || option.Right >= status.Left {
		t.Fatalf("footer controls overlap or have no gaps: output=%+v option=%+v status=%+v", output, option, status)
	}
	for _, rect := range []win32.Rect{output, option, status} {
		if rect.Bottom <= rect.Top || rect.Right <= rect.Left {
			t.Fatalf("invalid footer rectangle: %+v", rect)
		}
	}
}

func TestProductInputModesExcludeBuiltInKeyboardRepeat(t *testing.T) {
	modes := productInputModes()
	if len(modes) != 2 || modes[0] != input.ModeMouseLeft || modes[1] != input.ModeMouseRight {
		t.Fatalf("product input modes = %v, want mouse left/right only", modes)
	}
	for _, mode := range modes {
		if mode == input.ModeKeyboard {
			t.Fatal("retired built-in keyboard repeat remains in product modes")
		}
	}
}

func TestInputPageMinimumHeightAlsoAppliesToRestoredBounds(t *testing.T) {
	got := enforceMinimumWindowSize(config.WindowConfig{Width: 320, Height: 560})
	if got.Width != minimumWindowWidth || got.Height != minimumWindowHeight {
		t.Fatalf("restored minimum bounds = %dx%d, want %dx%d", got.Width, got.Height, minimumWindowWidth, minimumWindowHeight)
	}
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
