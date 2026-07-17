package overlay

import (
	"context"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"genshintools/internal/gamewindow"

	"golang.org/x/sys/windows"
)

var (
	procCreateFixtureOverlay  = overlayUser32.NewProc("CreateWindowExW")
	procDestroyFixtureOverlay = overlayUser32.NewProc("DestroyWindow")
	procShowFixtureOverlay    = overlayUser32.NewProc("ShowWindow")
	procGetWindowLongFixture  = overlayUser32.NewProc("GetWindowLongPtrW")
	procSendMessageFixture    = overlayUser32.NewProc("SendMessageW")
)

func TestNativeWindowIsNoActivateClickThroughAndCloses(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	className, _ := windows.UTF16PtrFromString("STATIC")
	title, _ := windows.UTF16PtrFromString("Genshin Tools S08 Overlay Fixture")
	const wsOverlappedWindowVisible = 0x00CF0000 | 0x10000000
	fixture, _, createErr := procCreateFixtureOverlay.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)), wsOverlappedWindowVisible, 80, 80, 640, 360, 0, 0, 0, 0)
	if fixture == 0 {
		t.Fatalf("CreateWindowEx fixture: %v", createErr)
	}
	defer procDestroyFixtureOverlay.Call(fixture)
	procShowFixtureOverlay.Call(fixture, 5)

	process, err := windows.GetCurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &creation, &exit, &kernel, &user); err != nil {
		t.Fatal(err)
	}
	target := gamewindow.Target{PID: windows.GetCurrentProcessId(), CreationTime: creation.Nanoseconds()}
	window, err := startNativeWindow(target, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	hwnd := window.hwnd.Load()
	if hwnd == 0 {
		t.Fatal("overlay did not publish its HWND")
	}
	styleIndex := int32(-20)
	style, _, _ := procGetWindowLongFixture.Call(hwnd, uintptr(styleIndex))
	want := uintptr(wsExTopmost | wsExTransparent | wsExToolWindow | wsExLayered | wsExNoActivate)
	if style&want != want {
		t.Fatalf("overlay extended style = 0x%X, missing 0x%X", style, want&^style)
	}
	hit, _, _ := procSendMessageFixture.Call(hwnd, wmNCHitTest, 0, 0)
	if hit != ^uintptr(0) {
		t.Fatalf("WM_NCHITTEST = 0x%X, want HTTRANSPARENT", hit)
	}
	activate, _, _ := procSendMessageFixture.Call(hwnd, wmMouseActivate, 0, 0)
	if activate != maNoActivate {
		t.Fatalf("WM_MOUSEACTIVATE = %d, want MA_NOACTIVATE", activate)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := window.close(ctx); err != nil {
		t.Fatal(err)
	}
}
