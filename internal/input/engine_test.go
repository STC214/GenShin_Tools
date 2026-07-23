package input

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeInjector struct {
	mu       sync.Mutex
	emits    int
	releases int
	failAt   int
}

func (f *fakeInjector) Emit(Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emits++
	if f.failAt > 0 && f.emits >= f.failAt {
		return errors.New("injection blocked")
	}
	return nil
}
func (f *fakeInjector) Release(Config) error { f.mu.Lock(); f.releases++; f.mu.Unlock(); return nil }
func (f *fakeInjector) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.emits, f.releases
}

func TestKeyboardRepeatFollowsTheRepeatedKeyAndStopsOnRelease(t *testing.T) {
	injector := &fakeInjector{}
	engine, _ := NewEngine(injector, nil)
	config := DefaultConfig()
	config.Enabled = true
	config.Interval = 10 * time.Millisecond
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})
	time.Sleep(35 * time.Millisecond)
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: false})
	emits, releases := injector.counts()
	if emits < 2 || releases == 0 {
		t.Fatalf("emits=%d releases=%d", emits, releases)
	}
	if engine.Snapshot().State != StateArmed {
		t.Fatalf("state=%s", engine.Snapshot().State)
	}
	before := emits
	time.Sleep(25 * time.Millisecond)
	emits, _ = injector.counts()
	if emits != before {
		t.Fatalf("late output after release: %d -> %d", before, emits)
	}
}

func TestMouseModeWaitsForConfirmedTargetBeforeStarting(t *testing.T) {
	injector := &fakeInjector{}
	engine, _ := NewEngine(injector, nil)
	config := DefaultConfig()
	config.Mode = ModeMouseLeft
	config.Enabled = true
	config.Interval = 5 * time.Millisecond
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if emits, _ := injector.counts(); emits != 0 {
		t.Fatalf("mouse mode emitted before target confirmation: %d", emits)
	}
	if snapshot := engine.Snapshot(); snapshot.State != StateArmed {
		t.Fatalf("mouse mode state before target confirmation = %s, want armed", snapshot.State)
	}
	if !engine.Start() {
		t.Fatal("mouse mode did not start after target confirmation")
	}
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		emits, _ := injector.counts()
		if emits >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("mouse mode did not auto-start: snapshot=%+v emits=%d", engine.Snapshot(), emits)
		}
		time.Sleep(time.Millisecond)
	}
	engine.Enable(false)
}

func TestOneMillisecondKeyboardCadence(t *testing.T) {
	injector := &fakeInjector{}
	engine, _ := NewEngine(injector, nil)
	config := DefaultConfig()
	config.Enabled = true
	config.Interval = time.Millisecond
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		emits, _ := injector.counts()
		if emits >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("1ms cadence produced only %d emissions", emits)
		}
		time.Sleep(time.Millisecond)
	}
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: false})
}

func TestStopKeyDisablesAndReleases(t *testing.T) {
	injector := &fakeInjector{}
	engine, _ := NewEngine(injector, nil)
	config := DefaultConfig()
	config.Enabled = true
	config.Interval = 10 * time.Millisecond
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.StopKey, Down: true})
	if snapshot := engine.Snapshot(); snapshot.State != StateDisabled || snapshot.Config.Enabled {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestIndependentToggleKeysSelectAndDisableTheirOwnModes(t *testing.T) {
	injector := &fakeInjector{}
	engine, _ := NewEngine(injector, nil)
	config := DefaultConfig()
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		key  uint32
		mode Mode
	}{
		{config.KeyboardToggleKey, ModeKeyboard},
		{config.MouseLeftToggleKey, ModeMouseLeft},
		{config.MouseRightToggleKey, ModeMouseRight},
	}
	for _, test := range tests {
		engine.Handle(PhysicalEvent{Kind: EventKey, Code: test.key, Down: true})
		snapshot := engine.Snapshot()
		if !snapshot.Config.Enabled || snapshot.Config.Mode != test.mode || snapshot.State != StateArmed {
			t.Fatalf("toggle %x selected %+v, want enabled armed %s", test.key, snapshot, test.mode)
		}
		engine.Handle(PhysicalEvent{Kind: EventKey, Code: test.key, Down: true})
		if snapshot = engine.Snapshot(); !snapshot.Config.Enabled || snapshot.Config.Mode != test.mode {
			t.Fatalf("held toggle %x changed state: %+v", test.key, snapshot)
		}
		engine.Handle(PhysicalEvent{Kind: EventKey, Code: test.key, Down: false})
		engine.Handle(PhysicalEvent{Kind: EventKey, Code: test.key, Down: true})
		if snapshot = engine.Snapshot(); snapshot.Config.Enabled || snapshot.State != StateDisabled {
			t.Fatalf("second toggle %x did not disable its mode: %+v", test.key, snapshot)
		}
		engine.Handle(PhysicalEvent{Kind: EventKey, Code: test.key, Down: false})
	}
}

