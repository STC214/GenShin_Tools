// Package gamewindow validates a process generation and locates its current
// visible top-level game window.
package gamewindow

import (
	"errors"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Target struct {
	PID          uint32
	CreationTime int64
}

type HWND uintptr

type Rect struct{ Left, Top, Right, Bottom int32 }

var ErrNotFound = errors.New("no verified visible game window")

const (
	gwlStyle = -16
	wsChild  = 0x40000000
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsIconic                 = user32.NewProc("IsIconic")
	procGetWindowLongPtr         = user32.NewProc("GetWindowLongPtrW")
	procGetWindow                = user32.NewProc("GetWindow")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
)

type searchContext struct {
	PID   uint32
	Found HWND
}

var (
	searchContexts sync.Map
	searchToken    atomic.Uint64
)

var enumCallback = windows.NewCallback(func(hwnd, parameter uintptr) uintptr {
	value, exists := searchContexts.Load(uint64(parameter))
	if !exists {
		return 0
	}
	context := value.(*searchContext)
	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != context.PID {
		return 1
	}
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	styleIndex := int32(gwlStyle)
	style, _, _ := procGetWindowLongPtr.Call(hwnd, uintptr(styleIndex))
	owner, _, _ := procGetWindow.Call(hwnd, 4) // GW_OWNER
	var rectangle Rect
	bounded, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rectangle)))
	if visible != 0 && style&wsChild == 0 && owner == 0 && bounded != 0 && rectangle.Right > rectangle.Left && rectangle.Bottom > rectangle.Top {
		context.Found = HWND(hwnd)
		return 0
	}
	return 1
})

func Find(target Target) (HWND, error) {
	if err := Validate(target); err != nil {
		return 0, err
	}
	context := searchContext{PID: target.PID}
	token := searchToken.Add(1)
	searchContexts.Store(token, &context)
	defer searchContexts.Delete(token)
	procEnumWindows.Call(enumCallback, uintptr(token))
	if context.Found == 0 {
		return 0, ErrNotFound
	}
	return context.Found, nil
}

func Validate(target Target) error {
	if target.PID == 0 || target.CreationTime == 0 {
		return ErrNotFound
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, target.PID)
	if err != nil {
		return ErrNotFound
	}
	defer windows.CloseHandle(handle)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil || creation.Nanoseconds() != target.CreationTime {
		return ErrNotFound
	}
	return nil
}

func Bounds(hwnd HWND) (Rect, error) {
	var rectangle Rect
	if ok, _, err := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rectangle))); ok == 0 {
		return Rect{}, err
	}
	return rectangle, nil
}

func IsMinimized(hwnd HWND) bool {
	value, _, _ := procIsIconic.Call(uintptr(hwnd))
	return value != 0
}
