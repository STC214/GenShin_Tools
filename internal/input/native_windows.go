package input

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	whKeyboardLL   = 13
	whMouseLL      = 14
	wMQuit         = 0x0012
	wMHookControl  = 0x8051
	pmNoRemove     = 0x0000
	ridInput       = 0x10000003
	rimTypeKey     = 1
	ridevRemove    = 0x00000001
	ridevInputSink = 0x00000100
	riKeyBreak     = 0x0001
	riKeyE0        = 0x0002
	riKeyE1        = 0x0004

	wMKeyDown     = 0x0100
	wMKeyUp       = 0x0101
	wMSysKeyDown  = 0x0104
	wMSysKeyUp    = 0x0105
	wMLButtonDown = 0x0201
	wMLButtonUp   = 0x0202
	wMRButtonDown = 0x0204
	wMRButtonUp   = 0x0205

	llkhfExtended = 0x01
	llkhfInjected = 0x10
	llmhfInjected = 0x01

	inputMouse    = 0
	inputKeyboard = 1

	keyeventfExtendedKey = 0x0001
	keyeventfKeyUp       = 0x0002
	keyeventfScanCode    = 0x0008
	mapvkVKToVSCEx       = 4

	mouseeventfLeftDown  = 0x0002
	mouseeventfLeftUp    = 0x0004
	mouseeventfRightDown = 0x0008
	mouseeventfRightUp   = 0x0010

	nativeQueueSize      = 256
	mouseTargetStableFor = 300 * time.Millisecond
	keyboardPollInterval = 5 * time.Millisecond
)

// injectionMarker is deliberately non-zero, uncommon, and limited to 32 bits.
// Mouse low-level hooks can expose only the low 32 bits of dwExtraInfo on some
// Windows paths, while keyboard hooks preserve the full ULONG_PTR value.
// Keeping one 32-bit marker makes self-injected filtering consistent for both.
const injectionMarker uintptr = 0x47544F4C // "GTOL"

// QuickInput tags its mouse events with 214. Matching that public user-mode
// event shape is intentional. Keyboard output keeps the project's separate
// marker used by the known-good scan-code SendInput baseline.
const quickInputExtraInfo uintptr = 214

type point struct{ X, Y int32 }

type message struct {
	Window  uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   point
	Private uint32
}

type keyboardHook struct {
	VirtualKey uint32
	ScanCode   uint32
	Flags      uint32
	Time       uint32
	ExtraInfo  uintptr
}

