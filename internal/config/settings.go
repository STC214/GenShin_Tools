// Package config owns the versioned executable-local application settings.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"genshintools/internal/capture"
	"genshintools/internal/injection"
	"genshintools/internal/input"
	"genshintools/internal/launch"
	"genshintools/internal/localenhance"
	"genshintools/internal/overlay"
	"genshintools/internal/plugins"
	"genshintools/internal/shellconfig"
)

const CurrentSchemaVersion = 9

// Settings contains only stable shell settings in S02. Feature settings are
// added by their implementation stage instead of being guessed in advance.
type Settings struct {
	SchemaVersion int                `json:"schemaVersion"`
	Window        WindowConfig       `json:"window"`
	Input         input.Config       `json:"input"`
	Game          GameConfig         `json:"game"`
	Launch        launch.Config      `json:"launch"`
	LocalEnhance  LocalEnhanceConfig `json:"localEnhance"`
	Capture       capture.Config     `json:"capture"`
	Overlay       overlay.Config     `json:"overlay"`
	Injection     injection.Config   `json:"injection"`
	Plugins       plugins.Config     `json:"plugins"`
	Shell         shellconfig.Config `json:"shell"`
}

type LocalEnhanceConfig struct {
	HDR                 localenhance.HDRConfig `json:"hdr"`
	StartupSoundEnabled bool                   `json:"startupSoundEnabled"`
	StartupSoundPath    string                 `json:"startupSoundPath"`
	BetterGIEnabled     bool                   `json:"betterGIEnabled"`
	BetterGIDelayMS     int                    `json:"betterGIDelayMs"`
}

type WindowConfig struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type GameConfig struct {
	Path             string `json:"path"`
	CustomExecutable string `json:"customExecutable"`
}

type LoadResult struct {
	Settings      Settings
	RecoveredFrom string
}

func Default() Settings {
	return Settings{
		SchemaVersion: CurrentSchemaVersion,
		Window: WindowConfig{
			X: -1, Y: -1,
			Width: 1100, Height: 720,
		},
		Input:        input.DefaultConfig(),
		Launch:       launch.DefaultConfig(),
		LocalEnhance: LocalEnhanceConfig{HDR: localenhance.DefaultHDRConfig()},
		Capture:      capture.DefaultConfig(),
		Overlay:      overlay.DefaultConfig(),
		Injection:    injection.DefaultConfig(),
		Plugins:      plugins.DefaultConfig(),
		Shell:        shellconfig.DefaultConfig(),
	}
}

func Load(path string) (LoadResult, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return LoadResult{Settings: Default()}, nil
	}
	if err != nil {
		return LoadResult{}, fmt.Errorf("read settings: %w", err)
	}

	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	settings := Default()
	// Interval is a runtime-only duration. Clear the default before decoding so
	// the persisted intervalMs value remains authoritative.
	settings.Input.Interval = 0
	if err := json.Unmarshal(data, &settings); err != nil {
		recovered, recoveryErr := quarantine(path)
		if recoveryErr != nil {
			return LoadResult{}, fmt.Errorf("decode settings: %v; quarantine: %w", err, recoveryErr)
		}
		return LoadResult{Settings: Default(), RecoveredFrom: recovered}, nil
	}
	if err := migrateAndValidate(&settings); err != nil {
		recovered, recoveryErr := quarantine(path)
		if recoveryErr != nil {
			return LoadResult{}, fmt.Errorf("validate settings: %v; quarantine: %w", err, recoveryErr)
		}
		return LoadResult{Settings: Default(), RecoveredFrom: recovered}, nil
	}
	return LoadResult{Settings: settings}, nil
}

