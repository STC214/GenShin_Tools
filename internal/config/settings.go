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

	"genshintools/internal/input"
	"genshintools/internal/launch"
)

const CurrentSchemaVersion = 4

// Settings contains only stable shell settings in S02. Feature settings are
// added by their implementation stage instead of being guessed in advance.
type Settings struct {
	SchemaVersion int           `json:"schemaVersion"`
	Window        WindowConfig  `json:"window"`
	Input         input.Config  `json:"input"`
	Game          GameConfig    `json:"game"`
	Launch        launch.Config `json:"launch"`
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
		Input:  input.DefaultConfig(),
		Launch: launch.DefaultConfig(),
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
	switch settings.SchemaVersion {
	case 0:
		// Schema 0 was the pre-release shape with the same window fields.
		settings.Input = input.DefaultConfig()
		settings.SchemaVersion = CurrentSchemaVersion
	case 1:
		settings.Input = input.DefaultConfig()
		settings.SchemaVersion = CurrentSchemaVersion
	case 2:
		settings.SchemaVersion = CurrentSchemaVersion
	case 3:
		settings.Launch = launch.DefaultConfig()
		settings.SchemaVersion = CurrentSchemaVersion
	case 4:
	default:
		return fmt.Errorf("unsupported schema version %d", settings.SchemaVersion)
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