type mouseHook struct {
	Point     point
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type rawInputDevice struct {
	UsagePage uint16
	Usage     uint16
	Flags     uint32
	Target    uintptr
}

type rawInputHeader struct {
	Type   uint32
	Size   uint32
	Device uintptr
	WParam uintptr
}

type rawKeyboard struct {
	MakeCode         uint16
	Flags            uint16
	Reserved         uint16
	VirtualKey       uint16
	Message          uint32
	ExtraInformation uint32
}

type hookControlRequest struct {
	ready  bool
	state  atomic.Uint32
	result chan error
}

const (
	hookControlPending uint32 = iota
	hookControlClaimed
	hookControlCanceled
	hookControlDone
)

// winInput mirrors INPUT on both 32-bit and 64-bit Windows. The explicit
// uintptr alignment before Data is what gives x64 its required 40-byte size.
type winInput struct {
	Type uint32
	_    uint32
	Data [32]byte
}

// GameProcess identifies one verified game process lifetime. PID alone is not
// sufficient because Windows may reuse it after the original process exits.
type GameProcess struct {
	PID          uint32
	CreationTime int64
}

type hookTargetSnapshot struct {
	configured bool
	processes  map[uint32]struct{}
}

type keyboardSuppressionSnapshot struct {
	active bool
	keys   map[uint32]struct{}
}

type Native struct {
	engine    *Engine
	lifecycle sync.Mutex
	hookMu    sync.Mutex
	physical  sync.Mutex
	queueMu   sync.Mutex
	targetsMu sync.RWMutex

	events      [nativeQueueSize]PhysicalEvent
	head        atomic.Uint32
	tail        atomic.Uint32
	wake        chan struct{}
	done        chan struct{}
	workerDone  chan struct{}
	monitorStop chan struct{}
	monitorDone chan struct{}
	pollDone    chan struct{}
	hookControl chan *hookControlRequest

	threadID         atomic.Uint32
	started          atomic.Bool
	closed           atomic.Bool
	overflow         atomic.Bool
	safetyDisabled   atomic.Bool
	capturing        atomic.Bool
	pollCaptureNew   atomic.Bool
	captureKey       atomic.Uint32
	runTarget        atomic.Uintptr
	armTarget        atomic.Uintptr
	rawRegistered    atomic.Bool
	keyboardReady    atomic.Bool
	observationReady atomic.Bool
	observationDirty atomic.Bool
	hookTargets      atomic.Pointer[hookTargetSnapshot]
	suppression      atomic.Pointer[keyboardSuppressionSnapshot]
	keyboardWorker   *keyboardWorkerController

	keyboardCallback uintptr
	mouseCallback    uintptr
	observerMu       sync.RWMutex
	observer         func(PhysicalEvent)
	foreground       func() windows.HWND
	windowPID        func(windows.HWND) uint32
	processCreated   func(uint32) int64
	keyDown          func(uint32) bool
	// hookDown tracks keys currently held according to WH_KEYBOARD_LL. It is
	// protected by physical. GetAsyncKeyState also observes our SendInput
	// output on some games/Windows builds, so its "up" result must not cancel a
	// real hook-confirmed hold of the repeat key.
	hookDown          map[uint32]bool
	gameProcesses     map[uint32]int64
	targetsConfigured bool
}

var activeNative atomic.Pointer[Native]

var (
	inputUser32                  = windows.NewLazySystemDLL("user32.dll")
	inputKernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procSetWindowsHookExW        = inputUser32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx      = inputUser32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx           = inputUser32.NewProc("CallNextHookEx")
	procGetMessageW              = inputUser32.NewProc("GetMessageW")
	procPeekMessageW             = inputUser32.NewProc("PeekMessageW")
	procPostThreadMessageW       = inputUser32.NewProc("PostThreadMessageW")
	procPostMessageW             = inputUser32.NewProc("PostMessageW")
	procSendInput                = inputUser32.NewProc("SendInput")
	procKeybdEvent               = inputUser32.NewProc("keybd_event")
	procMapVirtualKeyExW         = inputUser32.NewProc("MapVirtualKeyExW")
	procGetWindowThreadProcessID = inputUser32.NewProc("GetWindowThreadProcessId")
	procGetKeyboardLayout        = inputUser32.NewProc("GetKeyboardLayout")
	procGetAsyncKeyState         = inputUser32.NewProc("GetAsyncKeyState")
	procEnumWindows              = inputUser32.NewProc("EnumWindows")
	procGetClassNameW            = inputUser32.NewProc("GetClassNameW")
	procRegisterRawInputDevices  = inputUser32.NewProc("RegisterRawInputDevices")
	procGetRawInputData          = inputUser32.NewProc("GetRawInputData")
	procGetModuleHandleW         = inputKernel32.NewProc("GetModuleHandleW")
)

func NewNative(onChange func(Snapshot)) (*Native, error) {
	n := &Native{
		wake:           make(chan struct{}, 1),
		done:           make(chan struct{}),
		workerDone:     make(chan struct{}),
		monitorStop:    make(chan struct{}),
		monitorDone:    make(chan struct{}),
		pollDone:       make(chan struct{}),
		hookControl:    make(chan *hookControlRequest, 1),
		foreground:     windows.GetForegroundWindow,
		windowPID:      windowProcessID,
		processCreated: processCreationTime,
		keyDown:        asyncKeyDown,
		hookDown:       make(map[uint32]bool),
		gameProcesses:  make(map[uint32]int64),
	}
	injector, err := newSendInputInjector()
	if err != nil {
		return nil, err
	}
	injector.allowed = func() bool {
		return !n.keyboardInputHeldForLaunch() && n.isGameWindowFast(n.foreground())
	}
	n.keyboardWorker = newKeyboardWorkerController()
	injector.externalKeyboard = n.keyboardWorker.Active
	engine, err := NewEngine(injector, func(snapshot Snapshot) {
		n.publishKeyboardSuppression(snapshot)
		_ = n.syncKeyboardWorker(snapshot)
		if onChange != nil {
			onChange(snapshot)
		}
	})
	if err != nil {
		return nil, err
	}
	n.engine = engine
	n.keyboardCallback = syscall.NewCallback(keyboardHookCallback)
	n.mouseCallback = syscall.NewCallback(mouseHookCallback)
	return n, nil
}

func (n *Native) Start() error {
	n.lifecycle.Lock()
	defer n.lifecycle.Unlock()
	if n.closed.Load() {
		return errors.New("native input is closed")
	}
	if !n.started.CompareAndSwap(false, true) {
		return errors.New("native input is already started")
	}
	if !activeNative.CompareAndSwap(nil, n) {
		n.started.Store(false)
		return errors.New("another native input hook is active")
	}
	ready := make(chan error, 1)
	go n.runHookThread(ready)
	if err := <-ready; err != nil {
		activeNative.CompareAndSwap(n, nil)
		n.started.Store(false)
		return err
	}
	go n.eventWorker()
	go n.safetyMonitor()
	go n.keyboardPoller()
	return nil
}

func (n *Native) runHookThread(ready chan<- error) {
	defer func() {
		if recover() != nil {
			err := errors.New("panic in input hook thread")
			select {
			case ready <- err:
			default:
			}
			n.engine.Fail(err)
		}
	}()
	n.hookThread(ready)
}

func (n *Native) Configure(config Config) error {
	before := n.engine.Snapshot().State
	err := n.engine.Configure(config)
	if err == nil {
		n.clearHookState()
	}
	n.updateActivationTargets(before, n.engine.Snapshot())
	return err
}
func (n *Native) Enable(enabled bool) {
	before := n.engine.Snapshot().State
	n.engine.Enable(enabled)
	if !enabled {
		n.clearHookState()
	}
	n.updateActivationTargets(before, n.engine.Snapshot())
}
func (n *Native) Snapshot() Snapshot { return n.engine.Snapshot() }
func (n *Native) KeyboardWorkerActive() bool {
	return n != nil && n.keyboardWorker != nil && n.keyboardWorker.Active()
}
func (n *Native) KeyboardWorkerPID() int {
	if n == nil || n.keyboardWorker == nil {
		return 0
	}
	return n.keyboardWorker.PID()
}
func (n *Native) KeyboardWorkerError() string {
	if n == nil || n.keyboardWorker == nil {
		return "keyboard worker is unavailable"
	}
	return n.keyboardWorker.LastError()
}

// SetKeyboardBackendReady is the hard post-launch gate for the x86 keyboard
// hook. Game discovery may configure target PIDs early, but it must not install
// the hook until launch and every injected module have completed.
func (n *Native) SetKeyboardBackendReady(ready bool) error {
	if n == nil || n.keyboardWorker == nil {
		return errors.New("keyboard worker is unavailable")
	}
	if !ready && n.engine != nil {
		// Stop any active mouse/legacy output loop while preserving the user's
		// enabled configuration. No synthetic input may survive into the
		// suspended launch or plugin initialization interval.
		n.engine.stop(false)
	}
	n.keyboardReady.Store(ready)
	return n.syncKeyboardWorker(n.engine.Snapshot())
}

// SetObservationHooksReady controls the main-process low-level keyboard and
// mouse observation hooks on their owning message-loop thread. Disabling them
// before injection and reinstalling them during finalization ensures even the
// non-suppressing physical-event hooks are younger than injected plugin hooks.
func (n *Native) SetObservationHooksReady(ready bool) error {
	if n == nil {
		return errors.New("native input is unavailable")
	}
	n.hookMu.Lock()
	defer n.hookMu.Unlock()
	if n.closed.Load() || !n.started.Load() {
		return errors.New("native input hook thread is not running")
	}
	if n.observationReady.Load() == ready && !n.observationDirty.Load() {
		return nil
	}
	request := &hookControlRequest{ready: ready, result: make(chan error, 1)}
	select {
	case n.hookControl <- request:
	default:
		return errors.New("native input hook control is busy")
	}
	result, _, callErr := procPostThreadMessageW.Call(uintptr(n.threadID.Load()), wMHookControl, 0, 0)
	if result == 0 {
		select {
		case <-n.hookControl:
		default:
		}
		return fmt.Errorf("post native input hook control: %w", normalizeCallError(callErr))
	}
	select {
	case err := <-request.result:
		return err
	case <-n.done:
		return errors.New("native input hook thread stopped during control request")
	case <-time.After(2 * time.Second):
		// Cancellation wins only while the hook thread has not claimed the
		// request. If it already started applying the state, wait for its
		// acknowledgement so no request can take effect after this method
		// reports a timeout to the caller.
		if request.state.CompareAndSwap(hookControlPending, hookControlCanceled) {
			select {
			case <-n.hookControl:
			default:
			}
			return errors.New("native input hook control timed out before execution")
		}
		select {
		case err := <-request.result:
			return err
		case <-n.done:
			return errors.New("native input hook thread stopped after claiming control request")
		}
	}
}

func (n *Native) ObservationHooksReady() bool {
	return n != nil && n.observationReady.Load() && !n.observationDirty.Load()
}

// RefreshKeyboardBackend reinstalls an already released x86 worker hook. The
// caller cannot bypass the post-launch readiness gate.
func (n *Native) RefreshKeyboardBackend() error {
	if n == nil || n.keyboardWorker == nil {
		return errors.New("keyboard worker is unavailable")
	}
	if !n.keyboardReady.Load() {
		return errors.New("keyboard worker is waiting for post-launch input finalization")
	}
	return n.keyboardWorker.Restart()
}

func (n *Native) GameForeground() bool {
	return n != nil && n.foreground != nil && n.isGameWindowFast(n.foreground())
}

func (n *Native) GameProcessIDs() []uint32 {
	targets := n.hookTargets.Load()
	if targets == nil || !targets.configured {
		return nil
	}
	processes := make([]uint32, 0, len(targets.processes))
	for processID := range targets.processes {
		processes = append(processes, processID)
	}
	return processes
}

// RegisterRawKeyboard adds an INPUTSINK physical keyboard path to the launcher
// window. Raw events with a real device handle remain distinguishable from
// this process's SendInput output.
func (n *Native) RegisterRawKeyboard(window uintptr) error {
	if window == 0 {
		return errors.New("raw keyboard target window is required")
	}
	device := rawInputDevice{UsagePage: 1, Usage: 6, Flags: ridevInputSink, Target: window}
	result, _, callErr := procRegisterRawInputDevices.Call(
		uintptr(unsafe.Pointer(&device)),
		1,
		unsafe.Sizeof(device),
	)
	if result == 0 {
		return fmt.Errorf("RegisterRawInputDevices: %w", normalizeCallError(callErr))
	}
	n.rawRegistered.Store(true)
	return nil
}

// HandleRawInput accepts WM_INPUT LPARAM from the registered launcher window.
func (n *Native) HandleRawInput(rawHandle uintptr) {
	if rawHandle == 0 {
		return
	}
	var size uint32
	headerSize := uint32(unsafe.Sizeof(rawInputHeader{}))
	result, _, _ := procGetRawInputData.Call(rawHandle, ridInput, 0, uintptr(unsafe.Pointer(&size)), uintptr(headerSize))
	if uint32(result) == ^uint32(0) || size < headerSize+uint32(unsafe.Sizeof(rawKeyboard{})) || size > 4096 {
		return
	}
	buffer := make([]byte, size)
	result, _, _ = procGetRawInputData.Call(rawHandle, ridInput, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)), uintptr(headerSize))
	if uint32(result) == ^uint32(0) || uint32(result) < headerSize+uint32(unsafe.Sizeof(rawKeyboard{})) {
		return
	}
	header := (*rawInputHeader)(unsafe.Pointer(&buffer[0]))
	// SendInput-originated raw records do not carry a physical device handle.
	if header.Type != rimTypeKey || header.Device == 0 {
		return
	}
	keyboard := (*rawKeyboard)(unsafe.Pointer(&buffer[headerSize]))
	n.handleRawKeyboard(*keyboard)
}

