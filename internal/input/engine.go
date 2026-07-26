package input

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var errOutputTargetLost = errors.New("input output target is no longer the configured game window")

type Engine struct {
	mu         sync.Mutex
	emitMu     sync.Mutex
	config     Config
	state      State
	gen        uint64
	count      uint64
	lastErr    string
	cancel     context.CancelFunc
	injector   Injector
	onChange   func(Snapshot)
	closed     bool
	toggleHeld map[uint32]bool
	repeatHeld map[uint32]bool
}

func NewEngine(injector Injector, onChange func(Snapshot)) (*Engine, error) {
	if injector == nil {
		return nil, fmt.Errorf("injector is required")
	}
	config, _ := DefaultConfig().Normalized()
	return &Engine{
		config:     config,
		state:      StateDisabled,
		injector:   injector,
		onChange:   onChange,
		toggleHeld: map[uint32]bool{},
		repeatHeld: map[uint32]bool{},
	}, nil
}

func (e *Engine) Configure(config Config) error {
	normalized, err := config.Normalized()
	if err != nil {
		return err
	}
	e.stop(false)
	e.mu.Lock()
	e.config = normalized
	clear(e.toggleHeld)
	clear(e.repeatHeld)
	if normalized.Enabled {
		e.state = StateArmed
	} else {
		e.state = StateDisabled
	}
	e.lastErr = ""
	snapshot := e.snapshotLocked()
	e.mu.Unlock()
	e.notify(snapshot)
	return nil
}

func (e *Engine) Enable(enabled bool) {
	if !enabled {
		e.stop(true)
		return
	}
	e.mu.Lock()
	if !e.closed && e.state != StateFault {
		e.config.Enabled = true
		if e.state != StateRunning {
			e.state = StateArmed
		}
	}
	snapshot := e.snapshotLocked()
	e.mu.Unlock()
	e.notify(snapshot)
}

// Start begins a mouse auto-click session after the native boundary has
// observed the user switch away from the launcher to the intended target.
func (e *Engine) Start() bool {
	e.mu.Lock()
	if e.closed || !e.config.Enabled || e.state != StateArmed || e.config.Mode == ModeKeyboard {
		e.mu.Unlock()
		return false
	}
	ctx, generation, config := e.startLocked()
	snapshot := e.snapshotLocked()
	e.mu.Unlock()
	e.notify(snapshot)
	firstDue := time.Now().Add(config.Interval)
	if !e.emit(generation, config) {
		return false
	}
	go e.outputLoop(ctx, generation, config, firstDue)
	return true
}

func (e *Engine) startLocked() (context.Context, uint64, Config) {
	e.gen++
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.state = StateRunning
	return ctx, e.gen, e.config
}

func (e *Engine) Handle(event PhysicalEvent) {
	e.mu.Lock()
	if event.Kind == EventKey && !event.Down {
		if _, toggle := e.config.ToggleMode(event.Code); toggle {
			delete(e.toggleHeld, event.Code)
			e.mu.Unlock()
			return
		}
	}
	if e.closed || e.state == StateFault {
		e.mu.Unlock()
		return
	}
	config := e.config
	if event.Kind == EventKey && event.Down && SameKey(event.Code, config.StopKey) {
		e.mu.Unlock()
		e.stop(true)
		return
	}
	if event.Kind == EventKey && event.Down {
		if mode, toggle := config.ToggleMode(event.Code); toggle {
			if e.toggleHeld[event.Code] {
				e.mu.Unlock()
				return
			}
			e.toggleHeld[event.Code] = true
			disable := config.Enabled && config.Mode == mode
			e.mu.Unlock()
			if disable {
				e.stop(true)
			} else {
				e.activateMode(mode)
			}
			return
		}
	}
	if !config.Enabled {
		e.mu.Unlock()
		return
	}
	trigger := config.Mode == ModeKeyboard && event.Kind == EventKey && config.IsRepeatKey(event.Code)
	if !trigger {
		e.mu.Unlock()
		return
	}
	key := NormalizeKeyCode(event.Code)
	if event.Down {
		if e.repeatHeld[key] {
			e.mu.Unlock()
			return
		}
		e.repeatHeld[key] = true
	}
	if event.Down && e.state == StateArmed {
		ctx, generation, config := e.startLocked()
		snapshot := e.snapshotLocked()
		e.mu.Unlock()
		e.notify(snapshot)
		firstDue := time.Now().Add(config.Interval)
		if e.emitKey(generation, config, key) {
			go e.outputLoop(ctx, generation, config, firstDue)
		}
		return
	}
	if event.Down && e.state == StateRunning {
		generation, config := e.gen, e.config
		e.mu.Unlock()
		e.emitKey(generation, config, key)
		return
	}
	if !event.Down {
		delete(e.repeatHeld, key)
	}
	if !event.Down && e.state == StateRunning && len(e.repeatHeld) == 0 {
		e.mu.Unlock()
		e.stop(false)
		return
	}
	e.mu.Unlock()
}

