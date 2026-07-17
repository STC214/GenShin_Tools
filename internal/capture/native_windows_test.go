package capture

import (
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"genshintools/internal/gamewindow"

	"golang.org/x/sys/windows"
)

var (
	user32Fixture             = windows.NewLazySystemDLL("user32.dll")
	procCreateWindowExFixture = user32Fixture.NewProc("CreateWindowExW")
	procDestroyWindowFixture  = user32Fixture.NewProc("DestroyWindow")
	procShowWindowFixture     = user32Fixture.NewProc("ShowWindow")
	procUpdateWindowFixture   = user32Fixture.NewProc("UpdateWindow")
)

func TestNativeCaptureUsesVerifiedProcessGenerationAndWritesPNG(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	className, _ := windows.UTF16PtrFromString("STATIC")
	title, _ := windows.UTF16PtrFromString("Genshin Tools S08 Capture Fixture")
	const wsOverlappedWindowVisible = 0x00CF0000 | 0x10000000
	hwnd, _, createErr := procCreateWindowExFixture.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		wsOverlappedWindowVisible,
		64, 64, 360, 220,
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		t.Fatalf("CreateWindowEx fixture: %v", createErr)
	}
	defer procDestroyWindowFixture.Call(hwnd)
	procShowWindowFixture.Call(hwnd, 5)
	procUpdateWindowFixture.Call(hwnd)

	var creation, exit, kernel, user windows.Filetime
	process, err := windows.GetCurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.GetProcessTimes(process, &creation, &exit, &kernel, &user); err != nil {
		t.Fatal(err)
	}
	target := gamewindow.Target{PID: windows.GetCurrentProcessId(), CreationTime: creation.Nanoseconds()}
	if _, err := gamewindow.Find(target); err != nil {
		t.Fatalf("verified fixture window was not found: %v", err)
	}
	if _, err := gamewindow.Find(gamewindow.Target{PID: target.PID, CreationTime: target.CreationTime + int64(time.Second)}); err == nil {
		t.Fatal("stale process generation unexpectedly resolved a window")
	}

	path := filepath.Join(t.TempDir(), "fixture.png")
	if err := (NativeCapturer{}).Capture(target, path); err != nil {
		t.Fatalf("native capture: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	frame, err := png.Decode(file)
	if err != nil {
		t.Fatalf("decode output PNG: %v", err)
	}
	if frame.Bounds().Dx() != 360 || frame.Bounds().Dy() != 220 {
		t.Fatalf("capture bounds = %v, want 360x220", frame.Bounds())
	}
}