func (n *Native) handleRawKeyboard(keyboard rawKeyboard) {
	if keyboard.VirtualKey == 0 || keyboard.VirtualKey == 0xff {
		return
	}
	n.enqueue(PhysicalEvent{
		Kind: EventKey,
		Code: EncodeKeyCode(uint32(keyboard.VirtualKey), keyboard.Flags&(riKeyE0|riKeyE1) != 0),
		Down: keyboard.Flags&riKeyBreak == 0,
	})
}

// SetGameProcesses restricts generated input to verified running game process
// lifetimes. Passing an empty list deliberately disables input until a game
// process is discovered again.
func (n *Native) SetGameProcesses(processes []GameProcess) {
	n.targetsMu.Lock()
	if n.gameProcesses == nil {
		n.gameProcesses = make(map[uint32]int64)
	}
	clear(n.gameProcesses)
	hookProcesses := make(map[uint32]struct{}, len(processes))
	for _, process := range processes {
		if process.PID != 0 && process.CreationTime > 0 {
			n.gameProcesses[process.PID] = process.CreationTime
			hookProcesses[process.PID] = struct{}{}
		}
	}
	n.targetsConfigured = true
	n.targetsMu.Unlock()
	n.hookTargets.Store(&hookTargetSnapshot{configured: true, processes: hookProcesses})
	if n.engine != nil {
		_ = n.syncKeyboardWorker(n.engine.Snapshot())
	}
}

