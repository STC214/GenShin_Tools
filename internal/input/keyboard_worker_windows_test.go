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

func TestKeyboardWorkerHookTriggerUpIsDiagnosticOnly(t *testing.T) {
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
	worker.held[int(key&0x3ff)].Store(true)
	worker.heldDevice[int(key&0x3ff)].Store(4)
	f := keyboardHook{VirtualKey: 'F'}
	g := keyboardHook{VirtualKey: 'G'}
	worker.handleKeyboardHookEvent(wMKeyDown, &f)
	worker.handleKeyboardHookEvent(wMKeyDown, &g)
	worker.handleKeyboardHookEvent(wMKeyUp, &g)
	worker.handleKeyboardHookEvent(wMKeyUp, &f)
	if !worker.held[int(key&0x3ff)].Load() {
		t.Fatal("unmarked hook key-up changed the driver-owned physical ledger")
	}
	worker.held[int(key&0x3ff)].Store(false)
	close(worker.done)
	worker.wg.Wait()
	if got := worker.hookTriggerUpsIgnored.Load(); got != 1 {
		t.Fatalf("ignored hook release count = %d, want 1", got)
	}
}

func TestKeyboardWorkerDriverSuppressesTriggerAndKeepsDeviceOneOutput(t *testing.T) {
	key := EncodeKeyCode('F', false)
	fDown := interceptionKeyboardInput{MakeCode: 0x21}
	fUp := fDown
	fUp.Flags = interceptionKeyUp
	gDown := interceptionKeyboardInput{MakeCode: 0x22}
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
		enabled:    true,
		keys:       map[uint32]struct{}{key: {}},
		strokeKeys: map[uint32]uint32{interceptionStrokeSignature(fDown): key},
		interval:   time.Hour,
	})

	if forward := worker.interceptInterceptionStroke(4, fDown); forward {
		t.Fatal("physical trigger down was forwarded to the game")
	}
	select {
	case <-emitted:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("suppressed physical trigger did not start immediate replacement output")
	}
	if forward := worker.interceptInterceptionStroke(4, gDown); !forward {
		t.Fatal("unrelated driver key was not passed through")
	}
	if !worker.held[int(key&0x3ff)].Load() {
		t.Fatal("unrelated driver key interrupted the trigger")
	}

	worker.handleKeyboardHookEvent(wMKeyUp, &keyboardHook{VirtualKey: 'F'})
	if !worker.held[int(key&0x3ff)].Load() {
		t.Fatal("unmarked hook F-up interrupted the driver-owned trigger")
	}
	if forward := worker.interceptInterceptionStroke(5, fUp); forward {
		t.Fatal("cross-device configured release leaked an orphan edge")
	}
	if !worker.held[int(key&0x3ff)].Load() {
		t.Fatal("cross-device configured release interrupted the trigger")
	}
	if forward := worker.interceptInterceptionStroke(4, fUp); forward {
		t.Fatal("matching physical trigger release was forwarded to the game")
	}
	if worker.held[int(key&0x3ff)].Load() {
		t.Fatal("matching driver/device release did not stop the trigger")
	}
	close(worker.done)
	worker.wg.Wait()

	diagnostics := worker.diagnostics()
	if diagnostics.PhysicalSuppressed != 3 || diagnostics.ReleaseChecks != 2 ||
		diagnostics.CrossDeviceUpsIgnored != 1 || diagnostics.HookTriggerUpsIgnored != 1 ||
		diagnostics.LastDevice != 4 || diagnostics.LastOutputDevice != 1 {
		t.Fatalf("unexpected isolated-driver diagnostics: %+v", diagnostics)
	}
}

func TestKeyboardWorkerDriverPassesTriggerOutsideGame(t *testing.T) {
	key := EncodeKeyCode('F', false)
	fDown := interceptionKeyboardInput{MakeCode: 0x21}
	worker := &keyboardWorkerRuntime{
		done:               make(chan struct{}),
		gameForegroundTest: func(*keyboardWorkerConfig) bool { return false },
	}
	worker.config.Store(&keyboardWorkerConfig{
		enabled:    true,
		keys:       map[uint32]struct{}{key: {}},
		strokeKeys: map[uint32]uint32{interceptionStrokeSignature(fDown): key},
		interval:   time.Millisecond,
	})
	if forward := worker.interceptInterceptionStroke(4, fDown); !forward {
		t.Fatal("configured key was suppressed outside the game")
	}
	if worker.held[int(key&0x3ff)].Load() {
		t.Fatal("configured key became held outside the game")
	}
	close(worker.done)
}

func TestKeyboardWorkerDriverUsesOneForegroundDecisionPerStroke(t *testing.T) {
	key := EncodeKeyCode('F', false)
	fDown := interceptionKeyboardInput{MakeCode: 0x21}
	var foregroundChecks atomic.Int32
	worker := &keyboardWorkerRuntime{
		done: make(chan struct{}),
		gameForegroundTest: func(*keyboardWorkerConfig) bool {
			return foregroundChecks.Add(1) == 1
		},
		emitTest: func(uint32, time.Duration) error { return nil },
	}
	worker.config.Store(&keyboardWorkerConfig{
		enabled:    true,
		keys:       map[uint32]struct{}{key: {}},
		strokeKeys: map[uint32]uint32{interceptionStrokeSignature(fDown): key},
		interval:   time.Hour,
	})
	if forward := worker.interceptInterceptionStroke(4, fDown); forward {
		t.Fatal("one verified foreground decision did not suppress the physical trigger")
	}
	if got := foregroundChecks.Load(); got != 1 {
		t.Fatalf("foreground checked %d times for one captured stroke, want 1", got)
	}
	if !worker.held[int(key&0x3ff)].Load() {
		t.Fatal("foreground changed between duplicate checks and lost the trigger")
	}
	worker.held[int(key&0x3ff)].Store(false)
	close(worker.done)
	worker.wg.Wait()
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
