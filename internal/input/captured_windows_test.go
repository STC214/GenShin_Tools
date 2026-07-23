package input

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"genshintools/internal/platform/win32"

	"golang.org/x/sys/windows"
)

var capturedInput struct {
	keyDown   atomic.Uint64
	keyUp     atomic.Uint64
	leftDown  atomic.Uint64
	leftUp    atomic.Uint64
	rightDown atomic.Uint64
	rightUp   atomic.Uint64
	timesMu   sync.Mutex
	times     []time.Time
}

type capturedResult struct {
	mode          Mode
	expected      time.Duration
	down, up      uint64
	mean, minimum time.Duration
	maximum, p95  time.Duration
	err           error
}

func capturedKeyboardCallback(code int, message, dataPointer uintptr) uintptr {
	if code >= 0 {
		data := (*keyboardHook)(unsafe.Pointer(dataPointer))
		if data != nil && data.ExtraInfo == injectionMarker {
			switch message {
			case wMKeyDown, wMSysKeyDown:
				capturedInput.keyDown.Add(1)
				recordCapturedTime()
			case wMKeyUp, wMSysKeyUp:
				capturedInput.keyUp.Add(1)
			}
			return 1 // Test-owned injected input must not reach other applications.
		}
	}
	value, _, _ := procCallNextHookEx.Call(0, uintptr(code), message, dataPointer)
	return value
}

func capturedMouseCallback(code int, message, dataPointer uintptr) uintptr {
	if code >= 0 {
		data := (*mouseHook)(unsafe.Pointer(dataPointer))
		if data != nil && data.ExtraInfo == injectionMarker {
			switch message {
			case wMLButtonDown:
				capturedInput.leftDown.Add(1)
				recordCapturedTime()
			case wMLButtonUp:
				capturedInput.leftUp.Add(1)
			case wMRButtonDown:
				capturedInput.rightDown.Add(1)
				recordCapturedTime()
			case wMRButtonUp:
				capturedInput.rightUp.Add(1)
			}
			return 1 // Test-owned injected input must not reach other applications.
		}
	}
	value, _, _ := procCallNextHookEx.Call(0, uintptr(code), message, dataPointer)
	return value
}

func recordCapturedTime() {
	capturedInput.timesMu.Lock()
	capturedInput.times = append(capturedInput.times, time.Now())
	capturedInput.timesMu.Unlock()
}

func resetCapturedInput() {
	capturedInput.keyDown.Store(0)
	capturedInput.keyUp.Store(0)
	capturedInput.leftDown.Store(0)
	capturedInput.leftUp.Store(0)
	capturedInput.rightDown.Store(0)
	capturedInput.rightUp.Store(0)
	capturedInput.timesMu.Lock()
	capturedInput.times = nil
	capturedInput.timesMu.Unlock()
}

func capturedFixtureWindowProcedure(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	return win32.DefWindowProc(win32.HWND(hwnd), message, wParam, lParam)
}

func installCapturedForegroundWindow(t *testing.T) func() {
	t.Helper()
	className := fmt.Sprintf("GenshinTools.InputCapture.%d.%d", os.Getpid(), time.Now().UnixNano())
	callback := win32.NewCallback(capturedFixtureWindowProcedure)
	class := win32.WndClassEx{
		Style:     win32.CS_HREDRAW | win32.CS_VREDRAW,
		WndProc:   callback,
		Instance:  win32.ModuleHandle(),
		Cursor:    win32.LoadArrowCursor(),
		ClassName: win32.UTF16(className),
	}
	if err := win32.RegisterClass(&class); err != nil {
		t.Fatal(err)
	}
	hwnd, err := win32.CreateWindow(win32.UTF16(className), win32.UTF16("Genshin Tools input capture"), 0, 0, 160, 80, win32.ModuleHandle())
	if err != nil {
		t.Fatal(err)
	}
	win32.ShowWindow(hwnd, win32.SW_SHOWNORMAL)
	win32.UpdateWindow(hwnd)
	win32.SetForegroundWindow(hwnd)
	deadline := time.Now().Add(time.Second)
	for windows.GetForegroundWindow() != windows.HWND(hwnd) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		win32.SetForegroundWindow(hwnd)
	}
	if windows.GetForegroundWindow() != windows.HWND(hwnd) {
		win32.DestroyWindow(hwnd)
		t.Fatal("capture fixture could not become the foreground window")
	}
	return func() {
		win32.DestroyWindow(hwnd)
		runtime.KeepAlive(callback)
	}
}

