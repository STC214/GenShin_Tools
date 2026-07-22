// Package capture implements bounded game-window screenshot requests.
package capture

import (
	"errors"
	"path/filepath"
	"strings"

	"genshintools/internal/platform/win32"
)

const (
	ModAlt      uint32 = 0x0001
	ModControl  uint32 = 0x0002
	ModShift    uint32 = 0x0004
	ModWin      uint32 = 0x0008
	ModNoRepeat uint32 = 0x4000

	VKF10 uint32 = 0x79
)

type Config struct {
	Enabled    bool   `json:"enabled"`
	SaveDir    string `json:"saveDir"`
	VirtualKey uint32 `json:"virtualKey"`
	Modifiers  uint32 `json:"modifiers"`
}

func DefaultConfig() Config {
	return Config{VirtualKey: VKF10, Modifiers: ModControl | ModShift | ModNoRepeat}
}

func (config Config) Normalized() (Config, error) {
	config.SaveDir = strings.Trim(strings.TrimSpace(config.SaveDir), `"`)
	if config.VirtualKey == 0 || config.VirtualKey > 0xFF {
		return Config{}, errors.New("screenshot virtual key must be within 1..255")
	}
	if config.Modifiers&^(ModAlt|ModControl|ModShift|ModWin|ModNoRepeat) != 0 {
		return Config{}, errors.New("screenshot hotkey contains unsupported modifiers")
	}
	config.Modifiers |= ModNoRepeat
	if config.SaveDir != "" {
		config.SaveDir = filepath.Clean(config.SaveDir)
	}
	return config, nil
}

func (config Config) ConflictsWith(keys ...uint32) bool {
	for _, key := range keys {
		if key != 0 && key == config.VirtualKey {
			return true
		}
	}
	return false
}

func (config Config) HotkeyString() string {
	var parts []string
	if config.Modifiers&ModControl != 0 {
		parts = append(parts, "Ctrl")
	}
	if config.Modifiers&ModAlt != 0 {
		parts = append(parts, "Alt")
	}
	if config.Modifiers&ModShift != 0 {
		parts = append(parts, "Shift")
	}
	if config.Modifiers&ModWin != 0 {
		parts = append(parts, "Win")
	}
	return strings.Join(append(parts, win32.KeyName(config.VirtualKey)), "+")
}
