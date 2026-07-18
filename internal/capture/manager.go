package capture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"genshintools/internal/gamewindow"
)

type Target = gamewindow.Target

type Result struct {
	Path        string
	Error       string
	CompletedAt time.Time
}

type Capturer interface {
	Capture(Target, string) error
}

type Manager struct {
	mu       sync.RWMutex
	config   Config
	target   *Target
	capturer Capturer
	publish  func(Result)
	requests chan struct{}
	stop     chan struct{}
	done     chan struct{}
	closed   atomic.Bool
	sequence atomic.Uint64
}

func NewManager(capturer Capturer, publish func(Result)) *Manager {
	if capturer == nil {
		capturer = NativeCapturer{}
	}
	manager := &Manager{config: DefaultConfig(), capturer: capturer, publish: publish, requests: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	go manager.run()
	return manager
}

func (manager *Manager) Configure(config Config) error {
	normalized, err := config.Normalized()
	if err != nil {
		return err
	}
	manager.mu.Lock()
	manager.config = normalized
	manager.mu.Unlock()
	return nil
}

func (manager *Manager) SetTarget(target *Target) {
	manager.mu.Lock()
	if target == nil {
		manager.target = nil
	} else {
		copy := *target
		manager.target = &copy
	}
	manager.mu.Unlock()
}

func (manager *Manager) Request() bool {
	if manager.closed.Load() {
		return false
	}
	manager.mu.RLock()
	enabled, hasTarget := manager.config.Enabled, manager.target != nil
	manager.mu.RUnlock()
	if !enabled || !hasTarget {
		return false
	}
	select {
	case manager.requests <- struct{}{}:
		return true
	default:
		return false
	}
}

func (manager *Manager) Close(ctx context.Context) error {
	if manager.closed.CompareAndSwap(false, true) {
		close(manager.stop)
	}
	select {
	case <-manager.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (manager *Manager) run() {
	defer close(manager.done)
	for {
		select {
		case <-manager.stop:
			return
		case <-manager.requests:
			manager.captureOne()
		}
	}
}

func (manager *Manager) captureOne() {
	manager.mu.RLock()
	config := manager.config
	var target Target
	if manager.target != nil {
		target = *manager.target
	}
	manager.mu.RUnlock()
	if !config.Enabled || target.PID == 0 {
		return
	}
	directory := config.SaveDir
	if directory == "" {
		manager.publishResult(Result{Error: "screenshot save directory is not configured", CompletedAt: time.Now()})
		return
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		manager.publishResult(Result{Error: fmt.Sprintf("create screenshot directory: %v", err), CompletedAt: time.Now()})
		return
	}
	sequence := manager.sequence.Add(1)
	name := fmt.Sprintf("Genshin_%s_%03d.png", time.Now().Format("20060102_150405_000"), sequence%1000)
	path := filepath.Join(directory, name)
	err := captureSafely(manager.capturer, target, path)
	result := Result{Path: path, CompletedAt: time.Now()}
	if err != nil {
		result.Path = ""
		result.Error = err.Error()
	}
	manager.publishResult(result)
}

func (manager *Manager) publishResult(result Result) {
	if manager.publish != nil && !manager.closed.Load() {
		func() {
			defer func() { _ = recover() }()
			manager.publish(result)
		}()
	}
}

func captureSafely(capturer Capturer, target Target, path string) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("screenshot capturer panic: %v", value)
		}
	}()
	return capturer.Capture(target, path)
}

var ErrNoGameWindow = errors.New("no verified visible game window")