func installCaptureHooks(t *testing.T) func() {
	t.Helper()
	module, _, callErr := procGetModuleHandleW.Call(0)
	if module == 0 {
		t.Fatalf("GetModuleHandleW: %v", normalizeCallError(callErr))
	}
	keyboardCallback := syscall.NewCallback(capturedKeyboardCallback)
	mouseCallback := syscall.NewCallback(capturedMouseCallback)
	keyboard, _, callErr := procSetWindowsHookExW.Call(whKeyboardLL, keyboardCallback, module, 0)
	if keyboard == 0 {
		t.Fatalf("install capture keyboard hook: %v", normalizeCallError(callErr))
	}
	mouse, _, callErr := procSetWindowsHookExW.Call(whMouseLL, mouseCallback, module, 0)
	if mouse == 0 {
		procUnhookWindowsHookEx.Call(keyboard)
		t.Fatalf("install capture mouse hook: %v", normalizeCallError(callErr))
	}
	return func() {
		procUnhookWindowsHookEx.Call(mouse)
		procUnhookWindowsHookEx.Call(keyboard)
		runtime.KeepAlive(mouseCallback)
		runtime.KeepAlive(keyboardCallback)
	}
}

func runCaptureMessageLoop(t *testing.T) {
	t.Helper()
	var msg message
	for {
		value, _, callErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		result := int32(value)
		if result == 0 {
			return
		}
		if result == -1 {
			t.Fatalf("capture GetMessageW: %v", normalizeCallError(callErr))
		}
	}
}

