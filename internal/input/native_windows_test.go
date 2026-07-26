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

func TestRawInputLayoutsAndPhysicalKeyboardTranslation(t *testing.T) {
	if size := unsafe.Sizeof(rawInputDevice{}); size != 16 {
		t.Fatalf("sizeof(RAWINPUTDEVICE) = %d, want 16", size)
	}
	if size := unsafe.Sizeof(rawInputHeader{}); size != 24 {
		t.Fatalf("sizeof(RAWINPUTHEADER) = %d, want 24", size)
	}
	if size := unsafe.Sizeof(rawKeyboard{}); size != 16 {
		t.Fatalf("sizeof(RAWKEYBOARD) = %d, want 16", size)
	}
	n := &Native{wake: make(chan struct{}, 1)}
	n.handleRawKeyboard(rawKeyboard{VirtualKey: 'F'})
	n.handleRawKeyboard(rawKeyboard{VirtualKey: 'F', Flags: riKeyBreak})
	n.handleRawKeyboard(rawKeyboard{VirtualKey: 0x21, Flags: riKeyE0})
	if n.head.Load() != 3 {
		t.Fatalf("raw keyboard queued %d events, want 3", n.head.Load())
	}
	if first, second := n.events[0], n.events[1]; !first.Down || second.Down || !SameKey(first.Code, 'F') || !SameKey(second.Code, 'F') {
		t.Fatalf("raw F translation = %+v %+v", first, second)
	}
	if got := n.events[2].Code; got != EncodeKeyCode(0x21, true) {
		t.Fatalf("raw extended Page Up = %#x", got)
	}
}

