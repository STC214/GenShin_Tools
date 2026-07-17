package input

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"genshintools/internal/platform/win32"
)

var loopbackCounts struct {
	keyDown   atomic.Uint32
	keyUp     atomic.Uint32
	leftDown  atomic.Uint32
	leftUp    atomic.Uint32
	rightDown atomic.Uint32
	rightUp   atomic.Uint32
}

var loopbackTimes struct {
	sync.Mutex
	down []time.Time
}

func loopbackWindowProcedure(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case win32.WM_KEYDOWN:
		loopbackCounts.keyDown.Add(1)
		recordLoopbackDown()
		return 0
	case win32.WM_KEYUP:
		loopbackCounts.keyUp.Add(1)
		return 0
	case win32.WM_LBUTTONDOWN:
		loopbackCounts.leftDown.Add(1)
		recordLoopbackDown()
		return 0
	case 0x0202:
		loopbackCounts.leftUp.Add(1)
		return 0
	case 0x0204:
		loopbackCounts.rightDown.Add(1)
		recordLoopbackDown()
		return 0
	case 0x0205:
		loopbackCounts.rightUp.Add(1)
		return 0
	case win32.WM_CLOSE:
		win32.DestroyWindow(win32.HWND(hwnd))
		return 0
	case win32.WM_DESTROY:
		win32.PostQuitMessage(0)
		return 0
	}
	return win32.DefWindowProc(win32.HWND(hwnd), message, wParam, lParam)
}

func recordLoopbackDown() {
	loopbackTimes.Lock()
	loopbackTimes.down = append(loopbackTimes.down, time.Now())
	loopbackTimes.Unlock()
}

func resetLoopback() {
	loopbackCounts.keyDown.Store(0)
	loopbackCounts.keyUp.Store(0)
	loopbackCounts.leftDown.Store(0)
	loopbackCounts.leftUp.Store(0)
	loopbackCounts.rightDown.Store(0)
	loopbackCounts.rightUp.Store(0)
	loopbackTimes.Lock()
	loopbackTimes.down = nil
	loopbackTimes.Unlock()
}

