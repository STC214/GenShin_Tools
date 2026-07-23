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
		// Input enhancement retains the physical extended-key bit above the
		// Win32 virtual-key byte. RegisterHotKey only accepts the byte, so both
		// the navigation block and its keypad alias must be treated as a
		// conflict with the same screenshot hotkey.
		virtualKey := key & 0xff
		if virtualKey != 0 && hotkeyVirtualKeysConflict(virtualKey, config.VirtualKey) {
			return true
		}
	}
	return false
}

func hotkeyVirtualKeysConflict(left, right uint32) bool {
	if left == right {
		return true
	}
	leftAlias := keypadNavigationAlias(left)
	rightAlias := keypadNavigationAlias(right)
	return leftAlias != 0 && leftAlias == right ||
		rightAlias != 0 && rightAlias == left ||
		leftAlias != 0 && leftAlias == rightAlias
}

func keypadNavigationAlias(virtualKey uint32) uint32 {
	switch virtualKey {
	case 0x60:
		return 0x2d // Num 0 -> Insert
	case 0x61:
		return 0x23 // Num 1 -> End
	case 0x62:
		return 0x28 // Num 2 -> Down
	case 0x63:
		return 0x22 // Num 3 -> Page Down
	case 0x64:
		return 0x25 // Num 4 -> Left
	case 0x65:
		return 0x0c // Num 5 -> Clear
	case 0x66:
		return 0x27 // Num 6 -> Right
	case 0x67:
		return 0x24 // Num 7 -> Home
	case 0x68:
		return 0x26 // Num 8 -> Up
	case 0x69:
		return 0x21 // Num 9 -> Page Up
	case 0x6e:
		return 0x2e // Num . -> Delete
	default:
		return 0
	}
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
