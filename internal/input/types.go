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
	Enabled    bool          `json:"enabled"`
	Mode       Mode          `json:"mode"`
	TriggerKey uint32        `json:"triggerKey"`
	OutputKey  uint32        `json:"outputKey"`
	StopKey    uint32        `json:"stopKey"`
	Interval   time.Duration `json:"-"`
	IntervalMS int           `json:"intervalMs"`
}

func DefaultConfig() Config {
	return Config{Mode: ModeKeyboard, TriggerKey: 'F', OutputKey: 'F', StopKey: 0x7B, Interval: 50 * time.Millisecond, IntervalMS: 50}
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
	if c.StopKey == 0 {
		return Config{}, errors.New("stop key is required")
	}
	if c.Mode == ModeKeyboard {
		if c.OutputKey == 0 {
			return Config{}, errors.New("repeat key is required")
		}
		if c.StopKey == c.OutputKey {
			return Config{}, errors.New("stop key must differ from the repeat key")
		}
		// TriggerKey is retained in the JSON schema for backward compatibility,
		// but keyboard repeat now follows the key being repeated.
		c.TriggerKey = c.OutputKey
	}
	return c, nil
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