func (n *Native) syncKeyboardWorker(snapshot Snapshot) error {
	if n.keyboardWorker == nil {
		return nil
	}
	n.targetsMu.RLock()
	processes := make([]uint32, 0, len(n.gameProcesses))
	if n.keyboardReady.Load() {
		for processID := range n.gameProcesses {
			processes = append(processes, processID)
		}
	}
	n.targetsMu.RUnlock()
	return n.keyboardWorker.Configure(keyboardWorkerRequest{
		Enabled:       snapshot.Config.Enabled && snapshot.Config.Mode == ModeKeyboard,
		RepeatKeys:    snapshot.Config.RepeatKeys.Slice(),
		IntervalMS:    snapshot.Config.IntervalMS,
		GameProcesses: processes,
	})
}

func (n *Native) publishKeyboardSuppression(snapshot Snapshot) {
	// The x86 worker owns both the physical hook and AHK-compatible output, so
	// it suppresses only configured trigger keys inside the verified game.
	// The x64 hook must not suppress the same event a second time.
	n.suppression.Store(&keyboardSuppressionSnapshot{})
}

func (n *Native) shouldSuppressKeyboard(code uint32) bool {
	policy := n.suppression.Load()
	if policy == nil || !policy.active {
		return false
	}
	if _, ok := policy.keys[NormalizeKeyCode(code)]; !ok {
		return false
	}
	targets := n.hookTargets.Load()
	if targets == nil || !targets.configured {
		return false
	}
	foreground := n.foreground()
	if foreground == 0 || n.windowPID == nil {
		return false
	}
	_, allowed := targets.processes[n.windowPID(foreground)]
	return allowed
}

func (n *Native) isGameWindow(window windows.HWND) bool {
	n.targetsMu.RLock()
	configured := n.targetsConfigured
	// Retain permissive behavior for low-level unit fixtures that do not
	// configure a game target. The shell always configures this before input
	// can be enabled, including with an empty process list.
	if !configured {
		n.targetsMu.RUnlock()
		return true
	}
	if window == 0 {
		n.targetsMu.RUnlock()
		return false
	}
	windowPID := n.windowPID
	processCreated := n.processCreated
	processID := windowPID(window)
	expectedCreation, allowed := n.gameProcesses[processID]
	n.targetsMu.RUnlock()
	if !allowed || processCreated == nil {
		return false
	}
	return processCreated(processID) == expectedCreation
}

// isGameWindowFast is used directly at the SendInput boundary. The full
// creation-time check remains in the safety monitor; this fast path checks the
// current foreground HWND and its PID before every emitted pair without
// opening the game process up to one thousand times per second.
func (n *Native) isGameWindowFast(window windows.HWND) bool {
	n.targetsMu.RLock()
	defer n.targetsMu.RUnlock()
	if !n.targetsConfigured {
		return true
	}
	if window == 0 || n.windowPID == nil {
		return false
	}
	processID := n.windowPID(window)
	_, allowed := n.gameProcesses[processID]
	return allowed
}

func processCreationTime(processID uint32) int64 {
	if processID == 0 {
		return 0
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(process)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &creation, &exit, &kernel, &user); err != nil {
		return 0
	}
	return creation.Nanoseconds()
}

func (n *Native) updateActivationTargets(before State, snapshot Snapshot) {
	if before != StateRunning && snapshot.State == StateRunning {
		n.armTarget.Store(0)
		n.runTarget.Store(uintptr(n.foreground()))
	} else if snapshot.State == StateArmed && snapshot.Config.Enabled && snapshot.Config.Mode != ModeKeyboard {
		n.runTarget.Store(0)
		if before != StateArmed || n.armTarget.Load() == 0 {
			n.armTarget.Store(uintptr(n.foreground()))
		}
	} else if snapshot.State != StateRunning {
		n.runTarget.Store(0)
		n.armTarget.Store(0)
	}
}

func (n *Native) ForegroundIntegrity() IntegrityReport {
	return checkForegroundIntegrity()
}

// SetObserver receives filtered physical input on the event worker, never on
// the hook callback. It is intended for hotkey recording and diagnostics.
func (n *Native) SetObserver(observer func(PhysicalEvent)) {
	n.observerMu.Lock()
	n.observer = observer
	n.observerMu.Unlock()
}

// SetCaptureMode makes physical keyboard events observable by the UI without
// allowing the same key press to toggle or trigger input enhancement.
func (n *Native) SetCaptureMode(capturing bool) {
	n.physical.Lock()
	defer n.physical.Unlock()
	if capturing {
		n.captureKey.Store(0)
		// The polling fallback may already have a baseline from normal input
		// monitoring. Force the next capture scan to treat a currently-down
		// key as a new press, including when it was pressed before that scan.
		// Publish this reset before capture becomes visible to the poller.
		n.pollCaptureNew.Store(true)
		n.capturing.Store(true)
		return
	}
	n.capturing.Store(false)
	n.pollCaptureNew.Store(false)
	n.captureKey.Store(0)
}

func (n *Native) Close() {
	n.lifecycle.Lock()
	if !n.closed.CompareAndSwap(false, true) {
		n.lifecycle.Unlock()
		return
	}
	n.engine.Enable(false)
	if n.rawRegistered.Swap(false) {
		device := rawInputDevice{UsagePage: 1, Usage: 6, Flags: ridevRemove}
		procRegisterRawInputDevices.Call(uintptr(unsafe.Pointer(&device)), 1, unsafe.Sizeof(device))
	}
	started := n.started.Load()
	if started {
		close(n.monitorStop)
		if id := n.threadID.Load(); id != 0 {
			procPostThreadMessageW.Call(uintptr(id), wMQuit, 0, 0)
		}
	}
	n.lifecycle.Unlock()
	if started {
		<-n.done
		<-n.workerDone
		<-n.monitorDone
		<-n.pollDone
	}
	n.engine.Close()
	n.keyboardWorker.Close()
}

