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

func TestKeyboardHoldStartsOnceAndStopsOnRelease(t *testing.T) {
	injector := &fakeInjector{}
	engine, _ := NewEngine(injector, nil)
	config := DefaultConfig()
	config.Enabled = true
	config.Interval = 10 * time.Millisecond
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.TriggerKey, Down: true})
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.TriggerKey, Down: true})
	time.Sleep(35 * time.Millisecond)
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.TriggerKey, Down: false})
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

func TestStopKeyDisablesAndReleases(t *testing.T) {
	injector := &fakeInjector{}
	engine, _ := NewEngine(injector, nil)
	config := DefaultConfig()
	config.Enabled = true
	config.Interval = 10 * time.Millisecond
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.TriggerKey, Down: true})
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.StopKey, Down: true})
	if snapshot := engine.Snapshot(); snapshot.State != StateDisabled || snapshot.Config.Enabled {
		t.Fatalf("snapshot=%+v", snapshot)
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
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.TriggerKey, Down: true})
	deadline := time.Now().Add(time.Second)
	for engine.Snapshot().State != StateFault && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if snapshot := engine.Snapshot(); snapshot.State != StateFault || snapshot.LastError == "" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestConfigRejectsConflictsAndUnsafeIntervals(t *testing.T) {
	for _, config := range []Config{
		{Mode: ModeKeyboard, TriggerKey: 'A', OutputKey: 'B', StopKey: 'A', Interval: 50 * time.Millisecond},
		{Mode: ModeKeyboard, TriggerKey: 'A', OutputKey: 'B', StopKey: 'C', Interval: time.Millisecond},
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
		engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.TriggerKey, Down: true})
		engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.TriggerKey, Down: true})
		engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.TriggerKey, Down: false})
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
