// Package input implements the S03 keyboard-repeat and mouse-click state machine.
package input

import (
	"errors"
	"fmt"
	"time"
)

type Mode uint8

const (
	ModeKeyboard Mode = iota
	ModeMouseLeft
	ModeMouseRight
)

func (m Mode) String() string {
	switch m {
	case ModeKeyboard:
		return "keyboard"
	case ModeMouseLeft:
		return "mouse-left"
	case ModeMouseRight:
		return "mouse-right"
	default:
		return "unknown"
	}
}

type State uint8

const (
	StateDisabled State = iota
	StateArmed
	StateRunning
	StateStopping
	StateFault
)

func (s State) String() string {
	switch s {
	case StateDisabled:
		return "disabled"
	case StateArmed:
		return "armed"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateFault:
		return "fault"
	default:
		return "unknown"
	}
}

type Config struct {
	Enabled             bool          `json:"enabled"`
	Mode                Mode          `json:"mode"`
	TriggerKey          uint32        `json:"triggerKey"`
	OutputKey           uint32        `json:"outputKey"`
	StopKey             uint32        `json:"stopKey"`
	KeyboardToggleKey   uint32        `json:"keyboardToggleKey"`
	MouseLeftToggleKey  uint32        `json:"mouseLeftToggleKey"`
	MouseRightToggleKey uint32        `json:"mouseRightToggleKey"`
	Interval            time.Duration `json:"-"`
	IntervalMS          int           `json:"intervalMs"`
}

func DefaultConfig() Config {
	return Config{
		Mode:                ModeKeyboard,
		TriggerKey:          EncodeKeyCode('F', false),
		OutputKey:           EncodeKeyCode('F', false),
		StopKey:             EncodeKeyCode(0x7B, false),
		KeyboardToggleKey:   EncodeKeyCode(0x77, false),
		MouseLeftToggleKey:  EncodeKeyCode(0x78, false),
		MouseRightToggleKey: EncodeKeyCode(0x7A, false),
		Interval:            50 * time.Millisecond, IntervalMS: 50,
	}
}

func (c Config) Normalized() (Config, error) {
	if c.Mode > ModeMouseRight {
		return Config{}, fmt.Errorf("invalid mode %d", c.Mode)
	}
	if c.Interval == 0 {
		c.Interval = time.Duration(c.IntervalMS) * time.Millisecond
	}
	if c.Interval < time.Millisecond || c.Interval > 5*time.Second {
		return Config{}, errors.New("interval must be between 1 and 5000 milliseconds")
	}
	c.IntervalMS = int(c.Interval / time.Millisecond)
	defaults := DefaultConfig()
	if c.KeyboardToggleKey == 0 {
		c.KeyboardToggleKey = defaults.KeyboardToggleKey
	}
	if c.MouseLeftToggleKey == 0 {
		c.MouseLeftToggleKey = defaults.MouseLeftToggleKey
	}
	if c.MouseRightToggleKey == 0 {
		c.MouseRightToggleKey = defaults.MouseRightToggleKey
	}
	c.StopKey = NormalizeKeyCode(c.StopKey)
	c.KeyboardToggleKey = NormalizeKeyCode(c.KeyboardToggleKey)
	c.MouseLeftToggleKey = NormalizeKeyCode(c.MouseLeftToggleKey)
	c.MouseRightToggleKey = NormalizeKeyCode(c.MouseRightToggleKey)
	c.OutputKey = NormalizeKeyCode(c.OutputKey)
	keys := []uint32{c.StopKey, c.KeyboardToggleKey, c.MouseLeftToggleKey, c.MouseRightToggleKey}
	seen := make(map[uint32]bool, len(keys))
	for _, key := range keys {
		if !ValidKeyCode(key) {
			return Config{}, errors.New("stop and toggle keys must be valid keyboard keys")
		}
		if seen[key] {
			return Config{}, errors.New("stop and toggle keys must be different")
		}
		seen[key] = true
	}
	if !ValidKeyCode(c.OutputKey) {
		return Config{}, errors.New("repeat key is required")
	}
	if seen[c.OutputKey] {
		return Config{}, errors.New("repeat key must differ from stop and toggle keys")
	}
	// TriggerKey is retained in the JSON schema for backward compatibility,
	// but keyboard repeat now follows the key being repeated.
	c.TriggerKey = c.OutputKey
	return c, nil
}