func TestSendInputLoopback(t *testing.T) {
	if os.Getenv("GENSHINTOOLS_INPUT_INTEGRATION") != "1" {
		t.Skip("set GENSHINTOOLS_INPUT_INTEGRATION=1 to run the foreground SendInput loopback")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	className := fmt.Sprintf("GenshinTools.InputLoopback.%d", os.Getpid())
	class := win32.WndClassEx{
		Style:     win32.CS_HREDRAW | win32.CS_VREDRAW,
		WndProc:   win32.NewCallback(loopbackWindowProcedure),
		Instance:  win32.ModuleHandle(),
		Cursor:    win32.LoadArrowCursor(),
		ClassName: win32.UTF16(className),
	}
	if err := win32.RegisterClass(&class); err != nil {
		t.Fatal(err)
	}
	hwnd, err := win32.CreateWindow(win32.UTF16(className), win32.UTF16("Genshin Tools input loopback"), 120, 120, 480, 320, win32.ModuleHandle())
	if err != nil {
		t.Fatal(err)
	}
	win32.ShowWindow(hwnd, win32.SW_SHOWNORMAL)
	win32.UpdateWindow(hwnd)
	win32.SetForegroundWindow(hwnd)
	rect, ok := win32.GetWindowRect(hwnd)
	if !ok {
		t.Fatal("GetWindowRect failed")
	}
	if !win32.SetCursorPosition((rect.Left+rect.Right)/2, (rect.Top+rect.Bottom)/2) {
		t.Fatal("SetCursorPos failed")
	}
	if cursor, ok := win32.CursorPosition(); !ok || cursor.X < rect.Left || cursor.X >= rect.Right || cursor.Y < rect.Top || cursor.Y >= rect.Bottom {
		t.Fatalf("cursor %+v is outside window rect %+v", cursor, rect)
	}

	const pairs = 100
	go func() {
		time.Sleep(200 * time.Millisecond)
		injector, createErr := newSendInputInjector()
		if createErr != nil {
			t.Errorf("create injector: %v", createErr)
			win32.PostMessage(hwnd, win32.WM_CLOSE, 0, 0)
			return
		}
		for _, mode := range []Mode{ModeKeyboard, ModeMouseLeft, ModeMouseRight} {
			config := DefaultConfig()
			config.Mode = mode
			for i := 0; i < pairs; i++ {
				if emitErr := injector.Emit(config); emitErr != nil {
					t.Errorf("mode %s pair %d: %v", mode, i, emitErr)
					break
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
		win32.PostMessage(hwnd, win32.WM_CLOSE, 0, 0)
	}()

	var msg win32.Msg
	for {
		result, messageErr := win32.GetMessage(&msg)
		if messageErr != nil {
			t.Fatal(messageErr)
		}
		if result == 0 {
			break
		}
		win32.TranslateMessage(&msg)
		win32.DispatchMessage(&msg)
	}
	got := []uint32{
		loopbackCounts.keyDown.Load(), loopbackCounts.keyUp.Load(),
		loopbackCounts.leftDown.Load(), loopbackCounts.leftUp.Load(),
		loopbackCounts.rightDown.Load(), loopbackCounts.rightUp.Load(),
	}
	for index, count := range got {
		if count != pairs {
			t.Fatalf("event counter %d = %d, want %d; all=%v", index, count, pairs, got)
		}
	}
}

func TestNativeEngineLoopback(t *testing.T) {
	if os.Getenv("GENSHINTOOLS_INPUT_INTEGRATION") != "1" {
		t.Skip("set GENSHINTOOLS_INPUT_INTEGRATION=1 to run the native engine loopback")
	}
	duration := 2 * time.Second
	if value := os.Getenv("GENSHINTOOLS_INPUT_SOAK_SECONDS"); value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < 1 {
			t.Fatalf("invalid GENSHINTOOLS_INPUT_SOAK_SECONDS %q", value)
		}
		duration = time.Duration(seconds) * time.Second
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	resetLoopback()
	className := fmt.Sprintf("GenshinTools.InputEngineLoopback.%d", os.Getpid())
	class := win32.WndClassEx{Style: win32.CS_HREDRAW | win32.CS_VREDRAW, WndProc: win32.NewCallback(loopbackWindowProcedure), Instance: win32.ModuleHandle(), Cursor: win32.LoadArrowCursor(), ClassName: win32.UTF16(className)}
	if err := win32.RegisterClass(&class); err != nil {
		t.Fatal(err)
	}
	hwnd, err := win32.CreateWindow(win32.UTF16(className), win32.UTF16("Genshin Tools engine loopback"), 120, 120, 480, 320, win32.ModuleHandle())
	if err != nil {
		t.Fatal(err)
	}
	win32.ShowWindow(hwnd, win32.SW_SHOWNORMAL)
	win32.UpdateWindow(hwnd)
	win32.SetForegroundWindow(hwnd)
	rect, _ := win32.GetWindowRect(hwnd)
	if !win32.SetCursorPosition((rect.Left+rect.Right)/2, (rect.Top+rect.Bottom)/2) {
		t.Fatal("SetCursorPos failed")
	}

	type result struct {
		mode      Mode
		expected  time.Duration
		down, up  uint32
		intervals []time.Duration
		err       error
	}
	results := make(chan []result, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		native, createErr := NewNative(nil)
		if createErr != nil {
			results <- []result{{err: createErr}}
			win32.PostMessage(hwnd, win32.WM_CLOSE, 0, 0)
			return
		}
		if startErr := native.Start(); startErr != nil {
			results <- []result{{err: startErr}}
			win32.PostMessage(hwnd, win32.WM_CLOSE, 0, 0)
			return
		}
		// Cadence measurement must not depend on other desktop applications
		// stealing foreground focus. Production keeps this safety enabled.
		native.safetyDisabled.Store(true)
		defer native.Close()
		intervalsToTest := []time.Duration{30 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond}
		if value := os.Getenv("GENSHINTOOLS_INPUT_INTERVAL_MS"); value != "" {
			milliseconds, parseErr := strconv.Atoi(value)
			if parseErr != nil || milliseconds < 10 || milliseconds > 5000 {
				results <- []result{{err: fmt.Errorf("invalid GENSHINTOOLS_INPUT_INTERVAL_MS %q", value)}}
				win32.PostMessage(hwnd, win32.WM_CLOSE, 0, 0)
				return
			}
			intervalsToTest = []time.Duration{time.Duration(milliseconds) * time.Millisecond}
		}
		var all []result
		for _, mode := range []Mode{ModeKeyboard, ModeMouseLeft, ModeMouseRight} {
			for _, expected := range intervalsToTest {
				win32.SetForegroundWindow(hwnd)
				win32.SetCursorPosition((rect.Left+rect.Right)/2, (rect.Top+rect.Bottom)/2)
				time.Sleep(100 * time.Millisecond)
				resetLoopback()
				config := DefaultConfig()
				config.Enabled = true
				config.Mode = mode
				config.Interval = expected
				config.IntervalMS = int(expected / time.Millisecond)
				if configureErr := native.Configure(config); configureErr != nil {
					all = append(all, result{mode: mode, err: configureErr})
					break
				}
				event := PhysicalEvent{Down: true}
				switch mode {
				case ModeKeyboard:
					event.Kind, event.Code = EventKey, config.TriggerKey
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
				var down, up uint32
				switch mode {
				case ModeKeyboard:
					down, up = loopbackCounts.keyDown.Load(), loopbackCounts.keyUp.Load()
				case ModeMouseLeft:
					down, up = loopbackCounts.leftDown.Load(), loopbackCounts.leftUp.Load()
				case ModeMouseRight:
					down, up = loopbackCounts.rightDown.Load(), loopbackCounts.rightUp.Load()
				}
				loopbackTimes.Lock()
				times := append([]time.Time(nil), loopbackTimes.down...)
				loopbackTimes.Unlock()
				intervals := make([]time.Duration, 0, max(0, len(times)-1))
				for i := 1; i < len(times); i++ {
					intervals = append(intervals, times[i].Sub(times[i-1]))
				}
				all = append(all, result{mode: mode, expected: expected, down: down, up: up, intervals: intervals})
			}
		}
		results <- all
		win32.PostMessage(hwnd, win32.WM_CLOSE, 0, 0)
	}()

	var msg win32.Msg
	for {
		value, messageErr := win32.GetMessage(&msg)
		if messageErr != nil {
			t.Fatal(messageErr)
		}
		if value == 0 {
			break
		}
		win32.TranslateMessage(&msg)
		win32.DispatchMessage(&msg)
	}
	for _, result := range <-results {
		if result.err != nil {
			t.Fatalf("mode %s: %v", result.mode, result.err)
		}
		minimumPairs := uint32(duration / (result.expected + result.expected/2))
		if result.down != result.up || result.down < minimumPairs {
			t.Fatalf("mode %s down/up = %d/%d for %s", result.mode, result.down, result.up, duration)
		}
		var total time.Duration
		var minimum, maximum time.Duration
		for index, interval := range result.intervals {
			total += interval
			if index == 0 || interval < minimum {
				minimum = interval
			}
			if interval > maximum {
				maximum = interval
			}
		}
		mean := time.Duration(0)
		if len(result.intervals) > 0 {
			mean = total / time.Duration(len(result.intervals))
		}
		t.Logf("mode=%s expected=%s duration=%s pairs=%d mean=%s min=%s max=%s", result.mode, result.expected, duration, result.down, mean, minimum, maximum)
		tolerance := result.expected / 5
		if tolerance < 10*time.Millisecond {
			tolerance = 10 * time.Millisecond
		}
		if mean < result.expected-tolerance || mean > result.expected+tolerance {
			t.Fatalf("mode %s cadence mean %s outside %s +/- %s", result.mode, mean, result.expected, tolerance)
		}
	}
}
