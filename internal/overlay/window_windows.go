package overlay

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"genshintools/internal/gamewindow"

	"golang.org/x/sys/windows"
)

const (
	wsPopup          = 0x80000000
	wsExTopmost      = 0x00000008
	wsExTransparent  = 0x00000020
	wsExToolWindow   = 0x00000080
	wsExLayered      = 0x00080000
	wsExNoActivate   = 0x08000000
	wmDestroy        = 0x0002
	wmPaint          = 0x000F
	wmClose          = 0x0010
	wmTimer          = 0x0113
	wmNCHitTest      = 0x0084
	wmMouseActivate  = 0x0021
	htTransparent    = -1
	maNoActivate     = 3
	swHide           = 0
	swShowNoActivate = 8
	swpNoSize        = 0x0001
	swpNoActivate    = 0x0010
	swpShowWindow    = 0x0040
	lwaAlpha         = 0x00000002
	dtLeft           = 0x0000
	dtSingleLine     = 0x0020
	transparentBK    = 1
)

type point struct{ X, Y int32 }
type message struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}
type wndClassEx struct {
	Size, Style                        uint32
	WndProc                            uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	IconSmall                          uintptr
}
type paintStruct struct {
	DC                 uintptr
	Erase              int32
	Paint              gamewindow.Rect
	Restore, IncUpdate int32
	Reserved           [32]byte
}

var (
	overlayUser32                         = windows.NewLazySystemDLL("user32.dll")
	overlayGDI32                          = windows.NewLazySystemDLL("gdi32.dll")
	overlayKernel32                       = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExOverlay            = overlayUser32.NewProc("RegisterClassExW")
	procUnregisterClassOverlay            = overlayUser32.NewProc("UnregisterClassW")
	procCreateWindowExOverlay             = overlayUser32.NewProc("CreateWindowExW")
	procDefWindowProcOverlay              = overlayUser32.NewProc("DefWindowProcW")
	procDestroyWindowOverlay              = overlayUser32.NewProc("DestroyWindow")
	procShowWindowOverlay                 = overlayUser32.NewProc("ShowWindow")
	procSetWindowPosOverlay               = overlayUser32.NewProc("SetWindowPos")
	procSetLayeredWindowAttributesOverlay = overlayUser32.NewProc("SetLayeredWindowAttributes")
	procGetMessageOverlay                 = overlayUser32.NewProc("GetMessageW")
	procTranslateMessageOverlay           = overlayUser32.NewProc("TranslateMessage")
	procDispatchMessageOverlay            = overlayUser32.NewProc("DispatchMessageW")
	procPostMessageOverlay                = overlayUser32.NewProc("PostMessageW")
	procPostQuitMessageOverlay            = overlayUser32.NewProc("PostQuitMessage")
	procSetTimerOverlay                   = overlayUser32.NewProc("SetTimer")
	procKillTimerOverlay                  = overlayUser32.NewProc("KillTimer")
	procInvalidateRectOverlay             = overlayUser32.NewProc("InvalidateRect")
	procBeginPaintOverlay                 = overlayUser32.NewProc("BeginPaint")
	procEndPaintOverlay                   = overlayUser32.NewProc("EndPaint")
	procFillRectOverlay                   = overlayUser32.NewProc("FillRect")
	procDrawTextOverlay                   = overlayUser32.NewProc("DrawTextW")
	procSetTextColorOverlay               = overlayGDI32.NewProc("SetTextColor")
	procSetBkModeOverlay                  = overlayGDI32.NewProc("SetBkMode")
	procSelectObjectOverlay               = overlayGDI32.NewProc("SelectObject")
	procCreateSolidBrushOverlay           = overlayGDI32.NewProc("CreateSolidBrush")
	procCreateFontOverlay                 = overlayGDI32.NewProc("CreateFontW")
	procDeleteObjectOverlay               = overlayGDI32.NewProc("DeleteObject")
	procGetDpiForWindowOverlay            = overlayUser32.NewProc("GetDpiForWindow")
	procGetModuleHandleOverlay            = overlayKernel32.NewProc("GetModuleHandleW")
	overlayWindows                        sync.Map
	overlayGeneration                     atomic.Uint64
)

