package input

import (
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

func TestKeyboardWorkerSuppressesOnlyConfiguredGameTrigger(t *testing.T) {
	worker := &keyboardWorkerRuntime{
		done:               make(chan struct{}),
		gameForegroundTest: func(*keyboardWorkerConfig) bool { return true },
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
	if !worker.held['F'].Load() {
		t.Fatal("configured trigger was not marked held")
	}
	if !worker.handlePhysical(EncodeKeyCode('F', false), false) {
		t.Fatal("configured trigger up was not suppressed")
	}
	if worker.held['F'].Load() {
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
	if worker.held['F'].Load() {
		t.Fatal("trigger became held outside the game")
	}
}
