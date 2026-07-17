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
	whKeyboardLL = 13
	whMouseLL    = 14
	wMQuit       = 0x0012

	wMKeyDown     = 0x0100
	wMKeyUp       = 0x0101
	wMSysKeyDown  = 0x0104
	wMSysKeyUp    = 0x0105
	wMLButtonDown = 0x0201
	wMLButtonUp   = 0x0202
	wMRButtonDown = 0x0204
	wMRButtonUp   = 0x0205

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

	nativeQueueSize = 256
)

// injectionMarker is deliberately non-zero, uncommon, and limited to 32 bits.
// Mouse low-level hooks can expose only the low 32 bits of dwExtraInfo on some
// Windows paths, while keyboard hooks preserve the full ULONG_PTR value.
// Keeping one 32-bit marker makes self-injected filtering consistent for both.
const injectionMarker uintptr = 0x47544F4C // "GTOL"

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

// winInput mirrors INPUT on both 32-bit and 64-bit Windows. The explicit
// uintptr alignment before Data is what gives x64 its required 40-byte size.
type winInput struct {
	Type uint32
	_    uint32
	Data [32]byte
}

type Native struct {
	engine *Engine

	events      [nativeQueueSize]PhysicalEvent
	head        atomic.Uint32
	tail        atomic.Uint32
	wake        chan struct{}
	done        chan struct{}
	workerDone  chan struct{}
	monitorStop chan struct{}
	monitorDone chan struct{}

	threadID       atomic.Uint32
	started        atomic.Bool
	closed         atomic.Bool
	overflow       atomic.Bool
	safetyDisabled atomic.Bool
	runTarget      atomic.Uintptr

	keyboardCallback uintptr
	mouseCallback    uintptr
	observerMu       sync.RWMutex
	observer         func(PhysicalEvent)
	foreground       func() windows.HWND
}

var activeNative atomic.Pointer[Native]

var (
	inputUser32                  = windows.NewLazySystemDLL("user32.dll")
	inputKernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procSetWindowsHookExW        = inputUser32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx      = inputUser32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx           = inputUser32.NewProc("CallNextHookEx")
	procGetMessageW              = inputUser32.NewProc("GetMessageW")
	procPostThreadMessageW       = inputUser32.NewProc("PostThreadMessageW")
	procSendInput                = inputUser32.NewProc("SendInput")
	procMapVirtualKeyExW         = inputUser32.NewProc("MapVirtualKeyExW")
	procGetForegroundWindow      = inputUser32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessID = inputUser32.NewProc("GetWindowThreadProcessId")
	procGetKeyboardLayout        = inputUser32.NewProc("GetKeyboardLayout")
	procGetModuleHandleW         = inputKernel32.NewProc("GetModuleHandleW")
)

func NewNative(onChange func(Snapshot)) (*Native, error) {
	n := &Native{
		wake:        make(chan struct{}, 1),
		done:        make(chan struct{}),
		workerDone:  make(chan struct{}),
		monitorStop: make(chan struct{}),
		monitorDone: make(chan struct{}),
		foreground:  windows.GetForegroundWindow,
	}
	injector, err := newSendInputInjector()
	if err != nil {
		return nil, err
	}
	engine, err := NewEngine(injector, onChange)
	if err != nil {
		return nil, err
	}
	n.engine = engine
	n.keyboardCallback = syscall.NewCallback(keyboardHookCallback)
	n.mouseCallback = syscall.NewCallback(mouseHookCallback)
	return n, nil
}

func (n *Native) Start() error {
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
	go n.hookThread(ready)
	if err := <-ready; err != nil {
		activeNative.CompareAndSwap(n, nil)
		n.started.Store(false)
		return err
	}
	go n.eventWorker()
	go n.safetyMonitor()
	return nil
}

func (n *Native) Configure(config Config) error { return n.engine.Configure(config) }
func (n *Native) Enable(enabled bool)           { n.engine.Enable(enabled) }
func (n *Native) Snapshot() Snapshot            { return n.engine.Snapshot() }

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