func TestNativeQueueSupportsConcurrentHookAndRawInputProducers(t *testing.T) {
	n := &Native{wake: make(chan struct{}, 1)}
	const producers = 8
	const eventsPerProducer = nativeQueueSize / producers
	start := make(chan struct{})
	done := make(chan struct{}, producers)
	for producer := uint32(1); producer <= producers; producer++ {
		go func(code uint32) {
			<-start
			for range eventsPerProducer {
				n.enqueue(PhysicalEvent{Kind: EventKey, Code: code, Down: true})
			}
			done <- struct{}{}
		}(producer)
	}
	close(start)
	for range producers {
		<-done
	}
	if n.overflow.Load() {
		t.Fatal("exactly one queue capacity overflowed")
	}
	if got := n.head.Load(); got != nativeQueueSize {
		t.Fatalf("queued %d events, want %d", got, nativeQueueSize)
	}
	counts := make(map[uint32]int, producers)
	for index := range nativeQueueSize {
		counts[n.events[index].Code]++
	}
	for producer := uint32(1); producer <= producers; producer++ {
		if got := counts[producer]; got != eventsPerProducer {
			t.Fatalf("producer %d retained %d events, want %d", producer, got, eventsPerProducer)
		}
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

func TestKeyboardInputPreservesExtendedNavigationAndKeypadIdentity(t *testing.T) {
	pageUp, err := keyboardInput(EncodeKeyCode(0x21, true), false)
	if err != nil {
		t.Fatal(err)
	}
	num9, err := keyboardInput(EncodeKeyCode(0x21, false), false)
	if err != nil {
		t.Fatal(err)
	}
	pageUpFlags := *(*uint32)(unsafe.Pointer(&pageUp.Data[4]))
	num9Flags := *(*uint32)(unsafe.Pointer(&num9.Data[4]))
	if pageUpFlags&keyeventfExtendedKey == 0 {
		t.Fatalf("Page Up flags = %#x, missing extended-key flag", pageUpFlags)
	}
	if num9Flags&keyeventfExtendedKey != 0 {
		t.Fatalf("keypad 9 flags = %#x, unexpectedly extended", num9Flags)
	}
	numEnter, err := keyboardInput(EncodeKeyCode(0x0d, true), false)
	if err != nil {
		t.Fatal(err)
	}
	if flags := *(*uint32)(unsafe.Pointer(&numEnter.Data[4])); flags&keyeventfExtendedKey == 0 {
		t.Fatalf("keypad Enter flags = %#x, missing extended-key flag", flags)
	}
}

func TestKeyboardHookRecordsPhysicalExtendedBit(t *testing.T) {
	n, err := NewNative(nil)
	if err != nil {
		t.Fatal(err)
	}
	activeNative.Store(n)
	defer activeNative.Store(nil)
	handleKeyboardHook(&keyboardHook{VirtualKey: 0x21, Flags: llkhfExtended}, wMKeyDown)
	handleKeyboardHook(&keyboardHook{VirtualKey: 0x21}, wMKeyDown)
	if n.head.Load() != 2 {
		t.Fatalf("queued %d events, want 2", n.head.Load())
	}
	if got := n.events[0].Code; got != EncodeKeyCode(0x21, true) {
		t.Fatalf("Page Up code = %#x", got)
	}
	if got := n.events[1].Code; got != EncodeKeyCode(0x21, false) {
		t.Fatalf("keypad 9 code = %#x", got)
	}
}

func TestCaptureModeObservesButDoesNotTriggerShortcut(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	var observed atomic.Uint32
	n := &Native{
		engine: engine,
		wake:   make(chan struct{}, 1),
		foreground: func() windows.HWND {
			return 0
		},
		observer: func(PhysicalEvent) { observed.Add(1) },
	}
	n.SetCaptureMode(true)
	n.enqueue(PhysicalEvent{Kind: EventKey, Code: config.MouseLeftToggleKey, Down: true})
	n.drain()
	if observed.Load() != 1 {
		t.Fatalf("capture observer calls = %d, want 1", observed.Load())
	}
	if !n.capturing.Load() {
		t.Fatal("capture mode ended before the recorded key was released")
	}
	if snapshot := engine.Snapshot(); snapshot.Config.Enabled || snapshot.State != StateDisabled {
		t.Fatalf("captured shortcut reached engine: %+v", snapshot)
	}
	// Simulate keyboard autorepeat after the UI has accepted the first down.
	// Native capture must still own the key until its matching up event.
	n.enqueue(PhysicalEvent{Kind: EventKey, Code: config.MouseLeftToggleKey, Down: true})
	n.drain()
	if snapshot := engine.Snapshot(); snapshot.Config.Enabled || snapshot.State != StateDisabled {
		t.Fatalf("held recorded shortcut reached engine: %+v", snapshot)
	}
	n.enqueue(PhysicalEvent{Kind: EventKey, Code: config.MouseLeftToggleKey, Down: false})
	n.drain()
	if n.capturing.Load() || n.captureKey.Load() != 0 {
		t.Fatalf("capture remained active after key-up: capturing=%t key=%#x", n.capturing.Load(), n.captureKey.Load())
	}
	n.enqueue(PhysicalEvent{Kind: EventKey, Code: config.MouseLeftToggleKey, Down: true})
	n.drain()
	if snapshot := engine.Snapshot(); !snapshot.Config.Enabled || snapshot.Config.Mode != ModeMouseLeft {
		t.Fatalf("shortcut did not work after capture completed: %+v", snapshot)
	}
}

func TestCaptureModeSuppressesGlobalStopFromHookAndPolling(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Enabled = true
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	pressed := make(map[uint32]bool)
	n := &Native{
		engine:     engine,
		wake:       make(chan struct{}, 1),
		foreground: func() windows.HWND { return 0 },
		keyDown:    func(code uint32) bool { return pressed[NormalizeKeyCode(code)] },
	}
	states := make(map[uint32]bool)
	n.pollKeyboardOnce(states)
	n.SetCaptureMode(true)

	pressed[config.StopKey] = true
	n.pollKeyboardOnce(states)
	n.enqueue(PhysicalEvent{Kind: EventKey, Code: config.StopKey, Down: true})
	n.drain()
	if snapshot := engine.Snapshot(); !snapshot.Config.Enabled || snapshot.State != StateArmed {
		t.Fatalf("recorded global stop reached engine: %+v", snapshot)
	}

	n.enqueue(PhysicalEvent{Kind: EventKey, Code: config.StopKey, Down: false})
	n.drain()
	pressed[config.StopKey] = false
	n.pollKeyboardOnce(states)
	if n.capturing.Load() {
		t.Fatal("global-stop recording did not finish on key-up")
	}
	if snapshot := engine.Snapshot(); !snapshot.Config.Enabled || snapshot.State != StateArmed {
		t.Fatalf("global stop fired while recording completed: %+v", snapshot)
	}
}

func TestCaptureModeRecordsArbitraryKeyThroughPollingFallback(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	pressed := make(map[uint32]bool)
	var observed atomic.Uint32
	n := &Native{
		engine:     engine,
		foreground: func() windows.HWND { return 0 },
		keyDown:    func(code uint32) bool { return pressed[NormalizeKeyCode(code)] },
		observer: func(event PhysicalEvent) {
			if event.Kind == EventKey && event.Down && SameKey(event.Code, 'A') {
				observed.Add(1)
			}
		},
	}
	states := make(map[uint32]bool)
	n.SetCaptureMode(true)
	n.pollKeyboardOnce(states)
	pressed[NormalizeKeyCode('A')] = true
	n.pollKeyboardOnce(states)
	if observed.Load() != 1 || n.captureKey.Load() != NormalizeKeyCode('A') {
		t.Fatalf("polling capture did not record A: observed=%d key=%#x", observed.Load(), n.captureKey.Load())
	}
	pressed[NormalizeKeyCode('A')] = false
	n.pollKeyboardOnce(states)
	if n.capturing.Load() {
		t.Fatal("polling capture did not finish on key-up")
	}
}

func TestCaptureModeRecordsKeyDownBeforeFirstPollingScan(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	pressed := map[uint32]bool{NormalizeKeyCode('A'): true}
	var observed atomic.Uint32
	n := &Native{
		engine:  engine,
		keyDown: func(code uint32) bool { return pressed[NormalizeKeyCode(code)] },
		observer: func(event PhysicalEvent) {
			if event.Kind == EventKey && event.Down && SameKey(event.Code, 'A') {
				observed.Add(1)
			}
		},
	}
	states := make(map[uint32]bool)
	n.SetCaptureMode(true)
	n.pollKeyboardOnce(states)
	if observed.Load() != 1 || n.captureKey.Load() != NormalizeKeyCode('A') {
		t.Fatalf("first polling scan lost A: observed=%d key=%#x", observed.Load(), n.captureKey.Load())
	}

	pressed[NormalizeKeyCode('A')] = false
	n.pollKeyboardOnce(states)
	if n.capturing.Load() {
		t.Fatal("first-scan capture did not finish on key-up")
	}
}

func TestCancellingCaptureImmediatelyRestoresShortcuts(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	n := &Native{
		engine:     engine,
		wake:       make(chan struct{}, 1),
		foreground: func() windows.HWND { return 0 },
	}
	n.SetCaptureMode(true)
	n.SetCaptureMode(false)
	n.enqueue(PhysicalEvent{Kind: EventKey, Code: config.KeyboardToggleKey, Down: true})
	n.drain()
	if snapshot := engine.Snapshot(); !snapshot.Config.Enabled || snapshot.Config.Mode != ModeKeyboard {
		t.Fatalf("shortcut remained suppressed after capture cancellation: %+v", snapshot)
	}
}

func TestKeyboardPollingFallbackDrivesToggleHoldAndRelease(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Interval = 5 * time.Millisecond
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	pressed := make(map[uint32]bool)
	n := &Native{
		engine:     engine,
		foreground: func() windows.HWND { return 0 },
		keyDown:    func(code uint32) bool { return pressed[NormalizeKeyCode(code)] },
	}
	states := make(map[uint32]bool)
	n.pollKeyboardOnce(states) // establish the initial all-up state

	pressed[config.KeyboardToggleKey] = true
	n.pollKeyboardOnce(states)
	if snapshot := engine.Snapshot(); !snapshot.Config.Enabled || snapshot.Config.Mode != ModeKeyboard || snapshot.State != StateArmed {
		t.Fatalf("poll toggle snapshot = %+v", snapshot)
	}
	pressed[config.KeyboardToggleKey] = false
	n.pollKeyboardOnce(states)

	pressed[config.OutputKey] = true
	n.pollKeyboardOnce(states)
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		if emits, _ := injector.counts(); emits >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("polled hold produced no repeated output: snapshot=%+v", engine.Snapshot())
		}
		time.Sleep(time.Millisecond)
	}
	pressed[config.OutputKey] = false
	n.pollKeyboardOnce(states)
	if snapshot := engine.Snapshot(); snapshot.State != StateArmed || !snapshot.Config.Enabled {
		t.Fatalf("polled release snapshot = %+v", snapshot)
	}
	before, _ := injector.counts()
	time.Sleep(20 * time.Millisecond)
	after, _ := injector.counts()
	if after != before {
		t.Fatalf("polled release left late output: %d -> %d", before, after)
	}
}