func (e *Engine) activateMode(mode Mode) {
	e.stop(false)
	e.mu.Lock()
	if e.closed || e.state == StateFault {
		e.mu.Unlock()
		return
	}
	e.config.Mode = mode
	e.config.Enabled = true
	e.state = StateArmed
	e.lastErr = ""
	snapshot := e.snapshotLocked()
	e.mu.Unlock()
	e.notify(snapshot)
}

func (e *Engine) outputLoop(ctx context.Context, generation uint64, config Config, firstDue time.Time) {
	defer func() {
		if value := recover(); value != nil {
			e.Fail(fmt.Errorf("panic in input output loop: %v", value))
		}
	}()
	// The press edge already emitted synchronously. Use a lightweight,
	// cancellable first interval so a short tap never creates a Windows
	// waitable timer or a blocked OS thread.
	delayDuration := time.Until(firstDue)
	if delayDuration < 0 {
		delayDuration = 0
	}
	delay := time.NewTimer(delayDuration)
	select {
	case <-ctx.Done():
		if !delay.Stop() {
			select {
			case <-delay.C:
			default:
			}
		}
		return
	case <-delay.C:
	}
	// Arm the periodic cadence before emitting the second press. Native
	// injection deliberately holds each press for a short time, so anchoring
	// the cadence here keeps Interval as the press-to-press period instead of
	// accidentally adding that hold duration to every cycle.
	timer, err := newCadence(ctx, config.Interval)
	if err != nil {
		e.Fail(fmt.Errorf("create input cadence: %w", err))
		return
	}
	defer timer.Close()
	if !e.emit(generation, config) {
		return
	}
	for {
		ready, err := timer.Wait()
		if err != nil {
			e.Fail(fmt.Errorf("wait for input cadence: %w", err))
			return
		}
		if !ready {
			return
		}
		if !e.emit(generation, config) {
			return
		}
	}
}

func (e *Engine) emitKey(generation uint64, config Config, key uint32) bool {
	e.emitMu.Lock()
	defer e.emitMu.Unlock()
	e.mu.Lock()
	key = NormalizeKeyCode(key)
	valid := !e.closed && e.state == StateRunning && e.gen == generation && e.repeatHeld[key]
	e.mu.Unlock()
	if !valid {
		return false
	}
	keyConfig := config
	keyConfig.OutputKey = key
	keyConfig.TriggerKey = key
	if err := e.injector.Emit(keyConfig); err != nil {
		if errors.Is(err, errOutputTargetLost) {
			e.targetLost(generation)
			return false
		}
		e.fault(generation, err)
		return false
	}
	e.mu.Lock()
	if e.gen == generation {
		e.count++
	}
	snapshot := e.snapshotLocked()
	e.mu.Unlock()
	e.notify(snapshot)
	return true
}