func TestToggleAndStopKeysMustAllBeDistinct(t *testing.T) {
	config := DefaultConfig()
	config.MouseRightToggleKey = config.MouseLeftToggleKey
	if _, err := config.Normalized(); err == nil {
		t.Fatal("accepted duplicate mouse toggle keys")
	}
	config = DefaultConfig()
	config.OutputKey = config.KeyboardToggleKey
	if _, err := config.Normalized(); err == nil {
		t.Fatal("accepted repeat key matching keyboard toggle")
	}
}

func TestPhysicalNavigationAndKeypadIdentitiesRemainDistinct(t *testing.T) {
	pageUp := EncodeKeyCode(0x21, true)
	num9 := EncodeKeyCode(0x21, false)
	if SameKey(pageUp, num9) {
		t.Fatal("Page Up and keypad 9 collapsed to the same physical key")
	}
	if got := NormalizeKeyCode(0x21); got != pageUp {
		t.Fatalf("legacy Page Up normalized to %#x, want %#x", got, pageUp)
	}
	if got := NormalizeKeyCode(num9); got != num9 {
		t.Fatalf("keypad 9 normalized to %#x, want %#x", got, num9)
	}
	if num9 != EncodeKeyCode(0x69, false) {
		t.Fatalf("Num Lock changed keypad 9 identity: off=%#x on=%#x", num9, EncodeKeyCode(0x69, false))
	}
}

func TestInjectionFailureEntersFault(t *testing.T) {
	injector := &fakeInjector{failAt: 1}
	engine, _ := NewEngine(injector, nil)
	config := DefaultConfig()
	config.Enabled = true
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})
	deadline := time.Now().Add(time.Second)
	for engine.Snapshot().State != StateFault && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if snapshot := engine.Snapshot(); snapshot.State != StateFault || snapshot.LastError == "" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestConfigAcceptsOneMillisecondAndRejectsUnsafeIntervals(t *testing.T) {
	valid := Config{Mode: ModeKeyboard, OutputKey: 'B', StopKey: 'C', Interval: time.Millisecond}
	if normalized, err := valid.Normalized(); err != nil || normalized.IntervalMS != 1 {
		t.Fatalf("one millisecond interval rejected: normalized=%+v err=%v", normalized, err)
	}
	for _, config := range []Config{
		{Mode: ModeKeyboard, OutputKey: 'B', StopKey: 'B', Interval: 50 * time.Millisecond},
		{Mode: ModeKeyboard, OutputKey: 'B', StopKey: 'C', Interval: 500 * time.Microsecond},
		{Mode: ModeKeyboard, OutputKey: 'B', StopKey: 'C', Interval: 5001 * time.Millisecond},
	} {
		if _, err := config.Normalized(); err == nil {
			t.Fatalf("accepted %+v", config)
		}
	}
}

func TestRapidTriggerAndEnableDisableStress(t *testing.T) {
	injector := &fakeInjector{}
	engine, _ := NewEngine(injector, nil)
	config := DefaultConfig()
	config.Enabled = true
	config.Interval = 10 * time.Millisecond
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})
		engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})
		engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: false})
	}
	for i := 0; i < 200; i++ {
		engine.Enable(true)
		engine.Enable(false)
	}
	if snapshot := engine.Snapshot(); snapshot.State != StateDisabled || snapshot.Config.Enabled {
		t.Fatalf("snapshot after stress = %+v", snapshot)
	}
	emits, releases := injector.counts()
	time.Sleep(30 * time.Millisecond)
	after, _ := injector.counts()
	if after != emits {
		t.Fatalf("late output after stress: %d -> %d", emits, after)
	}
	if releases != 1000 {
		t.Fatalf("defensive releases = %d, want 1000", releases)
	}
}
