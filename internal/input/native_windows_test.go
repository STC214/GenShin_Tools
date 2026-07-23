package input

import (
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWinInputLayout(t *testing.T) {
	if size := unsafe.Sizeof(winInput{}); size != 40 {
		t.Fatalf("sizeof(INPUT) = %d, want 40 on amd64", size)
	}
}

func TestKeyboardPairUsesScanCodeMarkerAndKeyUp(t *testing.T) {
	config := DefaultConfig()
	pair, err := inputPair(config)
	if err != nil {
		t.Fatal(err)
	}
	if pair[0].Type != inputKeyboard || pair[1].Type != inputKeyboard {
		t.Fatal("wrong input type")
	}
	for i := range pair {
		if scan := *(*uint16)(unsafe.Pointer(&pair[i].Data[2])); scan == 0 {
			t.Fatal("missing scan code")
		}
		if marker := *(*uintptr)(unsafe.Pointer(&pair[i].Data[16])); marker != injectionMarker {
			t.Fatalf("marker = %#x", marker)
		}
	}
	downFlags := *(*uint32)(unsafe.Pointer(&pair[0].Data[4]))
	upFlags := *(*uint32)(unsafe.Pointer(&pair[1].Data[4]))
	if downFlags&keyeventfScanCode == 0 || downFlags&keyeventfKeyUp != 0 {
		t.Fatalf("down flags = %#x", downFlags)
	}
	if upFlags&keyeventfScanCode == 0 || upFlags&keyeventfKeyUp == 0 {
		t.Fatalf("up flags = %#x", upFlags)
	}
}

func TestMousePairAndDefensiveRelease(t *testing.T) {
	config := DefaultConfig()
	config.Mode = ModeMouseLeft
	pair, err := inputPair(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := *(*uint32)(unsafe.Pointer(&pair[0].Data[12])); got != mouseeventfLeftDown {
		t.Fatalf("down = %#x", got)
	}
	if got := *(*uint32)(unsafe.Pointer(&pair[1].Data[12])); got != mouseeventfLeftUp {
		t.Fatalf("up = %#x", got)
	}
	release, err := releaseInput(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := *(*uint32)(unsafe.Pointer(&release.Data[12])); got != mouseeventfLeftUp {
		t.Fatalf("release = %#x", got)
	}
}

func TestInjectedCallbacksAreIgnored(t *testing.T) {
	n, err := NewNative(nil)
	if err != nil {
		t.Fatal(err)
	}
	activeNative.Store(n)
	defer activeNative.Store(nil)
	key := keyboardHook{VirtualKey: 'A', Flags: llkhfInjected, ExtraInfo: injectionMarker}
	handleKeyboardHook(&key, wMKeyDown)
	mouse := mouseHook{Flags: llmhfInjected, ExtraInfo: injectionMarker}
	handleMouseHook(&mouse, wMLButtonDown)
	if n.head.Load() != 0 {
		t.Fatalf("injected events queued: %d", n.head.Load())
	}
}

func TestNativeHooksStartAndClose(t *testing.T) {
	n, err := NewNative(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Start(); err != nil {
		t.Fatal(err)
	}
	if n.threadID.Load() == 0 {
		t.Fatal("hook thread ID was not published")
	}
	n.Close()
	if activeNative.Load() != nil {
		t.Fatal("active native hook was not cleared")
	}
}

func TestNativeConcurrentStartCloseDoesNotLeakOrHang(t *testing.T) {
	for range 5 {
		n, err := NewNative(nil)
		if err != nil {
			t.Fatal(err)
		}
		finished := make(chan struct{})
		go func() {
			_ = n.Start()
			close(finished)
		}()
		n.Close()
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent Start/Close hung")
		}
		if activeNative.Load() == n {
			t.Fatal("concurrent Start/Close left active hooks")
		}
	}
}

func TestForegroundChangeStopsRunningEngine(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	var foreground atomic.Uintptr
	foreground.Store(1)
	n := &Native{
		engine:      engine,
		monitorStop: make(chan struct{}),
		monitorDone: make(chan struct{}),
		foreground:  func() windows.HWND { return windows.HWND(foreground.Load()) },
	}
	go n.safetyMonitor()
	defer func() {
		close(n.monitorStop)
		<-n.monitorDone
		engine.Close()
	}()
	config := DefaultConfig()
	config.Enabled = true
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	engine.Handle(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})
	deadline := time.Now().Add(time.Second)
	for engine.Snapshot().State != StateRunning && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	n.runTarget.Store(1)
	time.Sleep(150 * time.Millisecond)
	foreground.Store(2)
	deadline = time.Now().Add(time.Second)
	for engine.Snapshot().State != StateDisabled && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if snapshot := engine.Snapshot(); snapshot.State != StateDisabled || snapshot.Config.Enabled {
		t.Fatalf("snapshot after foreground change = %+v", snapshot)
	}
}

func TestMouseAutoClickStartsAfterForegroundLeavesLauncher(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	var foreground atomic.Uintptr
	foreground.Store(100)
	n := &Native{
		engine:      engine,
		monitorStop: make(chan struct{}),
		monitorDone: make(chan struct{}),
		foreground:  func() windows.HWND { return windows.HWND(foreground.Load()) },
	}
	go n.safetyMonitor()
	defer func() {
		close(n.monitorStop)
		<-n.monitorDone
		engine.Close()
	}()
	config := DefaultConfig()
	config.Mode = ModeMouseLeft
	config.Enabled = true
	config.Interval = 5 * time.Millisecond
	if err := n.Configure(config); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if snapshot := engine.Snapshot(); snapshot.State != StateArmed {
		t.Fatalf("mouse mode started over launcher: %+v", snapshot)
	}
	if emits, _ := injector.counts(); emits != 0 {
		t.Fatalf("mouse mode emitted over launcher: %d", emits)
	}
	// A real click or Alt+Tab produces physical events while the foreground is
	// changing. Those events must not replace the launcher origin.
	foreground.Store(200)
	n.updateActivationTargets(StateArmed, engine.Snapshot())
	if origin := n.armTarget.Load(); origin != 100 {
		t.Fatalf("physical switch event replaced arm origin with %d", origin)
	}
	// A transient foreground must not become the click target.
	foreground.Store(300)
	time.Sleep(mouseTargetStableFor / 2)
	foreground.Store(100)
	time.Sleep(150 * time.Millisecond)
	if snapshot := engine.Snapshot(); snapshot.State != StateArmed {
		t.Fatalf("transient foreground started mouse mode: %+v", snapshot)
	}
	if emits, _ := injector.counts(); emits != 0 {
		t.Fatalf("transient foreground produced %d emissions", emits)
	}
	foreground.Store(200)
	deadline := time.Now().Add(time.Second)
	for engine.Snapshot().State != StateRunning && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if snapshot := engine.Snapshot(); snapshot.State != StateRunning {
		t.Fatalf("mouse mode did not start over target: %+v", snapshot)
	}
	if target := n.runTarget.Load(); target != 200 {
		t.Fatalf("mouse run target = %d, want 200", target)
	}
	n.Enable(false)
}