func TestHookConfirmedRepeatHoldOutranksInjectedPollingUp(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Enabled = true
	config.Interval = 5 * time.Millisecond
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	pressed := map[uint32]bool{config.OutputKey: true}
	n := &Native{
		engine:     engine,
		foreground: func() windows.HWND { return 0 },
		keyDown:    func(code uint32) bool { return pressed[NormalizeKeyCode(code)] },
	}
	states := make(map[uint32]bool)
	n.pollKeyboardOnce(states)
	n.processPhysicalEvent(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})

	// SendInput emits an up after every generated key press. On affected game
	// input paths GetAsyncKeyState exposes that injected up even though the
	// user is still physically holding the trigger.
	pressed[config.OutputKey] = false
	n.pollKeyboardOnce(states)
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		if snapshot := engine.Snapshot(); snapshot.State != StateRunning || !snapshot.Config.Enabled {
			t.Fatalf("polling up cancelled hook-confirmed hold: %+v", snapshot)
		}
		if emits, _ := injector.counts(); emits >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("repeat did not continue after injected polling up: %+v", engine.Snapshot())
		}
		time.Sleep(time.Millisecond)
	}

	n.processPhysicalEvent(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: false})
	if snapshot := engine.Snapshot(); snapshot.State != StateArmed || !snapshot.Config.Enabled {
		t.Fatalf("real hook up did not stop repeat: %+v", snapshot)
	}
}