func (n *Native) Close() {
	if !n.closed.CompareAndSwap(false, true) {
		return
	}
	n.engine.Enable(false)
	if n.started.Load() {
		close(n.monitorStop)
		if id := n.threadID.Load(); id != 0 {
			procPostThreadMessageW.Call(uintptr(id), wMQuit, 0, 0)
		}
		<-n.done
		<-n.workerDone
		<-n.monitorDone
	}
	n.engine.Close()
}

func (n *Native) safetyMonitor() {
	defer close(n.monitorDone)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var target windows.HWND
	var runningSince time.Time
	for {
		select {
		case <-n.monitorStop:
			return
		case <-ticker.C:
			snapshot := n.engine.Snapshot()
			if snapshot.State != StateRunning {
				target = 0
				n.runTarget.Store(0)
				runningSince = time.Time{}
				continue
			}
			foreground := n.foreground()
			if target == 0 {
				target = windows.HWND(n.runTarget.Load())
				if target == 0 {
					continue
				}
				runningSince = time.Now()
				continue
			}
			if !n.safetyDisabled.Load() && (foreground == 0 || foreground != target) {
				n.engine.Enable(false)
				continue
			}
			if time.Since(runningSince) > 30*time.Minute {
				n.engine.Fail(errors.New("continuous input exceeded the 30-minute safety limit"))
			}
		}
	}
}

func (n *Native) hookThread(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(n.done)
	defer activeNative.CompareAndSwap(n, nil)

	n.threadID.Store(windows.GetCurrentThreadId())
	module, _, callErr := procGetModuleHandleW.Call(0)
	if module == 0 {
		ready <- fmt.Errorf("GetModuleHandleW: %w", normalizeCallError(callErr))
		return
	}
	keyboard, _, callErr := procSetWindowsHookExW.Call(whKeyboardLL, n.keyboardCallback, module, 0)
	if keyboard == 0 {
		ready <- fmt.Errorf("install keyboard hook: %w", normalizeCallError(callErr))
		return
	}
	defer procUnhookWindowsHookEx.Call(keyboard)
	mouse, _, callErr := procSetWindowsHookExW.Call(whMouseLL, n.mouseCallback, module, 0)
	if mouse == 0 {
		ready <- fmt.Errorf("install mouse hook: %w", normalizeCallError(callErr))
		return
	}
	defer procUnhookWindowsHookEx.Call(mouse)
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
	}
}

func (n *Native) eventWorker() {
	defer close(n.workerDone)
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
		n.observerMu.RLock()
		observer := n.observer
		n.observerMu.RUnlock()
		if observer != nil {
			observer(event)
		}
		before := n.engine.Snapshot().State
		n.engine.Handle(event)
		after := n.engine.Snapshot().State
		if before != StateRunning && after == StateRunning {
			n.runTarget.Store(uintptr(n.foreground()))
		} else if after != StateRunning {
			n.runTarget.Store(0)
		}
	}
	if n.overflow.Swap(false) {
		n.engine.Fail(errors.New("physical input event queue overflowed; input enhancement disabled"))
	}
}