// keyboardPoller is a fallback for protected game input paths that do not
// deliver physical keyboard transitions to WH_KEYBOARD_LL. Hook events remain
// the primary path; Engine's held-key state makes duplicate hook/poll edges
// idempotent when both sources are available.
func (n *Native) keyboardPoller() {
	defer close(n.pollDone)
	defer func() {
		if recover() != nil {
			n.engine.Fail(errors.New("panic in keyboard state poller"))
		}
	}()
	ticker := time.NewTicker(keyboardPollInterval)
	defer ticker.Stop()
	states := make(map[uint32]bool, 5)
	for {
		select {
		case <-n.monitorStop:
			return
		case <-ticker.C:
			n.pollKeyboardOnce(states)
		}
	}
}

func (n *Native) pollKeyboardOnce(states map[uint32]bool) {
	config := n.engine.Snapshot().Config
	keys := config.RepeatKeys.Slice()
	keys = append(keys,
		config.StopKey,
		config.KeyboardToggleKey,
		config.MouseLeftToggleKey,
		config.MouseRightToggleKey,
	)
	capturing := n.capturing.Load()
	if capturing {
		// Protected input paths may not deliver WH_KEYBOARD_LL events even
		// while the launcher owns the foreground. During shortcut recording,
		// poll every virtual key so the UI can still accept the next key.
		keys = keys[:0]
		for virtualKey := uint32(1); virtualKey <= 0xfe; virtualKey++ {
			if capturePollingKeyboardKey(virtualKey) {
				keys = append(keys, NormalizeKeyCode(virtualKey))
			}
		}
	}
	captureFirstScan := capturing && n.pollCaptureNew.Swap(false)
	if captureFirstScan {
		clear(states)
	}
	live := make(map[uint32]bool, len(keys))
	for _, code := range keys {
		code = NormalizeKeyCode(code)
		if !ValidKeyCode(code) || live[code] {
			continue
		}
		live[code] = true
		down := n.keyDown(code)
		previous, initialized := states[code]
		states[code] = down
		if !initialized {
			if captureFirstScan && down {
				n.dispatchPolledEvent(PhysicalEvent{Kind: EventKey, Code: code, Down: true})
			}
			continue
		}
		if down == previous {
			continue
		}
		n.dispatchPolledEvent(PhysicalEvent{Kind: EventKey, Code: code, Down: down})
	}
	for code := range states {
		if !live[code] {
			delete(states, code)
		}
	}
}

func capturePollingKeyboardKey(virtualKey uint32) bool {
	switch virtualKey {
	case 0x01, // VK_LBUTTON
		0x02, // VK_RBUTTON
		0x04, // VK_MBUTTON
		0x05, // VK_XBUTTON1
		0x06: // VK_XBUTTON2
		return false
	default:
		return true
	}
}

func asyncKeyDown(code uint32) bool {
	state, _, _ := procGetAsyncKeyState.Call(uintptr(VirtualKey(code)))
	return state&0x8000 != 0
}