func TestHookOwnershipClearsWhenInputSessionStopsOrReconfigures(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := &Native{engine: engine, foreground: func() windows.HWND { return 0 }}
	config := DefaultConfig()
	config.Enabled = true
	if err := n.Configure(config); err != nil {
		t.Fatal(err)
	}
	n.processPhysicalEvent(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})
	if !n.hookDown[config.OutputKey] {
		t.Fatal("repeat key was not hook-owned")
	}
	n.Enable(false)
	if len(n.hookDown) != 0 {
		t.Fatalf("disabled session retained hook state: %#v", n.hookDown)
	}

	n.processPhysicalEvent(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})
	if !n.hookDown[config.OutputKey] {
		t.Fatal("repeat key was not tracked before reconfigure")
	}
	if err := n.Configure(config); err != nil {
		t.Fatal(err)
	}
	if len(n.hookDown) != 0 {
		t.Fatalf("reconfigured session retained hook state: %#v", n.hookDown)
	}
}

func TestKeyboardPollingFallbackAndHookDoNotDoubleToggle(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	pressed := make(map[uint32]bool)
	n := &Native{
		engine:     engine,
		foreground: func() windows.HWND { return 0 },
		keyDown:    func(code uint32) bool { return pressed[NormalizeKeyCode(code)] },
	}
	states := make(map[uint32]bool)
	n.pollKeyboardOnce(states)
	n.processPhysicalEvent(PhysicalEvent{Kind: EventKey, Code: config.KeyboardToggleKey, Down: true})
	pressed[config.KeyboardToggleKey] = true
	n.pollKeyboardOnce(states)
	if snapshot := engine.Snapshot(); !snapshot.Config.Enabled || snapshot.Config.Mode != ModeKeyboard {
		t.Fatalf("hook plus poll double-toggled feature: %+v", snapshot)
	}
	n.processPhysicalEvent(PhysicalEvent{Kind: EventKey, Code: config.KeyboardToggleKey, Down: false})
	pressed[config.KeyboardToggleKey] = false
	n.pollKeyboardOnce(states)
}

