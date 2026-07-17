// Package launch owns the pure, non-injecting game launch state machine.
package launch

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"genshintools/internal/game"
)

type WindowMode uint8

const (
	WindowDefault WindowMode = iota
	WindowFullscreen
	WindowWindowed
	WindowBorderless
)

func (m WindowMode) String() string {
	switch m {
	case WindowFullscreen:
		return "全屏"
	case WindowWindowed:
		return "窗口"
	case WindowBorderless:
		return "无边框"
	default:
		return "游戏默认"
	}
}

type PostBehavior uint8

const (
	PostKeep PostBehavior = iota
	PostMinimize
	PostExit
)

type Config struct {
	Width           int          `json:"width"`
	Height          int          `json:"height"`
	Monitor         int          `json:"monitor"`
	WindowMode      WindowMode   `json:"windowMode"`
	CustomArguments string       `json:"customArguments"`
	PostBehavior    PostBehavior `json:"postBehavior"`
}

func DefaultConfig() Config {
	return Config{Width: 1920, Height: 1080, WindowMode: WindowDefault, PostBehavior: PostKeep}
}

func (c Config) Normalized() (Config, error) {
	if (c.Width == 0) != (c.Height == 0) {
		return Config{}, errors.New("width and height must both be zero or both be set")
	}
	if c.Width != 0 && (c.Width < 640 || c.Width > 16384 || c.Height < 480 || c.Height > 16384) {
		return Config{}, errors.New("resolution must be between 640x480 and 16384x16384")
	}
	if c.Monitor < 0 || c.Monitor > 64 {
		return Config{}, errors.New("monitor index must be between 0 and 64")
	}
	if c.WindowMode > WindowBorderless {
		return Config{}, fmt.Errorf("invalid window mode %d", c.WindowMode)
	}
	if c.PostBehavior > PostExit {
		return Config{}, fmt.Errorf("invalid post-launch behavior %d", c.PostBehavior)
	}
	if len(c.CustomArguments) > 8192 {
		return Config{}, errors.New("custom arguments exceed 8192 characters")
	}
	if strings.ContainsRune(c.CustomArguments, 0) {
		return Config{}, errors.New("custom arguments contain NUL")
	}
	c.CustomArguments = strings.TrimSpace(c.CustomArguments)
	return c, nil
}

func BuildArguments(config Config) ([]string, error) {
	config, err := config.Normalized()
	if err != nil {
		return nil, err
	}
	var arguments []string
	if config.Width > 0 {
		arguments = append(arguments, "-screen-width", fmt.Sprint(config.Width), "-screen-height", fmt.Sprint(config.Height))
	}
	switch config.WindowMode {
	case WindowFullscreen:
		arguments = append(arguments, "-screen-fullscreen", "1", "-window-mode", "exclusive")
	case WindowWindowed:
		arguments = append(arguments, "-screen-fullscreen", "0")
	case WindowBorderless:
		arguments = append(arguments, "-screen-fullscreen", "0", "-window-mode", "borderless", "-popupwindow")
	}
	if config.Monitor > 0 {
		arguments = append(arguments, "-monitor", fmt.Sprint(config.Monitor))
	}
	custom, err := ParseCustomArguments(config.CustomArguments)
	if err != nil {
		return nil, fmt.Errorf("parse custom arguments: %w", err)
	}
	return append(arguments, custom...), nil
}

type State uint8

const (
	StateIdle State = iota
	StateStarting
	StateRunning
	StateExited
	StateFailed
)

func (s State) String() string {
	switch s {
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateExited:
		return "exited"
	case StateFailed:
		return "failed"
	default:
		return "idle"
	}
}

type Snapshot struct {
	State        State
	Generation   uint64
	PID          int
	Owned        bool
	StartedAt    time.Time
	ExitedAt     time.Time
	ExitCode     int
	LastError    string
	Arguments    []string
	Executable   string
	WorkingDir   string
	PostBehavior PostBehavior
}

type Request struct {
	Candidate game.Candidate
	Config    Config
	Arguments []string
}

type Process interface {
	PID() int
	Wait() (int, error)
}

type Starter interface {
	Start(Request) (Process, error)
}