func (n *Native) safetyMonitor() {
	defer close(n.monitorDone)
	defer func() {
		if recover() != nil {
			n.engine.Fail(errors.New("panic in input safety monitor"))
		}
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var target windows.HWND
	var runningSince time.Time
	var candidate windows.HWND
	var candidateSince time.Time
	var targetProcessID uint32
	for {
		select {
		case <-n.monitorStop:
			return
		case <-ticker.C:
			snapshot := n.engine.Snapshot()
			if snapshot.State == StateArmed && snapshot.Config.Enabled && snapshot.Config.Mode != ModeKeyboard {
				target = 0
				targetProcessID = 0
				runningSince = time.Time{}
				foreground := n.foreground()
				origin := windows.HWND(n.armTarget.Load())
				if origin == 0 {
					n.armTarget.Store(uintptr(foreground))
					candidate = 0
					candidateSince = time.Time{}
				} else if foreground == 0 || foreground == origin || currentProcessWindow(foreground) || !n.isGameWindow(foreground) {
					candidate = 0
					candidateSince = time.Time{}
				} else if foreground != candidate {
					candidate = foreground
					candidateSince = time.Now()
				} else if time.Since(candidateSince) >= mouseTargetStableFor {
					n.runTarget.Store(uintptr(foreground))
					n.armTarget.Store(0)
					if !n.engine.Start() {
						n.runTarget.Store(0)
					}
					candidate = 0
					candidateSince = time.Time{}
				}
				continue
			}
			candidate = 0
			candidateSince = time.Time{}
			if snapshot.State != StateRunning {
				target = 0
				targetProcessID = 0
				n.runTarget.Store(0)
				n.armTarget.Store(0)
				runningSince = time.Time{}
				continue
			}
			foreground := n.foreground()
			if target == 0 {
				target = windows.HWND(n.runTarget.Load())
				if target == 0 {
					continue
				}
				targetProcessID = windowProcessID(target)
				runningSince = time.Now()
				continue
			}
			sameTarget := n.isGameWindow(foreground) && (foreground == target ||
				targetProcessID != 0 && windowProcessID(foreground) == targetProcessID)
			if !n.safetyDisabled.Load() && !sameTarget {
				n.engine.Enable(false)
				continue
			}
			if time.Since(runningSince) > 30*time.Minute {
				n.engine.Fail(errors.New("continuous input exceeded the 30-minute safety limit"))
			}
		}
	}
}

func windowProcessID(window windows.HWND) uint32 {
	if window == 0 {
		return 0
	}
	var processID uint32
	if _, err := windows.GetWindowThreadProcessId(window, &processID); err != nil {
		return 0
	}
	return processID
}

func currentProcessWindow(window windows.HWND) bool {
	return windowProcessID(window) == windows.GetCurrentProcessId()
}

func (n *Native) hookThread(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(n.done)
	defer activeNative.CompareAndSwap(n, nil)

	// PostThreadMessage fails if the target thread has not created a message
	// queue yet. Create it before Start can report success, otherwise an
	// immediate Close can wait forever for a WM_QUIT that was never posted.
	var queueMessage message
	procPeekMessageW.Call(uintptr(unsafe.Pointer(&queueMessage)), 0, 0, 0, pmNoRemove)
	n.threadID.Store(windows.GetCurrentThreadId())
	module, _, callErr := procGetModuleHandleW.Call(0)
	if module == 0 {
		ready <- fmt.Errorf("GetModuleHandleW: %w", normalizeCallError(callErr))
		return
	}
	var keyboard, mouse uintptr
	install := func() error {
		if keyboard != 0 && mouse != 0 {
			n.observationReady.Store(true)
			n.observationDirty.Store(false)
			return nil
		}
		installedKeyboard := false
		if keyboard == 0 {
			var callErr error
			keyboard, _, callErr = procSetWindowsHookExW.Call(whKeyboardLL, n.keyboardCallback, module, 0)
			if keyboard == 0 {
				return fmt.Errorf("install keyboard hook: %w", normalizeCallError(callErr))
			}
			installedKeyboard = true
		}
		if mouse == 0 {
			var callErr error
			mouse, _, callErr = procSetWindowsHookExW.Call(whMouseLL, n.mouseCallback, module, 0)
			if mouse == 0 {
				result := fmt.Errorf("install mouse hook: %w", normalizeCallError(callErr))
				if installedKeyboard {
					ok, _, rollbackErr := procUnhookWindowsHookEx.Call(keyboard)
					if ok == 0 {
						result = errors.Join(result, fmt.Errorf("rollback keyboard hook: %w", normalizeCallError(rollbackErr)))
					} else {
						keyboard = 0
					}
				}
				n.observationReady.Store(false)
				n.observationDirty.Store(true)
				return result
			}
		}
		n.observationReady.Store(true)
		n.observationDirty.Store(false)
		return nil
	}
	uninstall := func() error {
		n.observationReady.Store(false)
		var result error
		if mouse != 0 {
			ok, _, callErr := procUnhookWindowsHookEx.Call(mouse)
			if ok == 0 {
				result = errors.Join(result, fmt.Errorf("uninstall mouse hook: %w", normalizeCallError(callErr)))
			} else {
				mouse = 0
			}
		}
		if keyboard != 0 {
			ok, _, callErr := procUnhookWindowsHookEx.Call(keyboard)
			if ok == 0 {
				result = errors.Join(result, fmt.Errorf("uninstall keyboard hook: %w", normalizeCallError(callErr)))
			} else {
				keyboard = 0
			}
		}
		n.clearHookState()
		n.observationDirty.Store(result != nil)
		return result
	}
	defer func() { _ = uninstall() }()
	if err := install(); err != nil {
		ready <- err
		return
	}
	ready <- nil

	var msg message
	for {
		value, _, callErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		result := int32(value)
		if result == 0 {
			return
		}
		if result == -1 {
			n.engine.Fail(fmt.Errorf("input hook GetMessageW: %w", normalizeCallError(callErr)))
			return
		}
		if msg.Message == wMHookControl {
			var request *hookControlRequest
			select {
			case request = <-n.hookControl:
			default:
				continue
			}
			if request == nil || !request.state.CompareAndSwap(hookControlPending, hookControlClaimed) {
				if request != nil {
					request.result <- errors.New("native input hook control request was canceled")
				}
				continue
			}
			var err error
			if request.ready {
				err = install()
			} else {
				err = uninstall()
			}
			request.state.Store(hookControlDone)
			request.result <- err
		}
	}
}

func (n *Native) eventWorker() {
	defer close(n.workerDone)
	defer func() {
		if recover() != nil {
			n.engine.Fail(errors.New("panic in physical input worker"))
		}
	}()
	for {
		select {
		case <-n.wake:
			n.drain()
		case <-n.done:
			n.drain()
			return
		}
	}
}

func (n *Native) drain() {
	for {
		tail := n.tail.Load()
		if tail == n.head.Load() {
			break
		}
		event := n.events[tail%nativeQueueSize]
		n.tail.Store(tail + 1)
		n.observePhysical(event)
		n.physical.Lock()
		n.noteHookEventLocked(event)
		if n.capturing.Load() {
			n.capturePhysicalKeyLocked(event)
			n.physical.Unlock()
			continue
		}
		n.processPhysicalEventLocked(event)
		n.physical.Unlock()
	}
	if n.overflow.Swap(false) {
		n.engine.Fail(errors.New("physical input event queue overflowed; input enhancement disabled"))
	}
}

func (n *Native) dispatchPolledEvent(event PhysicalEvent) {
	n.physical.Lock()
	if n.capturing.Load() {
		n.physical.Unlock()
		n.observePhysical(event)
		n.physical.Lock()
		defer n.physical.Unlock()
		if !n.capturing.Load() {
			return
		}
		n.capturePhysicalKeyLocked(event)
		return
	}
	defer n.physical.Unlock()
	// A physical hook down is authoritative until its matching hook up. This
	// prevents an injected repeat-key up from the polling fallback ending the
	// user's still-held keyboard repeat trigger.
	if event.Kind == EventKey && n.hookDown != nil && n.hookDown[NormalizeKeyCode(event.Code)] {
		return
	}
	n.processPhysicalEventLocked(event)
}

func (n *Native) observePhysical(event PhysicalEvent) {
	n.observerMu.RLock()
	observer := n.observer
	n.observerMu.RUnlock()
	if observer != nil {
		observer(event)
	}
}

func (n *Native) capturePhysicalKeyLocked(event PhysicalEvent) {
	if event.Kind != EventKey {
		return
	}
	key := NormalizeKeyCode(event.Code)
	captured := n.captureKey.Load()
	if event.Down && captured == 0 {
		if n.captureKey.CompareAndSwap(0, key) {
			// A hook/raw event won the capture. Do not let the polling fallback
			// rescan already-held keys and publish duplicate candidates.
			n.pollCaptureNew.Store(false)
		}
	} else if !event.Down && captured != 0 && SameKey(key, captured) {
		// Keep all physical input suppressed through key-up. This prevents
		// keyboard autorepeat after the first down event from immediately
		// triggering the newly recorded shortcut.
		n.capturing.Store(false)
		n.captureKey.Store(0)
	}
}

func (n *Native) processPhysicalEvent(event PhysicalEvent) {
	n.physical.Lock()
	defer n.physical.Unlock()
	if n.capturing.Load() {
		return
	}
	n.noteHookEventLocked(event)
	n.processPhysicalEventLocked(event)
}

func (n *Native) noteHookEventLocked(event PhysicalEvent) {
	if event.Kind != EventKey {
		return
	}
	if n.hookDown == nil {
		n.hookDown = make(map[uint32]bool)
	}
	code := NormalizeKeyCode(event.Code)
	if event.Down {
		n.hookDown[code] = true
	} else {
		delete(n.hookDown, code)
	}
}

func (n *Native) clearHookState() {
	n.physical.Lock()
	defer n.physical.Unlock()
	n.clearHookStateLocked()
}

func (n *Native) clearHookStateLocked() {
	if n.hookDown != nil {
		clear(n.hookDown)
	}
}

func (n *Native) processPhysicalEventLocked(event PhysicalEvent) {
	// Once a verified game lifetime exists, no keyboard trigger or toggle may
	// reach Engine until the post-injection finalizer releases the keyboard
	// backend. The polling fallback remains alive while observation hooks are
	// deliberately unloaded, so this gate prevents an early key press from
	// faulting Engine or starting output before the worker is installed.
	if event.Kind == EventKey && n.keyboardInputHeldForLaunch() {
		return
	}
	before := n.engine.Snapshot()
	if before.State == StateArmed && before.Config.Enabled && before.Config.Mode == ModeKeyboard &&
		event.Kind == EventKey && event.Down && before.Config.IsRepeatKey(event.Code) && !n.isGameWindow(n.foreground()) {
		return
	}
	n.engine.Handle(event)
	after := n.engine.Snapshot()
	// Hook ownership belongs to one enabled input session only. Clearing it on
	// a stop prevents a missing key-up from suppressing polling after a later
	// reconfigure or re-enable.
	if before.State == StateRunning && after.State != StateRunning {
		n.clearHookStateLocked()
	}
	if event.Kind == EventKey && event.Down {
		if mode, toggle := before.Config.ToggleMode(event.Code); toggle && mode != ModeKeyboard && after.State == StateArmed && after.Config.Enabled && after.Config.Mode == mode {
			target := n.foreground()
			if target != 0 && !currentProcessWindow(target) && n.isGameWindow(target) {
				n.runTarget.Store(uintptr(target))
				n.armTarget.Store(0)
				if n.engine.Start() {
					after = n.engine.Snapshot()
				} else {
					n.runTarget.Store(0)
				}
			}
		}
	}
	n.updateActivationTargets(before.State, after)
}

func (n *Native) keyboardInputHeldForLaunch() bool {
	if n == nil || n.keyboardReady.Load() {
		return false
	}
	targets := n.hookTargets.Load()
	return targets != nil && targets.configured && len(targets.processes) != 0
}

func (n *Native) enqueue(event PhysicalEvent) {
	// WH_KEYBOARD_LL/WH_MOUSE_LL run on the hook thread while WM_INPUT is
	// delivered on the UI thread. Serialize producers so two callbacks cannot
	// reserve and overwrite the same ring slot.
	n.queueMu.Lock()
	defer n.queueMu.Unlock()
	head := n.head.Load()
	if head-n.tail.Load() >= nativeQueueSize {
		n.overflow.Store(true)
		select {
		case n.wake <- struct{}{}:
		default:
		}
		return
	}
	n.events[head%nativeQueueSize] = event
	n.head.Store(head + 1)
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

func keyboardHookCallback(code int, wParam, lParam uintptr) (result uintptr) {
	defer func() {
		if recover() != nil {
			if n := activeNative.Load(); n != nil {
				n.engine.Fail(errors.New("panic in keyboard hook callback"))
			}
			result, _, _ = procCallNextHookEx.Call(0, uintptr(code), wParam, lParam)
		}
	}()
	if code >= 0 {
		if handleKeyboardHook((*keyboardHook)(unsafe.Pointer(lParam)), wParam) {
			return 1
		}
	}
	result, _, _ = procCallNextHookEx.Call(0, uintptr(code), wParam, lParam)
	return result
}

func mouseHookCallback(code int, wParam, lParam uintptr) (result uintptr) {
	defer func() {
		if recover() != nil {
			if n := activeNative.Load(); n != nil {
				n.engine.Fail(errors.New("panic in mouse hook callback"))
			}
			result, _, _ = procCallNextHookEx.Call(0, uintptr(code), wParam, lParam)
		}
	}()
	if code >= 0 {
		handleMouseHook((*mouseHook)(unsafe.Pointer(lParam)), wParam)
	}
	result, _, _ = procCallNextHookEx.Call(0, uintptr(code), wParam, lParam)
	return result
}

func handleKeyboardHook(data *keyboardHook, message uintptr) bool {
	if data == nil || data.Flags&llkhfInjected != 0 || data.ExtraInfo == injectionMarker || data.ExtraInfo == interceptionMarker {
		return false
	}
	down := message == wMKeyDown || message == wMSysKeyDown
	up := message == wMKeyUp || message == wMSysKeyUp
	if !down && !up {
		return false
	}
	if n := activeNative.Load(); n != nil {
		code := EncodeKeyCode(data.VirtualKey, data.Flags&llkhfExtended != 0)
		n.enqueue(PhysicalEvent{Kind: EventKey, Code: code, Down: down})
		return false
	}
	return false
}

func handleMouseHook(data *mouseHook, message uintptr) {
	if data == nil || data.Flags&llmhfInjected != 0 || data.ExtraInfo == injectionMarker {
		return
	}
	var event PhysicalEvent
	switch message {
	case wMLButtonDown:
		event = PhysicalEvent{Kind: EventMouseLeft, Down: true}
	case wMLButtonUp:
		event = PhysicalEvent{Kind: EventMouseLeft, Down: false}
	case wMRButtonDown:
		event = PhysicalEvent{Kind: EventMouseRight, Down: true}
	case wMRButtonUp:
		event = PhysicalEvent{Kind: EventMouseRight, Down: false}
	default:
		return
	}
	if n := activeNative.Load(); n != nil {
		n.enqueue(event)
	}
}

type sendInputInjector struct {
	selfRID          uint32
	mu               sync.Mutex
	lastCheck        time.Time
	lastReport       IntegrityReport
	allowed          func() bool
	needsRelease     atomic.Bool
	externalKeyboard func() bool
}

func newSendInputInjector() (*sendInputInjector, error) {
	level, err := currentIntegrityLevel()
	if err != nil {
		return nil, fmt.Errorf("query current process integrity: %w", err)
	}
	return &sendInputInjector{selfRID: level}, nil
}

func (s *sendInputInjector) Emit(config Config) error {
	if s.allowed != nil && !s.allowed() {
		return errOutputTargetLost
	}
	if report := s.integrityReport(); report.Blocked {
		return fmt.Errorf("foreground process PID %d has higher integrity (%s) than Genshin Tools (%s); restart Genshin Tools at the same privilege level", report.TargetPID, report.TargetName, report.SelfName)
	}
	if s.allowed != nil && !s.allowed() {
		return errOutputTargetLost
	}
	if config.Mode == ModeKeyboard {
		if s.externalKeyboard != nil && s.externalKeyboard() {
			return nil
		}
		return errors.New("Interception keyboard worker is unavailable; install the driver, restart Windows, and run Genshin Tools as administrator")
	}
	down, up, err := s.pressFunctions(config)
	if err != nil {
		return err
	}
	s.needsRelease.Store(true)
	if err := emitPressRelease(down, up, pressDuration(config.Interval), time.Sleep); err != nil {
		// The down edge may already have reached the target. Retry the up edge
		// once before entering Fault so API failure cannot leave a stuck key or
		// mouse button.
		if up() == nil {
			s.needsRelease.Store(false)
		}
		return err
	}
	s.needsRelease.Store(false)
	return nil
}

func (s *sendInputInjector) integrityReport() IntegrityReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.lastCheck) < 250*time.Millisecond {
		return s.lastReport
	}
	s.lastCheck = time.Now()
	s.lastReport = checkForegroundIntegrityFromSelf(s.selfRID)
	return s.lastReport
}

