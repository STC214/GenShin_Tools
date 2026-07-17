package input

import (
	"context"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const createWaitableTimerHighResolution = 0x00000002

type cadence interface {
	Wait() (bool, error)
	Close()
}

type waitableCadence struct {
	timer      windows.Handle
	cancel     windows.Handle
	stopNotify func() bool
	closeOnce  sync.Once
}

var (
	cadenceKernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procCreateWaitableTimerExW = cadenceKernel32.NewProc("CreateWaitableTimerExW")
	procCreateWaitableTimerW   = cadenceKernel32.NewProc("CreateWaitableTimerW")
	procSetWaitableTimer       = cadenceKernel32.NewProc("SetWaitableTimer")
	procCancelWaitableTimer    = cadenceKernel32.NewProc("CancelWaitableTimer")
)

func newCadence(ctx context.Context, interval time.Duration) (cadence, error) {
	if interval <= 0 {
		return nil, fmt.Errorf("interval must be positive")
	}
	timer, _, callErr := procCreateWaitableTimerExW.Call(0, 0, createWaitableTimerHighResolution, windows.TIMER_ALL_ACCESS)
	if timer == 0 {
		// High-resolution timers arrived in newer Windows 10 builds. The
		// fallback preserves function on older supported systems.
		timer, _, callErr = procCreateWaitableTimerW.Call(0, 0, 0)
	}
	if timer == 0 {
		return nil, fmt.Errorf("CreateWaitableTimer: %w", normalizeCallError(callErr))
	}
	cancel, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(windows.Handle(timer))
		return nil, fmt.Errorf("CreateEvent: %w", err)
	}
	dueTime := -interval.Nanoseconds() / 100
	if dueTime == 0 {
		dueTime = -1
	}
	periodMS := interval.Milliseconds()
	if periodMS < 1 {
		periodMS = 1
	}
	value, _, callErr := procSetWaitableTimer.Call(timer, uintptr(unsafe.Pointer(&dueTime)), uintptr(periodMS), 0, 0, 0)
	if value == 0 {
		windows.CloseHandle(cancel)
		windows.CloseHandle(windows.Handle(timer))
		return nil, fmt.Errorf("SetWaitableTimer: %w", normalizeCallError(callErr))
	}
	result := &waitableCadence{timer: windows.Handle(timer), cancel: cancel}
	result.stopNotify = context.AfterFunc(ctx, func() { _ = windows.SetEvent(cancel) })
	return result, nil
}

func (c *waitableCadence) Wait() (bool, error) {
	result, err := windows.WaitForMultipleObjects([]windows.Handle{c.timer, c.cancel}, false, windows.INFINITE)
	if err != nil {
		return false, fmt.Errorf("WaitForMultipleObjects: %w", err)
	}
	if result == windows.WAIT_OBJECT_0 {
		return true, nil
	}
	if result == windows.WAIT_OBJECT_0+1 {
		return false, nil
	}
	return false, fmt.Errorf("WaitForMultipleObjects returned unexpected index %#x", result)
}

func (c *waitableCadence) Close() {
	c.closeOnce.Do(func() {
		if c.stopNotify != nil {
			c.stopNotify()
		}
		procCancelWaitableTimer.Call(uintptr(c.timer))
		windows.CloseHandle(c.cancel)
		windows.CloseHandle(c.timer)
	})
}
