package input

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRepeatKeyListJSONAddDeleteAndLegacyMigration(t *testing.T) {
	keys := NewKeyList('F', 'G')
	if !keys.Delete(0) || !keys.Append('H') || keys.Len() != 2 || !SameKey(keys.At(0), 'G') || !SameKey(keys.At(1), 'H') {
		t.Fatalf("key-list mutation failed: %v", keys.Slice())
	}
	data, err := json.Marshal(keys)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `[71,72]` {
		t.Fatalf("key-list JSON = %s", data)
	}
	var decoded KeyList
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != keys {
		t.Fatalf("decoded key list = %v, want %v", decoded.Slice(), keys.Slice())
	}
	legacy := DefaultConfig()
	legacy.RepeatKeys = KeyList{}
	legacy.OutputKey = 'B'
	normalized, err := legacy.Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if normalized.RepeatKeys.Len() != 1 || !SameKey(normalized.RepeatKeys.At(0), 'B') {
		t.Fatalf("legacy output key did not migrate: %+v", normalized)
	}
}

type fakeInjector struct {
	mu       sync.Mutex
	emits    int
	releases int
	failAt   int
	failErr  error
	keys     []uint32
}

func (f *fakeInjector) Emit(config Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emits++
	f.keys = append(f.keys, config.OutputKey)
	if f.failAt > 0 && f.emits >= f.failAt {
		if f.failErr != nil {
			return f.failErr
		}
		return errors.New("injection blocked")
	}
	return nil
}
func (f *fakeInjector) emittedKeys() []uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint32(nil), f.keys...)
}
func (f *fakeInjector) Release(Config) error { f.mu.Lock(); f.releases++; f.mu.Unlock(); return nil }

func TestOutputTargetLossDisablesWithoutFault(t *testing.T) {
	injector := &fakeInjector{failAt: 1, failErr: errOutputTargetLost}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	config := DefaultConfig()
	config.Enabled = true
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})
	snapshot := engine.Snapshot()
	if snapshot.State != StateDisabled || snapshot.Config.Enabled || snapshot.LastError != "" {
		t.Fatalf("target loss became a fault instead of a clean stop: %+v", snapshot)
	}
}
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

func TestShortTapEmitsImmediatelyBeforeKeyUp(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Enabled = true
	config.Interval = 5 * time.Second
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: false})
	emits, _ := injector.counts()
	if emits != 1 {
		t.Fatalf("short tap emitted %d pairs, want exactly one immediate pair", emits)
	}
	if snapshot := engine.Snapshot(); snapshot.State != StateArmed || snapshot.OutputCount != 1 {
		t.Fatalf("short-tap snapshot = %+v", snapshot)
	}
}

func TestUnrelatedKeysDoNotInterruptKeyboardRepeatOrMouseClick(t *testing.T) {
	for _, mode := range []Mode{ModeKeyboard, ModeMouseLeft, ModeMouseRight} {
		t.Run(mode.String(), func(t *testing.T) {
			injector := &fakeInjector{}
			engine, err := NewEngine(injector, nil)
			if err != nil {
				t.Fatal(err)
			}
			config := DefaultConfig()
			config.Mode = mode
			config.Enabled = true
			config.Interval = 5 * time.Millisecond
			if err := engine.Configure(config); err != nil {
				t.Fatal(err)
			}
			if mode == ModeKeyboard {
				engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})
			} else if !engine.Start() {
				t.Fatal("mouse mode did not start")
			}
			deadline := time.Now().Add(250 * time.Millisecond)
			for {
				if emits, _ := injector.counts(); emits >= 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("mode did not emit: %+v", engine.Snapshot())
				}
				time.Sleep(time.Millisecond)
			}
			before, _ := injector.counts()
			engine.Handle(PhysicalEvent{Kind: EventKey, Code: 'A', Down: true})
			engine.Handle(PhysicalEvent{Kind: EventKey, Code: 'A', Down: false})
			time.Sleep(20 * time.Millisecond)
			after, _ := injector.counts()
			if snapshot := engine.Snapshot(); snapshot.State != StateRunning || after <= before {
				t.Fatalf("unrelated key interrupted %s: before=%d after=%d snapshot=%+v", mode, before, after, snapshot)
			}
			if mode == ModeKeyboard {
				engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: false})
			} else {
				engine.Enable(false)
			}
		})
	}
}

func TestMultipleRepeatKeysRunIndependently(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Enabled = true
	config.Interval = 5 * time.Millisecond
	config.RepeatKeys = NewKeyList('F', 'G')
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: 'F', Down: true})
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: 'A', Down: true})
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: 'A', Down: false})
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: 'G', Down: true})
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		seenF, seenG := false, false
		for _, key := range injector.emittedKeys() {
			seenF = seenF || SameKey(key, 'F')
			seenG = seenG || SameKey(key, 'G')
		}
		if seenF && seenG {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("both held repeat keys were not emitted: %v", injector.emittedKeys())
		}
		time.Sleep(time.Millisecond)
	}
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: 'F', Down: false})
	before := len(injector.emittedKeys())
	time.Sleep(20 * time.Millisecond)
	afterKeys := injector.emittedKeys()
	if snapshot := engine.Snapshot(); snapshot.State != StateRunning || len(afterKeys) <= before {
		t.Fatalf("releasing F interrupted held G: before=%d after=%d snapshot=%+v", before, len(afterKeys), snapshot)
	}
	for _, key := range afterKeys[before:] {
		if !SameKey(key, 'G') {
			t.Fatalf("released F was still emitted: %v", afterKeys[before:])
		}
	}
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: 'G', Down: false})
	if snapshot := engine.Snapshot(); snapshot.State != StateArmed {
		t.Fatalf("last repeat-key release did not arm engine: %+v", snapshot)
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
	config.RepeatKeys = NewKeyList(config.KeyboardToggleKey)
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
