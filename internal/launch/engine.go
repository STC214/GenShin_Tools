package launch

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"genshintools/internal/game"
)

var ErrAlreadyRunning = errors.New("game is already running")

type ExistingChecker func(game.Candidate) ([]game.ProcessIdentity, error)

type Engine struct {
	mu        sync.Mutex
	starter   Starter
	checker   ExistingChecker
	onChange  func(Snapshot)
	snapshot  Snapshot
	closed    bool
	launching bool
}

func NewEngine(starter Starter, checker ExistingChecker, onChange func(Snapshot)) (*Engine, error) {
	if starter == nil {
		return nil, errors.New("starter is required")
	}
	if checker == nil {
		checker = game.RunningProcesses
	}
	return &Engine{starter: starter, checker: checker, onChange: onChange}, nil
}

func (e *Engine) Launch(candidate game.Candidate, config Config) error {
	return e.LaunchWithStarter(candidate, config, e.starter)
}

func (e *Engine) LaunchWithStarter(candidate game.Candidate, config Config, starter Starter) error {
	if starter == nil {
		return errors.New("launch starter is required")
	}
	if candidate.Executable == "" || candidate.Root == "" {
		return errors.New("validated game candidate is required")
	}
	if info, err := os.Stat(candidate.Executable); err != nil || info.IsDir() {
		if err == nil {
			err = errors.New("path is a directory")
		}
		return fmt.Errorf("validate game executable: %w", err)
	}
	arguments, err := BuildArguments(config)
	if err != nil {
		return err
	}
	config, _ = config.Normalized()

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return errors.New("launch engine is closed")
	}
	if e.launching || e.snapshot.State == StateStarting || e.snapshot.State == StateRunning {
		e.mu.Unlock()
		return errors.New("a game launch is already active")
	}
	e.launching = true
	e.mu.Unlock()

	running, checkErr := e.checker(candidate)
	if checkErr != nil {
		e.clearLaunchReservation()
		return fmt.Errorf("check existing game process: %w", checkErr)
	}
	if len(running) > 0 {
		e.clearLaunchReservation()
		return fmt.Errorf("%w: PID %d", ErrAlreadyRunning, running[0].PID)
	}

	e.mu.Lock()
	if e.closed {
		e.launching = false
		e.mu.Unlock()
		return errors.New("launch engine was closed while checking existing processes")
	}
	e.launching = false
	e.snapshot.Generation++
	generation := e.snapshot.Generation
	e.snapshot = Snapshot{State: StateStarting, Generation: generation, Arguments: append([]string(nil), arguments...), Executable: candidate.Executable, WorkingDir: candidate.Root, PostBehavior: config.PostBehavior}
	starting := e.snapshotLocked()
	e.mu.Unlock()
	e.notify(starting)

	process, err := starter.Start(Request{Candidate: candidate, Config: config, Arguments: arguments})
	if err != nil {
		e.fail(generation, fmt.Errorf("start game: %w", err))
		return err
	}
	e.mu.Lock()
	if e.closed || e.snapshot.Generation != generation {
		e.mu.Unlock()
		// Deliberately do not terminate a process that has already been created,
		// but still wait in the background so the Windows process handle closes.
		go func() { _, _ = process.Wait() }()
		return errors.New("launcher closed after game process creation")
	}
	e.snapshot.State = StateRunning
	e.snapshot.PID = process.PID()
	e.snapshot.Owned = true
	e.snapshot.StartedAt = time.Now()
	runningSnapshot := e.snapshotLocked()
	e.mu.Unlock()
	e.notify(runningSnapshot)
	go e.wait(process, generation)
	return nil
}

func (e *Engine) clearLaunchReservation() {
	e.mu.Lock()
	e.launching = false
	e.mu.Unlock()
}

func (e *Engine) wait(process Process, generation uint64) {
	exitCode, err := process.Wait()
	e.mu.Lock()
	if e.closed || e.snapshot.Generation != generation {
		e.mu.Unlock()
		return
	}
	e.snapshot.State = StateExited
	e.snapshot.ExitedAt = time.Now()
	e.snapshot.ExitCode = exitCode
	if err != nil {
		e.snapshot.State = StateFailed
		e.snapshot.LastError = err.Error()
	}
	snapshot := e.snapshotLocked()
	e.mu.Unlock()
	e.notify(snapshot)
}

func (e *Engine) fail(generation uint64, err error) {
	e.mu.Lock()
	if e.snapshot.Generation != generation {
		e.mu.Unlock()
		return
	}
	e.snapshot.State = StateFailed
	e.snapshot.LastError = err.Error()
	snapshot := e.snapshotLocked()
	e.mu.Unlock()
	e.notify(snapshot)
}

func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotLocked()
}

// Close detaches launcher observation only. It never kills or closes the game.
func (e *Engine) Close() {
	e.mu.Lock()
	e.closed = true
	e.onChange = nil
	e.mu.Unlock()
}

func (e *Engine) snapshotLocked() Snapshot {
	result := e.snapshot
	result.Arguments = append([]string(nil), e.snapshot.Arguments...)
	return result
}

func (e *Engine) notify(snapshot Snapshot) {
	e.mu.Lock()
	callback := e.onChange
	e.mu.Unlock()
	if callback != nil {
		func() {
			defer func() { _ = recover() }()
			callback(snapshot)
		}()
	}
}