func (s *sendInputInjector) Release(config Config) error {
	if !s.needsRelease.Swap(false) {
		return nil
	}
	if config.Mode == ModeKeyboard {
		up, err := scanCodeKeyboardInput(config.OutputKey, true)
		if err != nil {
			return err
		}
		return sendInputs([]winInput{up})
	}
	_, up, err := s.pressFunctions(config)
	if err != nil {
		return err
	}
	return up()
}

func (s *sendInputInjector) pressFunctions(config Config) (down func() error, up func() error, err error) {
	switch config.Mode {
	case ModeKeyboard:
		return nil, nil, errors.New("keyboard output uses the baseline paired SendInput path")
	case ModeMouseLeft:
		return func() error { return sendInputs([]winInput{mouseInput(mouseeventfLeftDown)}) },
			func() error { return sendInputs([]winInput{mouseInput(mouseeventfLeftUp)}) }, nil
	case ModeMouseRight:
		return func() error { return sendInputs([]winInput{mouseInput(mouseeventfRightDown)}) },
			func() error { return sendInputs([]winInput{mouseInput(mouseeventfRightUp)}) }, nil
	default:
		return nil, nil, fmt.Errorf("invalid input mode %d", config.Mode)
	}
}

func scanCodeKeyboardInput(key uint32, up bool) (winInput, error) {
	extended := KeyIsExtended(key)
	virtualKey := VirtualKey(key)
	foreground := windows.GetForegroundWindow()
	threadID, _, _ := procGetWindowThreadProcessID.Call(uintptr(foreground), 0)
	layout, _, _ := procGetKeyboardLayout.Call(threadID)
	scan, _, _ := procMapVirtualKeyExW.Call(uintptr(virtualKey), mapvkVKToVSCEx, layout)
	if scan == 0 {
		return winInput{}, fmt.Errorf("MapVirtualKeyExW returned no scan code for virtual key 0x%02X", virtualKey)
	}
	flags := uint32(keyeventfScanCode)
	if extended || scan&0xff00 == 0xe000 || scan&0xff00 == 0xe100 {
		flags |= keyeventfExtendedKey
	}
	if up {
		flags |= keyeventfKeyUp
	}
	value := winInput{Type: inputKeyboard}
	*(*uint16)(unsafe.Pointer(&value.Data[2])) = uint16(scan & 0xff)
	*(*uint32)(unsafe.Pointer(&value.Data[4])) = flags
	*(*uintptr)(unsafe.Pointer(&value.Data[16])) = injectionMarker
	return value, nil
}