func TestCapturedSendInputPairs(t *testing.T) {
	if os.Getenv("GENSHINTOOLS_INPUT_CAPTURE") != "1" {
		t.Skip("set GENSHINTOOLS_INPUT_CAPTURE=1 to run swallowed SendInput capture")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	closeForeground := installCapturedForegroundWindow(t)
	defer closeForeground()
	threadID := windows.GetCurrentThreadId()
	cleanup := installCaptureHooks(t)
	defer cleanup()
	resetCapturedInput()

	const pairs = 100
	result := make(chan error, 1)
	go func() {
		injector, err := newSendInputInjector()
		if err == nil {
			for _, mode := range []Mode{ModeKeyboard, ModeMouseLeft, ModeMouseRight} {
				config := DefaultConfig()
				config.Mode = mode
				for index := 0; index < pairs && err == nil; index++ {
					err = injector.Emit(config)
				}
			}
		}
		result <- err
		procPostThreadMessageW.Call(uintptr(threadID), wMQuit, 0, 0)
	}()
	runCaptureMessageLoop(t)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	got := []uint64{
		capturedInput.keyDown.Load(), capturedInput.keyUp.Load(),
		capturedInput.leftDown.Load(), capturedInput.leftUp.Load(),
		capturedInput.rightDown.Load(), capturedInput.rightUp.Load(),
	}
	for index, count := range got {
		if count != pairs {
			t.Fatalf("captured counter %d = %d, want %d; all=%v", index, count, pairs, got)
		}
	}
}

func TestCapturedNativeEngine(t *testing.T) {
	if os.Getenv("GENSHINTOOLS_INPUT_CAPTURE") != "1" {
		t.Skip("set GENSHINTOOLS_INPUT_CAPTURE=1 to run swallowed native engine capture")
	}
	duration := 2 * time.Second
	if value := os.Getenv("GENSHINTOOLS_INPUT_SOAK_SECONDS"); value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < 1 {
			t.Fatalf("invalid GENSHINTOOLS_INPUT_SOAK_SECONDS %q", value)
		}
		duration = time.Duration(seconds) * time.Second
	}
	intervals := []time.Duration{30 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond}
	if value := os.Getenv("GENSHINTOOLS_INPUT_INTERVAL_MS"); value != "" {
		milliseconds, err := strconv.Atoi(value)
		if err != nil || milliseconds < 1 || milliseconds > 5000 {
			t.Fatalf("invalid GENSHINTOOLS_INPUT_INTERVAL_MS %q", value)
		}
		intervals = []time.Duration{time.Duration(milliseconds) * time.Millisecond}
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	closeForeground := installCapturedForegroundWindow(t)
	defer closeForeground()
	threadID := windows.GetCurrentThreadId()
	cleanup := installCaptureHooks(t)
	defer cleanup()

	results := make(chan []capturedResult, 1)
	go func() {
		native, err := NewNative(nil)
		if err == nil {
			err = native.Start()
		}
		if err != nil {
			results <- []capturedResult{{err: err}}
			procPostThreadMessageW.Call(uintptr(threadID), wMQuit, 0, 0)
			return
		}
		// Keep the real hook chain installed but detach physical user input from
		// this test engine. The test drives enqueue directly so normal mouse use
		// cannot stop a mouse-button soak run.
		activeNative.CompareAndSwap(native, nil)
		native.safetyDisabled.Store(true)
		defer native.Close()
		var measurements []capturedResult
		for _, mode := range []Mode{ModeKeyboard, ModeMouseLeft, ModeMouseRight} {
			for _, interval := range intervals {
				fmt.Printf("CAPTURE START mode=%s interval=%s duration=%s\n", mode, interval, duration)
				resetCapturedInput()
				config := DefaultConfig()
				config.Enabled = true
				config.Mode = mode
				config.Interval = interval
				config.IntervalMS = int(interval / time.Millisecond)
				if err := native.Configure(config); err != nil {
					measurements = append(measurements, capturedResult{mode: mode, expected: interval, err: err})
					break
				}
				event := PhysicalEvent{Down: true}
				switch mode {
				case ModeKeyboard:
					event.Kind, event.Code = EventKey, config.OutputKey
				case ModeMouseLeft:
					event.Kind = EventMouseLeft
				case ModeMouseRight:
					event.Kind = EventMouseRight
				}
				native.enqueue(event)
				time.Sleep(duration)
				event.Down = false
				native.enqueue(event)
				time.Sleep(150 * time.Millisecond)
				measurement := capturedMeasurement(mode, interval)
				measurements = append(measurements, measurement)
				fmt.Printf("CAPTURE DONE mode=%s interval=%s pairs=%d/%d mean=%s max=%s p95=%s\n", mode, interval, measurement.down, measurement.up, measurement.mean, measurement.maximum, measurement.p95)
			}
		}
		results <- measurements
		procPostThreadMessageW.Call(uintptr(threadID), wMQuit, 0, 0)
	}()
	runCaptureMessageLoop(t)
	for _, result := range <-results {
		if result.err != nil {
			t.Fatalf("mode=%s expected=%s: %v", result.mode, result.expected, result.err)
		}
		minimumPairs := uint64(duration / (result.expected + result.expected/2))
		if result.down != result.up || result.down < minimumPairs {
			t.Fatalf("mode=%s expected=%s down/up=%d/%d duration=%s", result.mode, result.expected, result.down, result.up, duration)
		}
		t.Logf("mode=%s expected=%s duration=%s pairs=%d mean=%s min=%s max=%s p95=%s", result.mode, result.expected, duration, result.down, result.mean, result.minimum, result.maximum, result.p95)
		tolerance := result.expected / 5
		if tolerance < 10*time.Millisecond {
			tolerance = 10 * time.Millisecond
		}
		if result.mean < result.expected-tolerance || result.mean > result.expected+tolerance {
			t.Fatalf("mode=%s cadence mean %s outside %s +/- %s", result.mode, result.mean, result.expected, tolerance)
		}
	}
}

func capturedMeasurement(mode Mode, expected time.Duration) (result capturedResult) {
	result.mode, result.expected = mode, expected
	switch mode {
	case ModeKeyboard:
		result.down, result.up = capturedInput.keyDown.Load(), capturedInput.keyUp.Load()
	case ModeMouseLeft:
		result.down, result.up = capturedInput.leftDown.Load(), capturedInput.leftUp.Load()
	case ModeMouseRight:
		result.down, result.up = capturedInput.rightDown.Load(), capturedInput.rightUp.Load()
	}
	capturedInput.timesMu.Lock()
	times := append([]time.Time(nil), capturedInput.times...)
	capturedInput.timesMu.Unlock()
	intervals := make([]time.Duration, 0, max(0, len(times)-1))
	for index := 1; index < len(times); index++ {
		intervals = append(intervals, times[index].Sub(times[index-1]))
	}
	if len(intervals) == 0 {
		return result
	}
	sorted := append([]time.Duration(nil), intervals...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	result.minimum, result.maximum = sorted[0], sorted[len(sorted)-1]
	result.p95 = sorted[(len(sorted)-1)*95/100]
	var total time.Duration
	for _, interval := range intervals {
		total += interval
	}
	result.mean = total / time.Duration(len(intervals))
	return result
}
