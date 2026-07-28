// Package input implements the S03 keyboard-repeat and mouse-click state machine.
package input

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Mode uint8

// BuiltInKeyboardRepeatEnabled is deliberately false for the product path.
// Keyboard repeat is provided only by the bundled AHK_F.exe lifecycle.
// The legacy engine and worker code remain buildable for compatibility tests,
// but Native never exposes or starts them.
const BuiltInKeyboardRepeatEnabled = false

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
	RepeatKeys          KeyList       `json:"repeatKeys"`
	StopKey             uint32        `json:"stopKey"`
	KeyboardToggleKey   uint32        `json:"keyboardToggleKey"`
	MouseLeftToggleKey  uint32        `json:"mouseLeftToggleKey"`
	MouseRightToggleKey uint32        `json:"mouseRightToggleKey"`
	Interval            time.Duration `json:"-"`
	IntervalMS          int           `json:"intervalMs"`
}

func DefaultConfig() Config {
	return Config{
		Mode:                ModeMouseLeft,
		TriggerKey:          EncodeKeyCode('F', false),
		OutputKey:           EncodeKeyCode('F', false),
		RepeatKeys:          NewKeyList(EncodeKeyCode('F', false)),
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
	if !BuiltInKeyboardRepeatEnabled {
		// Retired fields must not reserve invisible shortcuts or make a
		// current mouse/AHK configuration impossible to load. Preserve valid
		// historical values for diagnostics, but repair malformed hidden data
		// rather than rejecting the active product configuration.
		if !ValidKeyCode(NormalizeKeyCode(c.KeyboardToggleKey)) {
			c.KeyboardToggleKey = defaults.KeyboardToggleKey
		}
		if c.RepeatKeys.Present() {
			for _, key := range c.RepeatKeys.Slice() {
				if !ValidKeyCode(NormalizeKeyCode(key)) {
					c.RepeatKeys = defaults.RepeatKeys
					break
				}
			}
		}
	}
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
	keys := []uint32{c.StopKey, c.MouseLeftToggleKey, c.MouseRightToggleKey}
	if BuiltInKeyboardRepeatEnabled {
		keys = append(keys, c.KeyboardToggleKey)
	}
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
	// A nil list identifies legacy settings. An explicit empty list is valid
	// and lets the UI remove every repeat row before adding new ones.
	if !c.RepeatKeys.Present() {
		legacy := c.OutputKey
		if !ValidKeyCode(legacy) {
			legacy = defaults.OutputKey
		}
		c.RepeatKeys = NewKeyList(legacy)
	}
	repeatSeen := make(map[uint32]bool, c.RepeatKeys.Len())
	for index, key := range c.RepeatKeys.Slice() {
		key = NormalizeKeyCode(key)
		if !ValidKeyCode(key) {
			return Config{}, errors.New("repeat keys must be valid keyboard keys")
		}
		if BuiltInKeyboardRepeatEnabled && seen[key] {
			return Config{}, errors.New("repeat keys must differ from stop and toggle keys")
		}
		if BuiltInKeyboardRepeatEnabled && repeatSeen[key] {
			return Config{}, errors.New("repeat keys must be different")
		}
		repeatSeen[key] = true
		c.RepeatKeys.Set(index, key)
	}
	// Retain the first key in the legacy fields for older readers.
	c.OutputKey, c.TriggerKey = 0, 0
	if c.RepeatKeys.Len() != 0 {
		c.OutputKey = c.RepeatKeys.At(0)
		c.TriggerKey = c.RepeatKeys.At(0)
	}
	return c, nil
}

func (c Config) IsRepeatKey(code uint32) bool {
	for _, key := range c.RepeatKeys.Slice() {
		if SameKey(code, key) {
			return true
		}
	}
	return false
}

const MaxRepeatKeys = 16

// KeyList remains comparable so Settings can be transactionally compared and
// rolled back, while its JSON representation stays a normal variable-length
// array for users and older tooling.
type KeyList struct {
	keys    [MaxRepeatKeys]uint32
	count   uint8
	present bool
}

func NewKeyList(keys ...uint32) KeyList {
	var result KeyList
	result.present = true
	for _, key := range keys {
		if !result.Append(key) {
			break
		}
	}
	return result
}

func (keys KeyList) Present() bool { return keys.present }
func (keys KeyList) Len() int      { return int(keys.count) }
func (keys KeyList) At(index int) uint32 {
	if index < 0 || index >= keys.Len() {
		return 0
	}
	return keys.keys[index]
}
func (keys KeyList) Slice() []uint32 {
	return append([]uint32(nil), keys.keys[:keys.Len()]...)
}
func (keys *KeyList) Set(index int, key uint32) bool {
	if index < 0 || index >= keys.Len() {
		return false
	}
	keys.keys[index] = key
	keys.present = true
	return true
}
func (keys *KeyList) Append(key uint32) bool {
	if keys.Len() >= MaxRepeatKeys {
		return false
	}
	keys.keys[keys.count] = key
	keys.count++
	keys.present = true
	return true
}
func (keys *KeyList) Delete(index int) bool {
	if index < 0 || index >= keys.Len() {
		return false
	}
	copy(keys.keys[index:], keys.keys[index+1:keys.Len()])
	keys.count--
	keys.keys[keys.count] = 0
	keys.present = true
	return true
}
func (keys KeyList) MarshalJSON() ([]byte, error) {
	return json.Marshal(keys.keys[:keys.Len()])
}
func (keys *KeyList) UnmarshalJSON(data []byte) error {
	var values []uint32
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	if len(values) > MaxRepeatKeys {
		return fmt.Errorf("no more than %d repeat keys are allowed", MaxRepeatKeys)
	}
	*keys = NewKeyList(values...)
	return nil
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
	case BuiltInKeyboardRepeatEnabled && SameKey(code, c.KeyboardToggleKey):
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