func TestKeyboardPollingFallbackStartsMouseModeInExternalForeground(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Interval = 5 * time.Millisecond
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	pressed := make(map[uint32]bool)
	n := &Native{
		engine:     engine,
		foreground: func() windows.HWND { return windows.HWND(0x1234) },
		keyDown:    func(code uint32) bool { return pressed[NormalizeKeyCode(code)] },
	}
	states := make(map[uint32]bool)
	n.pollKeyboardOnce(states)
	pressed[config.MouseRightToggleKey] = true
	n.pollKeyboardOnce(states)
	defer engine.Close()
	if snapshot := engine.Snapshot(); snapshot.State != StateRunning || snapshot.Config.Mode != ModeMouseRight {
		t.Fatalf("polled mouse toggle snapshot = %+v", snapshot)
	}
	if target := n.runTarget.Load(); target != 0x1234 {
		t.Fatalf("polled mouse target = %#x", target)
	}
}

func TestInputOnlyStartsInConfiguredGameProcess(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	var foreground atomic.Uintptr
	foreground.Store(2)
	n := &Native{
		engine:         engine,
		foreground:     func() windows.HWND { return windows.HWND(foreground.Load()) },
		windowPID:      func(window windows.HWND) uint32 { return uint32(window) },
		processCreated: func(processID uint32) int64 { return int64(processID) * 10 },
	}
	n.SetGameProcesses([]GameProcess{{PID: 1, CreationTime: 10}})
	config := DefaultConfig()
	config.Enabled = true
	config.Interval = 5 * time.Millisecond
	if err := n.Configure(config); err != nil {
		t.Fatal(err)
	}
	n.processPhysicalEvent(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})
	if snapshot := engine.Snapshot(); snapshot.State != StateArmed {
		t.Fatalf("keyboard repeat started outside configured game process: %+v", snapshot)
	}

	foreground.Store(1)
	n.processPhysicalEvent(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: true})
	deadline := time.Now().Add(250 * time.Millisecond)
	for engine.Snapshot().State != StateRunning && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if snapshot := engine.Snapshot(); snapshot.State != StateRunning {
		t.Fatalf("keyboard repeat did not start in configured game process: %+v", snapshot)
	}
	n.processPhysicalEvent(PhysicalEvent{Kind: EventKey, Code: config.OutputKey, Down: false})
}

func TestConfiguredGameProcessRejectsReusedPID(t *testing.T) {
	var creation atomic.Int64
	creation.Store(10)
	n := &Native{
		windowPID:      func(windows.HWND) uint32 { return 1 },
		processCreated: func(uint32) int64 { return creation.Load() },
	}
	n.SetGameProcesses([]GameProcess{{PID: 1, CreationTime: 10}})
	if !n.isGameWindow(windows.HWND(0x1234)) {
		t.Fatal("matching process lifetime was rejected")
	}
	creation.Store(20)
	if n.isGameWindow(windows.HWND(0x1234)) {
		t.Fatal("reused PID with a different creation time was accepted")
	}
}