var overlayWndProc = windows.NewCallback(func(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	value, exists := overlayWindows.Load(hwnd)
	if !exists {
		result, _, _ := procDefWindowProcOverlay.Call(hwnd, uintptr(msg), wParam, lParam)
		return result
	}
	window := value.(*nativeWindow)
	switch msg {
	case wmPaint:
		window.paint(hwnd)
		return 0
	case wmTimer:
		window.reposition(hwnd)
		return 0
	case wmNCHitTest:
		return ^uintptr(0)
	case wmMouseActivate:
		return maNoActivate
	case wmClose:
		procDestroyWindowOverlay.Call(hwnd)
		return 0
	case wmDestroy:
		procKillTimerOverlay.Call(hwnd, 1)
		overlayWindows.Delete(hwnd)
		window.hwnd.Store(0)
		procPostQuitMessageOverlay.Call(0)
		return 0
	}
	result, _, _ := procDefWindowProcOverlay.Call(hwnd, uintptr(msg), wParam, lParam)
	return result
})

type nativeWindow struct {
	target    gamewindow.Target
	config    Config
	stats     atomic.Pointer[Stats]
	hwnd      atomic.Uintptr
	ready     chan error
	done      chan struct{}
	missing   int
	dpi       uint32
	font      uintptr
	brush     uintptr
	className string
	closing   atomic.Bool
}

func startNativeWindow(target gamewindow.Target, config Config) (*nativeWindow, error) {
	window := &nativeWindow{target: target, config: config, ready: make(chan error, 1), done: make(chan struct{}), className: fmt.Sprintf("GenshinTools.Overlay.%d.%d", windows.GetCurrentProcessId(), overlayGeneration.Add(1))}
	go window.run()
	select {
	case err := <-window.ready:
		if err != nil {
			return nil, err
		}
		return window, nil
	case <-time.After(1500 * time.Millisecond):
		window.closing.Store(true)
		window.requestClose()
		return nil, errors.New("overlay window startup timed out")
	}
}

func (window *nativeWindow) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(window.done)
	instance, _, _ := procGetModuleHandleOverlay.Call(0)
	className, _ := windows.UTF16PtrFromString(window.className)
	class := wndClassEx{Size: uint32(unsafe.Sizeof(wndClassEx{})), WndProc: overlayWndProc, Instance: instance, ClassName: className}
	if atom, _, err := procRegisterClassExOverlay.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		window.ready <- fmt.Errorf("RegisterClassEx overlay: %w", err)
		return
	}
	defer procUnregisterClassOverlay.Call(uintptr(unsafe.Pointer(className)), instance)
	title, _ := windows.UTF16PtrFromString("Genshin Tools Performance Overlay")
	exStyle := uintptr(wsExTopmost | wsExTransparent | wsExToolWindow | wsExLayered | wsExNoActivate)
	hwnd, _, err := procCreateWindowExOverlay.Call(exStyle, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)), wsPopup, 0, 0, 260, 96, 0, 0, instance, 0)
	if hwnd == 0 {
		window.ready <- fmt.Errorf("CreateWindowEx overlay: %w", err)
		return
	}
	window.hwnd.Store(hwnd)
	overlayWindows.Store(hwnd, window)
	if window.closing.Load() {
		window.ready <- errors.New("overlay window startup canceled")
		overlayWindows.Delete(hwnd)
		window.hwnd.Store(0)
		procDestroyWindowOverlay.Call(hwnd)
		return
	}
	window.brush, _, _ = procCreateSolidBrushOverlay.Call(0x00251D19)
	procSetLayeredWindowAttributesOverlay.Call(hwnd, 0, 220, lwaAlpha)
	window.reposition(hwnd)
	procSetTimerOverlay.Call(hwnd, 1, 500, 0)
	window.ready <- nil
	var msg message
	for {
		result, _, callErr := procGetMessageOverlay.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) == -1 {
			_ = callErr
			break
		}
		if result == 0 {
			break
		}
		procTranslateMessageOverlay.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageOverlay.Call(uintptr(unsafe.Pointer(&msg)))
	}
	if hwnd := window.hwnd.Swap(0); hwnd != 0 {
		overlayWindows.Delete(hwnd)
		procDestroyWindowOverlay.Call(hwnd)
	}
	if window.font != 0 {
		procDeleteObjectOverlay.Call(window.font)
	}
	if window.brush != 0 {
		procDeleteObjectOverlay.Call(window.brush)
	}
}

func (window *nativeWindow) update(stats Stats) {
	copy := stats
	window.stats.Store(&copy)
	if hwnd := window.hwnd.Load(); hwnd != 0 {
		procInvalidateRectOverlay.Call(hwnd, 0, 0)
	}
}