func emitPressRelease(down, up func() error, hold time.Duration, pause func(time.Duration)) error {
	if err := down(); err != nil {
		return err
	}
	pause(hold)
	return up()
}

func pressDuration(interval time.Duration) time.Duration {
	hold := interval / 2
	if hold < time.Millisecond {
		return time.Millisecond
	}
	if hold > 50*time.Millisecond {
		return 50 * time.Millisecond
	}
	return hold
}

func mouseInput(flags uint32) winInput {
	value := winInput{Type: inputMouse}
	*(*uint32)(unsafe.Pointer(&value.Data[12])) = flags
	*(*uintptr)(unsafe.Pointer(&value.Data[24])) = quickInputExtraInfo
	return value
}

func sendInputs(inputs []winInput) error {
	if len(inputs) == 0 {
		return nil
	}
	sent, _, callErr := procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
	if int(sent) != len(inputs) {
		return fmt.Errorf("SendInput accepted %d of %d events: %w (a higher-integrity foreground process may be blocking input through UIPI)", sent, len(inputs), normalizeCallError(callErr))
	}
	return nil
}

func normalizeCallError(err error) error {
	if err == nil {
		return syscall.EINVAL
	}
	if value, ok := err.(syscall.Errno); ok && value == 0 {
		return syscall.EINVAL
	}
	return err
}
