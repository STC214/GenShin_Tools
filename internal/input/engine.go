package input

import (
	"context"
	"fmt"
	"sync"
)

type Engine struct {
	mu       sync.Mutex
	emitMu   sync.Mutex
	config   Config
	state    State
	gen      uint64
	count    uint64
	lastErr  string
	cancel   context.CancelFunc
	injector Injector
	onChange func(Snapshot)
	closed   bool
}

func NewEngine(injector Injector, onChange func(Snapshot)) (*Engine, error) {
	if injector == nil {
		return nil, fmt.Errorf("injector is required")
	}
	config, _ := DefaultConfig().Normalized()
	return &Engine{config: config, state: StateDisabled, injector: injector, onChange: onChange}, nil
}

func (e *Engine) Configure(config Config) error {
	normalized, err := config.Normalized()
	if err != nil {
		return err
	}
	e.stop(false)
	e.mu.Lock()
	e.config = normalized
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
		e.state = StateArmed
	}
	snapshot := e.snapshotLocked()
	e.mu.Unlock()
	e.notify(snapshot)
}

func (e *Engine) Handle(event PhysicalEvent) {
	e.mu.Lock()
	if e.closed || !e.config.Enabled || e.state == StateFault {
		e.mu.Unlock()
		return
	}
	config := e.config
	if event.Kind == EventKey && event.Down && event.Code == config.StopKey {
		e.mu.Unlock()
		e.stop(true)
		return
	}
	trigger := (config.Mode == ModeKeyboard && event.Kind == EventKey && event.Code == config.TriggerKey) ||
		(config.Mode == ModeMouseLeft && event.Kind == EventMouseLeft) ||
		(config.Mode == ModeMouseRight && event.Kind == EventMouseRight)
	if !trigger {
		e.mu.Unlock()
		return
	}
	if event.Down && e.state == StateArmed {
		e.gen++
		generation := e.gen
		ctx, cancel := context.WithCancel(context.Background())
		e.cancel = cancel
		e.state = StateRunning
		snapshot := e.snapshotLocked()
		e.mu.Unlock()
		e.notify(snapshot)
		go e.outputLoop(ctx, generation, config)
		return
	}
	if !event.Down && e.state == StateRunning {
		e.mu.Unlock()
		e.stop(false)
		return
	}
	e.mu.Unlock()
}

func (e *Engine) outputLoop(ctx context.Context, generation uint64, config Config) {
	defer func() {
		if value := recover(); value != nil {
			e.Fail(fmt.Errorf("panic in input output loop: %v", value))
		}
	}()
	if !e.emit(generation, config) {
		return
	}
	timer, err := newCadence(ctx, config.Interval)
	if err != nil {
		e.Fail(fmt.Errorf("create input cadence: %w", err))
		return
	}
	defer timer.Close()
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

func (e *Engine) emit(generation uint64, config Config) bool {
	e.emitMu.Lock()
	defer e.emitMu.Unlock()
	e.mu.Lock()
	valid := !e.closed && e.state == StateRunning && e.gen == generation
	e.mu.Unlock()
	if !valid {
		return false
	}
	if err := e.injector.Emit(config); err != nil {
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

func (e *Engine) stop(disable bool) {
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	release := e.state == StateRunning || e.state == StateStopping
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
