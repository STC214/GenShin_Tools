package win32

import (
	"strings"
	"testing"
)

func TestKeyNameUsesKeyboardLabelsInsteadOfVirtualKeyCodes(t *testing.T) {
	for _, key := range []uint32{'A', '7', 0x79, 0xBA, VK_RETURN, VK_UP} {
		name := KeyName(key)
		if strings.TrimSpace(name) == "" || strings.Contains(strings.ToUpper(name), "VK") || strings.Contains(strings.ToLower(name), "0x") {
			t.Fatalf("KeyName(0x%02X) exposed an internal key code: %q", key, name)
		}
	}
}

func TestKeyNameHandlesAmbiguousLegacyScanCodes(t *testing.T) {
	tests := map[uint32]string{
		0x13: "Pause",
		0x2c: "Print Screen",
		0x90: "Num Lock",
	}
	for key, want := range tests {
		if got := KeyName(key); got != want {
			t.Errorf("KeyName(0x%02X) = %q, want %q", key, got, want)
		}
	}
}
