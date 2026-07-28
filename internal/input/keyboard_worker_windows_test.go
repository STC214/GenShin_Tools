package input

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSameKeyboardWorkerRequest(t *testing.T) {
	left := &keyboardWorkerRequest{
		Enabled:       true,
		RepeatKeys:    []uint32{EncodeKeyCode('F', false), EncodeKeyCode('G', false)},
		IntervalMS:    5,
		GameProcesses: []uint32{10, 20},
	}
	right := &keyboardWorkerRequest{
		Enabled:       true,
		RepeatKeys:    append([]uint32(nil), left.RepeatKeys...),
		IntervalMS:    5,
		GameProcesses: []uint32{20, 10},
	}
	if !sameKeyboardWorkerRequest(left, right) {
		t.Fatal("equivalent worker requests did not compare equal")
	}
	right.RepeatKeys[1] = EncodeKeyCode('H', false)
	if sameKeyboardWorkerRequest(left, right) {
		t.Fatal("different repeat keys compared equal")
	}
}

func TestKeyboardWorkerDoesNotStartBeforeGameProcess(t *testing.T) {
	controller := newKeyboardWorkerController()
	defer controller.Close()
	err := controller.Configure(keyboardWorkerRequest{
		Enabled:    true,
		RepeatKeys: []uint32{EncodeKeyCode('F', false)},
		IntervalMS: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if controller.Active() || controller.PID() != 0 {
		t.Fatalf("worker started without a verified game process: active=%t pid=%d", controller.Active(), controller.PID())
	}
}

func TestKeyboardWorkerDoesNotStartWhileFeatureDisabled(t *testing.T) {
	controller := newKeyboardWorkerController()
	defer controller.Close()
	err := controller.Configure(keyboardWorkerRequest{
		Enabled:       false,
		RepeatKeys:    []uint32{EncodeKeyCode('F', false)},
		IntervalMS:    5,
		GameProcesses: []uint32{42},
	})
	if err != nil {
		t.Fatal(err)
	}
	if controller.Active() || controller.PID() != 0 {
		t.Fatalf("disabled worker installed a hook: active=%t pid=%d", controller.Active(), controller.PID())
	}
}

func TestKeyboardWorkerSuppressesOnlyConfiguredGameTrigger(t *testing.T) {
	worker := &keyboardWorkerRuntime{
		done:               make(chan struct{}),
		gameForegroundTest: func(*keyboardWorkerConfig) bool { return true },
		emitTest:           func(uint32, time.Duration) error { return nil },
	}
	worker.config.Store(&keyboardWorkerConfig{
		enabled:  true,
		keys:     map[uint32]struct{}{EncodeKeyCode('F', false): {}},
		interval: time.Hour,
	})

	if worker.handlePhysical(EncodeKeyCode('G', false), true) {
		t.Fatal("unrelated key was suppressed")
	}
	if !worker.handlePhysical(EncodeKeyCode('F', false), true) {
		t.Fatal("configured trigger down was not suppressed")
	}
	heldIndex := int(EncodeKeyCode('F', false) & 0x3ff)
	if !worker.held[heldIndex].Load() {
		t.Fatal("configured trigger was not marked held")
	}
	if !worker.handlePhysical(EncodeKeyCode('F', false), false) {
		t.Fatal("configured trigger up was not suppressed")
	}
	if worker.held[heldIndex].Load() {
		t.Fatal("configured trigger remained held after key up")
	}
	close(worker.done)
	worker.wg.Wait()
}

func TestKeyboardWorkerDoesNotActivateOutsideGame(t *testing.T) {
	worker := &keyboardWorkerRuntime{
		done:               make(chan struct{}),
		gameForegroundTest: func(*keyboardWorkerConfig) bool { return false },
	}
	worker.config.Store(&keyboardWorkerConfig{
		enabled:  true,
		keys:     map[uint32]struct{}{EncodeKeyCode('F', false): {}},
		interval: time.Millisecond,
	})
	if worker.handlePhysical(EncodeKeyCode('F', false), true) {
		t.Fatal("trigger was suppressed outside the game")
	}
	if worker.held[int(EncodeKeyCode('F', false)&0x3ff)].Load() {
		t.Fatal("trigger became held outside the game")
	}
}

func TestKeyboardWorkerRecordsInterceptionOutputFailure(t *testing.T) {
	driverError := errors.New("driver write failed")
	key := EncodeKeyCode('F', false)
	worker := &keyboardWorkerRuntime{
		done:               make(chan struct{}),
		gameForegroundTest: func(*keyboardWorkerConfig) bool { return true },
		emitTest:           func(uint32, time.Duration) error { return driverError },
	}
	worker.config.Store(&keyboardWorkerConfig{
		enabled:  true,
		keys:     map[uint32]struct{}{key: {}},
		interval: time.Millisecond,
	})
	worker.held[int(key&0x3ff)].Store(true)
	generation := worker.generation[int(key&0x3ff)].Add(1)
	worker.wg.Add(1)
	worker.repeatKey(key, generation)

	fault := worker.runtimeFault()
	if fault == nil || !errors.Is(fault, driverError) {
		t.Fatalf("runtime fault = %v, want wrapped driver error", fault)
	}
	if !strings.Contains(fault.Error(), "Interception output") {
		t.Fatalf("runtime fault lacks actionable context: %v", fault)
	}
}

func TestKeyboardWorkerGenerationPreventsOldLoopJoiningNewPress(t *testing.T) {
	key := EncodeKeyCode('F', false)
	index := int(key & 0x3ff)
	var emitted atomic.Int32
	worker := &keyboardWorkerRuntime{
		done:               make(chan struct{}),
		gameForegroundTest: func(*keyboardWorkerConfig) bool { return true },
		emitTest: func(uint32, time.Duration) error {
			emitted.Add(1)
			return nil
		},
	}
	worker.config.Store(&keyboardWorkerConfig{
		enabled:  true,
		keys:     map[uint32]struct{}{key: {}},
		interval: time.Millisecond,
	})

	worker.held[index].Store(true)
	oldGeneration := worker.generation[index].Add(1)
	worker.generation[index].Add(1)
	worker.wg.Add(1)
	worker.repeatKey(key, oldGeneration)
	if emitted.Load() != 0 {
		t.Fatalf("stale repeat generation emitted %d event(s)", emitted.Load())
	}
}
