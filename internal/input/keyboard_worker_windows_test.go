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

func TestKeyboardWorkerTracksOnlyConfiguredDriverTrigger(t *testing.T) {
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

	worker.handlePhysical(EncodeKeyCode('G', false), true)
	worker.handlePhysical(EncodeKeyCode('F', false), true)
	worker.handlePhysical(EncodeKeyCode('G', false), false)
	heldIndex := int(EncodeKeyCode('F', false) & 0x3ff)
	if !worker.held[heldIndex].Load() {
		t.Fatal("unrelated key transition interrupted the configured trigger")
	}
	worker.handlePhysical(EncodeKeyCode('F', false), false)
	if worker.held[heldIndex].Load() {
		t.Fatal("configured trigger remained held after key up")
	}
	close(worker.done)
	worker.wg.Wait()
	diagnostics := worker.diagnostics()
	if diagnostics.ConfiguredKeyEvents != 2 || diagnostics.TriggerDowns != 1 ||
		diagnostics.TriggerUps != 1 || diagnostics.RepeatStarts != 1 ||
		diagnostics.RepeatStops != 1 || diagnostics.LastKey != EncodeKeyCode('F', false) {
		t.Fatalf("unexpected worker diagnostics: %+v", diagnostics)
	}
}

func TestKeyboardWorkerHookKeepsConfiguredKeyIndependent(t *testing.T) {
	key := EncodeKeyCode('F', false)
	worker := &keyboardWorkerRuntime{
		done:               make(chan struct{}),
		gameForegroundTest: func(*keyboardWorkerConfig) bool { return true },
		emitTest:           func(uint32, time.Duration) error { return nil },
	}
	worker.config.Store(&keyboardWorkerConfig{
		enabled:  true,
		keys:     map[uint32]struct{}{key: {}},
		interval: time.Hour,
	})
	f := keyboardHook{VirtualKey: 'F'}
	g := keyboardHook{VirtualKey: 'G'}
	worker.handleKeyboardHookEvent(wMKeyDown, &f)
	worker.handleKeyboardHookEvent(wMKeyDown, &g)
	worker.handleKeyboardHookEvent(wMKeyUp, &g)
	if !worker.held[int(key&0x3ff)].Load() {
		t.Fatal("unrelated hook key changed the configured physical ledger")
	}
	worker.handleKeyboardHookEvent(wMKeyUp, &f)
	if worker.held[int(key&0x3ff)].Load() {
		t.Fatal("configured hook key remained held after its own release")
	}
	close(worker.done)
	worker.wg.Wait()
	if got := worker.releaseChecks.Load(); got != 1 {
		t.Fatalf("hook release count = %d, want 1", got)
	}
}

func TestKeyboardWorkerFirstDownEmitsWithoutLongPressDelay(t *testing.T) {
	key := EncodeKeyCode('F', false)
	emitted := make(chan struct{}, 1)
	worker := &keyboardWorkerRuntime{
		done:               make(chan struct{}),
		gameForegroundTest: func(*keyboardWorkerConfig) bool { return true },
		emitTest: func(uint32, time.Duration) error {
			select {
			case emitted <- struct{}{}:
			default:
			}
			return nil
		},
	}
	worker.config.Store(&keyboardWorkerConfig{
		enabled:  true,
		keys:     map[uint32]struct{}{key: {}},
		interval: time.Hour,
	})
	worker.handlePhysicalDevice(key, true, 4)
	select {
	case <-emitted:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("first physical down waited for a long-press repeat")
	}
	if got := worker.heldDevice[int(key&0x3ff)].Load(); got != 4 {
		t.Fatalf("held output device = %d, want physical device 4", got)
	}
	if got := worker.lastOutputDevice.Load(); got != 1 {
		t.Fatalf("synthetic output device = %d, want reference device 1", got)
	}
	worker.handlePhysical(key, false)
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
	worker.handlePhysical(EncodeKeyCode('F', false), true)
	if worker.held[int(EncodeKeyCode('F', false)&0x3ff)].Load() {
		t.Fatal("trigger became held outside the game")
	}
	diagnostics := worker.diagnostics()
	if diagnostics.ConfiguredKeyEvents != 1 || diagnostics.ForegroundMisses != 1 ||
		diagnostics.TriggerDowns != 0 || diagnostics.RepeatStarts != 0 {
		t.Fatalf("foreground rejection was not diagnosed: %+v", diagnostics)
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
	diagnostics := worker.diagnostics()
	if diagnostics.OutputFailures != 1 || diagnostics.OutputPairs != 0 || diagnostics.RepeatStops != 1 {
		t.Fatalf("driver failure was not diagnosed: %+v", diagnostics)
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