func (e *Engine) emit(generation uint64, config Config) bool {
	e.emitMu.Lock()
	defer e.emitMu.Unlock()
	e.mu.Lock()
	valid := !e.closed && e.state == StateRunning && e.gen == generation
	repeatKeys := make([]uint32, 0, len(e.repeatHeld))
	if valid && config.Mode == ModeKeyboard {
		for key := range e.repeatHeld {
			repeatKeys = append(repeatKeys, key)
		}
		valid = len(repeatKeys) != 0
	}
	e.mu.Unlock()
	if !valid {
		return false
	}
	emitted := uint64(1)
	if config.Mode == ModeKeyboard {
		emitted = 0
		for _, key := range repeatKeys {
			keyConfig := config
			keyConfig.OutputKey = key
			keyConfig.TriggerKey = key
			if err := e.injector.Emit(keyConfig); err != nil {
				if errors.Is(err, errOutputTargetLost) {
					e.targetLost(generation)
					return false
				}
				e.fault(generation, err)
				return false
			}
			emitted++
		}
	} else if err := e.injector.Emit(config); err != nil {
		if errors.Is(err, errOutputTargetLost) {
			e.targetLost(generation)
			return false
		}
		e.fault(generation, err)
		return false
	}
	e.mu.Lock()
	if e.gen == generation {
		e.count += emitted
	}
	snapshot := e.snapshotLocked()
	e.mu.Unlock()
	e.notify(snapshot)
	return true
}

func (e *Engine) targetLost(generation uint64) {
	e.mu.Lock()
	if e.closed || e.gen != generation {
		e.mu.Unlock()
		return
	}
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	e.gen++
	e.config.Enabled = false
	e.state = StateDisabled
	clear(e.repeatHeld)
	snapshot := e.snapshotLocked()
	e.mu.Unlock()
	e.notify(snapshot)
}

func (e *Engine) stop(disable bool) {
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	release := e.state == StateRunning || e.state == StateStopping
	clear(e.repeatHeld)
	if e.state == StateRunning {
		e.state = StateStopping
	}
	e.gen++
	config := e.config
	e.mu.Unlock()

	if release {
		e.emitMu.Lock()
		_ = e.injector.Release(config)
		e.emitMu.Unlock()
	}

	e.mu.Lock()
	if disable {
		e.config.Enabled = false
		e.state = StateDisabled
	} else if e.config.Enabled {
		e.state = StateArmed
	} else {
		e.state = StateDisabled
	}
	snapshot := e.snapshotLocked()
	e.mu.Unlock()
	e.notify(snapshot)
}

func (e *Engine) fault(generation uint64, err error) {
	e.mu.Lock()
	if e.gen != generation {
		e.mu.Unlock()
		return
	}
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	e.config.Enabled = false
	e.state = StateFault
	e.lastErr = err.Error()
	snapshot := e.snapshotLocked()
	e.mu.Unlock()
	e.notify(snapshot)
}

func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotLocked()
}

// Fail transitions the engine to a disabled fault state. It is used by the
// native boundary when input delivery itself can no longer be trusted (for
// example, when the hook event queue overflows).
func (e *Engine) Fail(err error) {
	if err == nil {
		return
	}
	e.stop(true)
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.state = StateFault
	e.lastErr = err.Error()
	snapshot := e.snapshotLocked()
	e.mu.Unlock()
	e.notify(snapshot)
}

func (e *Engine) Close() {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	e.stop(true)
}

func (e *Engine) snapshotLocked() Snapshot {
	return Snapshot{State: e.state, Config: e.config, Generation: e.gen, OutputCount: e.count, LastError: e.lastErr}
}

func (e *Engine) notify(snapshot Snapshot) {
	if e.onChange != nil {
		func() {
			defer func() { _ = recover() }()
			e.onChange(snapshot)
		}()
	}
}