func (n *Native) enqueue(event PhysicalEvent) {
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

func keyboardHookCallback(code int, wParam, lParam uintptr) uintptr {
	if code >= 0 {
		handleKeyboardHook((*keyboardHook)(unsafe.Pointer(lParam)), wParam)
	}
	value, _, _ := procCallNextHookEx.Call(0, uintptr(code), wParam, lParam)
	return value
}

func mouseHookCallback(code int, wParam, lParam uintptr) uintptr {
	if code >= 0 {
		handleMouseHook((*mouseHook)(unsafe.Pointer(lParam)), wParam)
	}
	value, _, _ := procCallNextHookEx.Call(0, uintptr(code), wParam, lParam)
	return value
}

func handleKeyboardHook(data *keyboardHook, message uintptr) {
	if data == nil || data.Flags&llkhfInjected != 0 || data.ExtraInfo == injectionMarker {
		return
	}
	down := message == wMKeyDown || message == wMSysKeyDown
	up := message == wMKeyUp || message == wMSysKeyUp
	if !down && !up {
		return
	}
	if n := activeNative.Load(); n != nil {
		n.enqueue(PhysicalEvent{Kind: EventKey, Code: data.VirtualKey, Down: down})
	}
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
	selfRID      uint32
	mu           sync.Mutex
	lastCheck    time.Time
	lastReport   IntegrityReport
	needsRelease atomic.Bool
}

func newSendInputInjector() (*sendInputInjector, error) {
	level, err := currentIntegrityLevel()
	if err != nil {
		return nil, fmt.Errorf("query current process integrity: %w", err)
	}
	return &sendInputInjector{selfRID: level}, nil
}

func (s *sendInputInjector) Emit(config Config) error {
	if report := s.integrityReport(); report.Blocked {
		return fmt.Errorf("foreground process PID %d has higher integrity (%s) than Genshin Tools (%s); restart Genshin Tools at the same privilege level", report.TargetPID, report.TargetName, report.SelfName)
	}
	inputs, err := inputPair(config)
	if err != nil {
		return err
	}
	s.needsRelease.Store(true)
	if err := sendInputs(inputs[:]); err != nil {
		// A partial SendInput can leave only the down half accepted. Always try
		// the corresponding up before the engine enters Fault.
		if release, releaseErr := releaseInput(config); releaseErr == nil {
			if sendInputs([]winInput{release}) == nil {
				s.needsRelease.Store(false)
			}
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
	input, err := releaseInput(config)
	if err != nil {
		return err
	}
	return sendInputs([]winInput{input})
}

func inputPair(config Config) ([2]winInput, error) {
	var result [2]winInput
	switch config.Mode {
	case ModeKeyboard:
		down, err := keyboardInput(config.OutputKey, false)
		if err != nil {
			return result, err
		}
		up, err := keyboardInput(config.OutputKey, true)
		if err != nil {
			return result, err
		}
		result[0], result[1] = down, up
	case ModeMouseLeft:
		result[0] = mouseInput(mouseeventfLeftDown)
		result[1] = mouseInput(mouseeventfLeftUp)
	case ModeMouseRight:
		result[0] = mouseInput(mouseeventfRightDown)
		result[1] = mouseInput(mouseeventfRightUp)
	default:
		return result, fmt.Errorf("invalid input mode %d", config.Mode)
	}
	return result, nil
}

func releaseInput(config Config) (winInput, error) {
	switch config.Mode {
	case ModeKeyboard:
		return keyboardInput(config.OutputKey, true)
	case ModeMouseLeft:
		return mouseInput(mouseeventfLeftUp), nil
	case ModeMouseRight:
		return mouseInput(mouseeventfRightUp), nil
	default:
		return winInput{}, fmt.Errorf("invalid input mode %d", config.Mode)
	}
}

func keyboardInput(virtualKey uint32, up bool) (winInput, error) {
	foreground, _, _ := procGetForegroundWindow.Call()
	threadID, _, _ := procGetWindowThreadProcessID.Call(foreground, 0)
	layout, _, _ := procGetKeyboardLayout.Call(threadID)
	scan, _, _ := procMapVirtualKeyExW.Call(uintptr(virtualKey), mapvkVKToVSCEx, layout)
	if scan == 0 {
		return winInput{}, fmt.Errorf("MapVirtualKeyExW returned no scan code for virtual key 0x%02X", virtualKey)
	}
	flags := uint32(keyeventfScanCode)
	if scan&0xff00 == 0xe000 || scan&0xff00 == 0xe100 {
		flags |= keyeventfExtendedKey
	}
	if up {
		flags |= keyeventfKeyUp
	}
	value := winInput{Type: inputKeyboard}
	*(*uint16)(unsafe.Pointer(&value.Data[2])) = uint16(scan)
	*(*uint32)(unsafe.Pointer(&value.Data[4])) = flags
	*(*uintptr)(unsafe.Pointer(&value.Data[16])) = injectionMarker
	return value, nil
}

func mouseInput(flags uint32) winInput {
	value := winInput{Type: inputMouse}
	*(*uint32)(unsafe.Pointer(&value.Data[12])) = flags
	*(*uintptr)(unsafe.Pointer(&value.Data[24])) = injectionMarker
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