func TestMouseModeWaitsForConfiguredGameProcess(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	var foreground atomic.Uintptr
	foreground.Store(2)
	n := &Native{
		engine:         engine,
		monitorStop:    make(chan struct{}),
		monitorDone:    make(chan struct{}),
		foreground:     func() windows.HWND { return windows.HWND(foreground.Load()) },
		windowPID:      func(window windows.HWND) uint32 { return uint32(window) },
		processCreated: func(processID uint32) int64 { return int64(processID) * 10 },
	}
	n.SetGameProcesses([]GameProcess{{PID: 1, CreationTime: 10}})
	config := DefaultConfig()
	config.Mode = ModeMouseLeft
	config.Interval = 5 * time.Millisecond
	if err := n.Configure(config); err != nil {
		t.Fatal(err)
	}
	n.processPhysicalEvent(PhysicalEvent{Kind: EventKey, Code: config.MouseLeftToggleKey, Down: true})
	if snapshot := engine.Snapshot(); snapshot.State != StateArmed || !snapshot.Config.Enabled {
		t.Fatalf("mouse mode was not armed outside game process: %+v", snapshot)
	}
	go n.safetyMonitor()
	defer func() {
		close(n.monitorStop)
		<-n.monitorDone
		engine.Close()
	}()
	foreground.Store(1)
	deadline := time.Now().Add(2 * time.Second)
	for engine.Snapshot().State != StateRunning && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if snapshot := engine.Snapshot(); snapshot.State != StateRunning {
		t.Fatalf("mouse mode did not start after configured game process became foreground: %+v", snapshot)
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

func TestMouseToggleStartsImmediatelyWhenPressedInExternalTarget(t *testing.T) {
	injector := &fakeInjector{}
	engine, err := NewEngine(injector, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Interval = 5 * time.Millisecond
	if err := engine.Configure(config); err != nil {
		t.Fatal(err)
	}
	n := &Native{
		engine:     engine,
		wake:       make(chan struct{}, 1),
		foreground: func() windows.HWND { return windows.HWND(0x1234) },
	}
	n.enqueue(PhysicalEvent{Kind: EventKey, Code: config.MouseLeftToggleKey, Down: true})
	n.drain()
	defer engine.Close()
	if snapshot := engine.Snapshot(); snapshot.State != StateRunning || snapshot.Config.Mode != ModeMouseLeft {
		t.Fatalf("external mouse toggle snapshot = %+v", snapshot)
	}
	if target := n.runTarget.Load(); target != 0x1234 {
		t.Fatalf("mouse run target = %#x", target)
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
	deadline = time.Now().Add(2 * time.Second)
	for engine.Snapshot().State != StateDisabled && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if snapshot := engine.Snapshot(); snapshot.State != StateDisabled || snapshot.Config.Enabled {
		t.Fatalf("snapshot after foreground change = %+v", snapshot)
	}
}

func TestTransientForegroundChangeImmediatelyStopsRunningMouseMode(t *testing.T) {
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
	config := DefaultConfig()
	config.Mode = ModeMouseLeft
	config.Enabled = true
	config.Interval = 5 * time.Millisecond
	if err := n.Configure(config); err != nil {
		t.Fatal(err)
	}
	if !engine.Start() {
		t.Fatal("mouse engine did not start")
	}
	n.updateActivationTargets(StateArmed, engine.Snapshot())
	go n.safetyMonitor()
	defer func() {
		close(n.monitorStop)
		<-n.monitorDone
		engine.Close()
	}()
	time.Sleep(150 * time.Millisecond)
	foreground.Store(2)
	deadline := time.Now().Add(500 * time.Millisecond)
	for engine.Snapshot().State != StateDisabled && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if snapshot := engine.Snapshot(); snapshot.State != StateDisabled || snapshot.Config.Enabled {
		t.Fatalf("foreground change did not immediately stop mouse mode: %+v", snapshot)
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