const (
	ExtendedKeyFlag uint32 = 0x100
	KeyIdentityFlag uint32 = 0x200
)

func EncodeKeyCode(virtualKey uint32, extended bool) uint32 {
	if !extended {
		// The low-level hook reports keypad digits as navigation virtual keys
		// while Num Lock is off. Canonicalize those aliases so one physical
		// keypad key keeps working and displaying consistently in either state.
		switch virtualKey & 0xff {
		case 0x2d: // VK_INSERT
			virtualKey = 0x60 // VK_NUMPAD0
		case 0x23: // VK_END
			virtualKey = 0x61 // VK_NUMPAD1
		case 0x28: // VK_DOWN
			virtualKey = 0x62 // VK_NUMPAD2
		case 0x22: // VK_NEXT
			virtualKey = 0x63 // VK_NUMPAD3
		case 0x25: // VK_LEFT
			virtualKey = 0x64 // VK_NUMPAD4
		case 0x0c: // VK_CLEAR
			virtualKey = 0x65 // VK_NUMPAD5
		case 0x27: // VK_RIGHT
			virtualKey = 0x66 // VK_NUMPAD6
		case 0x24: // VK_HOME
			virtualKey = 0x67 // VK_NUMPAD7
		case 0x26: // VK_UP
			virtualKey = 0x68 // VK_NUMPAD8
		case 0x21: // VK_PRIOR
			virtualKey = 0x69 // VK_NUMPAD9
		case 0x2e: // VK_DELETE
			virtualKey = 0x6e // VK_DECIMAL
		}
	}
	virtualKey = KeyIdentityFlag | virtualKey&0xff
	if extended {
		virtualKey |= ExtendedKeyFlag
	}
	return virtualKey
}

func VirtualKey(code uint32) uint32 { return code & 0xff }

func KeyIsExtended(code uint32) bool { return code&ExtendedKeyFlag != 0 }

func ValidKeyCode(code uint32) bool {
	return code <= KeyIdentityFlag|ExtendedKeyFlag|0xff && VirtualKey(code) != 0
}

func NormalizeKeyCode(code uint32) uint32 {
	if code&KeyIdentityFlag != 0 {
		return EncodeKeyCode(VirtualKey(code), KeyIsExtended(code))
	}
	virtualKey := VirtualKey(code)
	extended := KeyIsExtended(code)
	switch virtualKey {
	case 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x2d, 0x2e:
		// Legacy configurations could not retain the extended flag. These
		// virtual keys overwhelmingly refer to the dedicated navigation block;
		// newly recorded keypad variants carry KeyIdentityFlag and remain
		// distinguishable.
		extended = true
	}
	return EncodeKeyCode(virtualKey, extended)
}

func SameKey(left, right uint32) bool {
	return NormalizeKeyCode(left) == NormalizeKeyCode(right)
}

func (c Config) ToggleMode(code uint32) (Mode, bool) {
	switch {
	case SameKey(code, c.KeyboardToggleKey):
		return ModeKeyboard, true
	case SameKey(code, c.MouseLeftToggleKey):
		return ModeMouseLeft, true
	case SameKey(code, c.MouseRightToggleKey):
		return ModeMouseRight, true
	default:
		return 0, false
	}
}

type EventKind uint8

const (
	EventKey EventKind = iota
	EventMouseLeft
	EventMouseRight
)

type PhysicalEvent struct {
	Kind EventKind
	Code uint32
	Down bool
}

type Snapshot struct {
	State       State
	Config      Config
	Generation  uint64
	OutputCount uint64
	LastError   string
}

type Injector interface {
	Emit(Config) error
	Release(Config) error
}