func migrateAndValidate(settings *Settings) error {
	loadedSchema := settings.SchemaVersion
	switch settings.SchemaVersion {
	case 0:
		// Schema 0 was the pre-release shape with the same window fields.
		settings.Input = input.DefaultConfig()
		settings.LocalEnhance = Default().LocalEnhance
		settings.Capture, settings.Overlay, settings.Injection = Default().Capture, Default().Overlay, Default().Injection
		settings.SchemaVersion = CurrentSchemaVersion
	case 1:
		settings.Input = input.DefaultConfig()
		settings.LocalEnhance = Default().LocalEnhance
		settings.Capture, settings.Overlay, settings.Injection = Default().Capture, Default().Overlay, Default().Injection
		settings.SchemaVersion = CurrentSchemaVersion
	case 2:
		settings.LocalEnhance = Default().LocalEnhance
		settings.Capture, settings.Overlay, settings.Injection = Default().Capture, Default().Overlay, Default().Injection
		settings.SchemaVersion = CurrentSchemaVersion
	case 3:
		settings.Launch = launch.DefaultConfig()
		settings.LocalEnhance = Default().LocalEnhance
		settings.Capture, settings.Overlay, settings.Injection = Default().Capture, Default().Overlay, Default().Injection
		settings.SchemaVersion = CurrentSchemaVersion
	case 4:
		settings.LocalEnhance = Default().LocalEnhance
		settings.Capture, settings.Overlay, settings.Injection = Default().Capture, Default().Overlay, Default().Injection
		settings.SchemaVersion = CurrentSchemaVersion
	case 5:
		settings.Capture, settings.Overlay, settings.Injection = Default().Capture, Default().Overlay, Default().Injection
		settings.SchemaVersion = CurrentSchemaVersion
	case 6:
		settings.Injection = Default().Injection
		settings.SchemaVersion = CurrentSchemaVersion
	case 7:
		settings.SchemaVersion = CurrentSchemaVersion
	case 8:
		settings.SchemaVersion = CurrentSchemaVersion
	case 9:
	default:
		return fmt.Errorf("unsupported schema version %d", settings.SchemaVersion)
	}
	if loadedSchema < 8 {
		settings.Plugins = plugins.DefaultConfig()
	}
	if loadedSchema < 9 {
		settings.Shell = shellconfig.DefaultConfig()
	}
	if settings.Window.Width < 640 || settings.Window.Width > 10000 {
		settings.Window.Width = Default().Window.Width
	}
	if settings.Window.Height < 480 || settings.Window.Height > 10000 {
		settings.Window.Height = Default().Window.Height
	}
	normalized, err := settings.Input.Normalized()
	if err != nil {
		return fmt.Errorf("input settings: %w", err)
	}
	// Enabling input is a session decision. Parameters persist, but a restart
	// always returns to Disabled so stale physical state cannot begin output.
	normalized.Enabled = false
	settings.Input = normalized
	settings.Game.Path = strings.Trim(strings.TrimSpace(settings.Game.Path), `"`)
	settings.Game.CustomExecutable = strings.Trim(strings.TrimSpace(settings.Game.CustomExecutable), `"`)
	if settings.Game.CustomExecutable != "" && (filepath.Base(settings.Game.CustomExecutable) != settings.Game.CustomExecutable || !strings.EqualFold(filepath.Ext(settings.Game.CustomExecutable), ".exe")) {
		return errors.New("game custom executable must be a file name ending in .exe")
	}
	normalizedLaunch, err := settings.Launch.Normalized()
	if err != nil {
		return fmt.Errorf("launch settings: %w", err)
	}
	settings.Launch = normalizedLaunch
	normalizedHDR, err := settings.LocalEnhance.HDR.Normalized()
	if err != nil {
		return fmt.Errorf("HDR settings: %w", err)
	}
	settings.LocalEnhance.HDR = normalizedHDR
	settings.LocalEnhance.StartupSoundPath = strings.Trim(strings.TrimSpace(settings.LocalEnhance.StartupSoundPath), `"`)
	if settings.LocalEnhance.StartupSoundPath != "" && !strings.EqualFold(filepath.Ext(settings.LocalEnhance.StartupSoundPath), ".wav") {
		return errors.New("startup sound must be a WAV file")
	}
	if settings.LocalEnhance.BetterGIDelayMS < 0 || settings.LocalEnhance.BetterGIDelayMS > 60000 {
		return errors.New("BetterGI delay must be within 0..60000 ms")
	}
	normalizedCapture, err := settings.Capture.Normalized()
	if err != nil {
		return fmt.Errorf("capture settings: %w", err)
	}
	if normalizedCapture.ConflictsWith(settings.Input.TriggerKey, settings.Input.OutputKey, settings.Input.StopKey) {
		return errors.New("screenshot hotkey conflicts with an input enhancement physical key")
	}
	settings.Capture = normalizedCapture
	normalizedOverlay, err := settings.Overlay.Normalized()
	if err != nil {
		return fmt.Errorf("overlay settings: %w", err)
	}
	settings.Overlay = normalizedOverlay
	normalizedInjection, err := settings.Injection.Normalized()
	if err != nil {
		return fmt.Errorf("injection settings: %w", err)
	}
	settings.Injection = normalizedInjection
	normalizedPlugins, err := settings.Plugins.Normalized()
	if err != nil {
		return fmt.Errorf("plugin settings: %w", err)
	}
	settings.Plugins = normalizedPlugins
	normalizedShell, err := settings.Shell.Normalized()
	if err != nil {
		return fmt.Errorf("shell settings: %w", err)
	}
	settings.Shell = normalizedShell
	return nil
}

func Save(path string, settings Settings) error {
	settings.SchemaVersion = CurrentSchemaVersion
	if err := migrateAndValidate(&settings); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	data = append(data, '\n')

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary settings: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write temporary settings: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("flush temporary settings: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary settings: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return fmt.Errorf("commit settings: %w", err)
	}
	committed = true
	return nil
}

func quarantine(path string) (string, error) {
	timestamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	target := path + ".corrupt-" + timestamp
	if err := os.Rename(path, target); err != nil {
		return "", err
	}
	return target, nil
}