func (window *nativeWindow) close(ctx context.Context) error {
	window.requestClose()
	select {
	case <-window.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (window *nativeWindow) requestClose() {
	window.closing.Store(true)
	if hwnd := window.hwnd.Load(); hwnd != 0 {
		procPostMessageOverlay.Call(hwnd, wmClose, 0, 0)
	}
}

func (window *nativeWindow) reposition(hwnd uintptr) {
	target, err := gamewindow.Find(window.target)
	if err != nil || gamewindow.IsMinimized(target) {
		window.missing++
		procShowWindowOverlay.Call(hwnd, swHide)
		if window.missing >= 10 {
			procDestroyWindowOverlay.Call(hwnd)
		}
		return
	}
	window.missing = 0
	rectangle, err := gamewindow.Bounds(target)
	if err != nil {
		procShowWindowOverlay.Call(hwnd, swHide)
		return
	}
	dpi, _, _ := procGetDpiForWindowOverlay.Call(uintptr(target))
	if dpi == 0 {
		dpi = 96
	}
	width, height := int32(260*dpi/96), int32(96*dpi/96)
	if uint32(dpi) != window.dpi {
		window.dpi = uint32(dpi)
		if window.font != 0 {
			procDeleteObjectOverlay.Call(window.font)
		}
		face, _ := windows.UTF16PtrFromString("Segoe UI")
		window.font, _, _ = procCreateFontOverlay.Call(uintptr(int32(-16*int32(dpi)/96)), 0, 0, 0, 600, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(face)))
	}
	x := rectangle.Left + int32(window.config.OffsetX)*int32(dpi)/96
	y := rectangle.Top + int32(window.config.OffsetY)*int32(dpi)/96
	const hwndTopmost = ^uintptr(0)
	procSetWindowPosOverlay.Call(hwnd, hwndTopmost, uintptr(x), uintptr(y), uintptr(width), uintptr(height), swpNoActivate|swpShowWindow)
	procShowWindowOverlay.Call(hwnd, swShowNoActivate)
}

func (window *nativeWindow) paint(hwnd uintptr) {
	var paint paintStruct
	dc, _, _ := procBeginPaintOverlay.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
	if dc == 0 {
		return
	}
	defer procEndPaintOverlay.Call(hwnd, uintptr(unsafe.Pointer(&paint)))
	rect := gamewindow.Rect{Left: 0, Top: 0, Right: int32(260 * max(window.dpi, 96) / 96), Bottom: int32(96 * max(window.dpi, 96) / 96)}
	procFillRectOverlay.Call(dc, uintptr(unsafe.Pointer(&rect)), window.brush)
	procSetBkModeOverlay.Call(dc, transparentBK)
	procSetTextColorOverlay.Call(dc, 0x00F2EEE8)
	if window.font != 0 {
		old, _, _ := procSelectObjectOverlay.Call(dc, window.font)
		defer procSelectObjectOverlay.Call(dc, old)
	}
	stats := window.stats.Load()
	lines := metricLines(window.config, stats)
	lineHeight := int32(25 * max(window.dpi, 96) / 96)
	for index, line := range lines {
		lineRect := rect
		lineRect.Left += int32(14 * max(window.dpi, 96) / 96)
		lineRect.Top = int32(10*max(window.dpi, 96)/96) + int32(index)*lineHeight
		lineRect.Bottom = lineRect.Top + lineHeight
		pointer, _ := windows.UTF16PtrFromString(line)
		procDrawTextOverlay.Call(dc, uintptr(unsafe.Pointer(pointer)), ^uintptr(0), uintptr(unsafe.Pointer(&lineRect)), dtLeft|dtSingleLine)
	}
}

func metricLines(config Config, stats *Stats) []string {
	format := func(label string, value float64, valid bool, suffix string) string {
		if !valid {
			return label + "  N/A"
		}
		return fmt.Sprintf("%s  %.1f%s", label, value, suffix)
	}
	var lines []string
	if config.ShowFPS {
		lines = append(lines, format("FPS", value(stats, func(s *Stats) float64 { return s.FPS }), stats != nil && stats.FPSValid, ""))
	}
	if config.ShowCPU {
		lines = append(lines, format("CPU", value(stats, func(s *Stats) float64 { return s.CPU }), stats != nil && stats.CPUValid, " %"))
	}
	if config.ShowGPU {
		lines = append(lines, format("GPU", value(stats, func(s *Stats) float64 { return s.GPU }), stats != nil && stats.GPUValid, " %"))
	}
	return lines
}

func value(stats *Stats, get func(*Stats) float64) float64 {
	if stats == nil {
		return 0
	}
	return get(stats)
}
