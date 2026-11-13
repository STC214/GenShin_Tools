package shell

import (
	"fmt"
	"strconv"
	"strings"

	"genshintools/internal/platform/win32"
	"genshintools/internal/plugins"
)

// Keep the plugin's numeric INI format at the persistence boundary only.
func configFieldDisplayValue(field plugins.ConfigField, value string) string {
	if field.Type == "key" {
		if code, err := strconv.ParseUint(value, 10, 8); err == nil && code != 0 {
			if name := win32.KeyName(uint32(code)); name != "Unknown key" {
				return name
			}
		}
	}
	return value
}

func configFieldStoredValue(field plugins.ConfigField, value string) (string, error) {
	if field.Type != "key" {
		return value, nil
	}
	name := strings.TrimSpace(value)
	// A single digit means the labelled number-row key, not a virtual-key code.
	if len(name) == 1 && (name[0] >= '0' && name[0] <= '9' || name[0] >= 'A' && name[0] <= 'Z' || name[0] >= 'a' && name[0] <= 'z') {
		return strconv.Itoa(int(strings.ToUpper(name)[0])), nil
	}
	aliases := map[string]uint32{
		"backspace": 0x08, "tab": 0x09, "enter": 0x0d, "return": 0x0d,
		"shift": 0x10, "ctrl": 0x11, "control": 0x11, "alt": 0x12,
		"pause": 0x13, "caps lock": 0x14, "esc": 0x1b, "escape": 0x1b,
		"space": 0x20, "spacebar": 0x20, "page up": 0x21, "pgup": 0x21,
		"page down": 0x22, "pgdn": 0x22, "end": 0x23, "home": 0x24,
		"left": 0x25, "up": 0x26, "right": 0x27, "down": 0x28,
		"print screen": 0x2c, "insert": 0x2d, "ins": 0x2d, "delete": 0x2e, "del": 0x2e,
		"num lock": 0x90, "scroll lock": 0x91,
		"mouse left": 0x01, "mouse right": 0x02, "mouse middle": 0x04,
		"mouse x1": 0x05, "mouse x2": 0x06,
	}
	if code, ok := aliases[strings.ToLower(name)]; ok {
		return strconv.FormatUint(uint64(code), 10), nil
	}
	for code := uint32(1); code <= 255; code++ {
		label := win32.KeyName(code)
		if label != "Unknown key" && strings.EqualFold(name, label) {
			return strconv.FormatUint(uint64(code), 10), nil
		}
	}
	// Accept old multi-digit codes for existing workflows as well.
	if code, err := strconv.ParseUint(name, 10, 8); err == nil && code != 0 {
		return strconv.FormatUint(code, 10), nil
	}
	return "", fmt.Errorf("unknown key name %q (e.g. F10, Insert, Space)", value)
}
