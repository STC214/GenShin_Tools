// Package win32 contains typed, minimal wrappers for Win32 APIs used by the shell.
package win32

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type HWND uintptr
type HINSTANCE uintptr
type HICON uintptr
type HCURSOR uintptr
type HBRUSH uintptr
type HFONT uintptr
type HMENU uintptr
type HDC uintptr

type Point struct{ X, Y int32 }
type Rect struct{ Left, Top, Right, Bottom int32 }

type Msg struct {
	HWnd     HWND
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       Point
	LPrivate uint32
}

type WndClassEx struct {
	CbSize     uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   HINSTANCE
	Icon       HICON
	Cursor     HCURSOR
	Background HBRUSH
	MenuName   *uint16
	ClassName  *uint16
	IconSmall  HICON
}

type PaintStruct struct {
	DC        HDC
	Erase     int32
	Paint     Rect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

type MinMaxInfo struct {
	Reserved     Point
	MaxSize      Point
	MaxPosition  Point
	MinTrackSize Point
	MaxTrackSize Point
}

type MonitorInfo struct {
	Size    uint32
	Monitor Rect
	Work    Rect
	Flags   uint32
}

type NotifyIconData struct {
	Size        uint32
	Window      HWND
	ID          uint32
	Flags       uint32
	CallbackMsg uint32
	Icon        HICON
	Tip         [128]uint16
	State       uint32
	StateMask   uint32
	Info        [256]uint16
	Version     uint32
	InfoTitle   [64]uint16
	InfoFlags   uint32
	GUID        [16]byte
	BalloonIcon HICON
}

type ThreadEntry32 struct {
	Size           uint32
	Usage          uint32
	ThreadID       uint32
	OwnerProcessID uint32
	BasePriority   int32
	DeltaPriority  int32
	Flags          uint32
}

type ResourceSnapshot struct {
	Handles uint32
	Threads uint32
	GDI     uint32
	USER    uint32
}

type openFileName struct {
	Size            uint32
	Owner           HWND
	Instance        HINSTANCE
	Filter          *uint16
	CustomFilter    *uint16
	MaxCustomFilter uint32
	FilterIndex     uint32
	File            *uint16
	MaxFile         uint32
	FileTitle       *uint16
	MaxFileTitle    uint32
	InitialDir      *uint16
	Title           *uint16
	Flags           uint32
	FileOffset      uint16
	FileExtension   uint16
	DefaultExt      *uint16
	CustomData      uintptr
	Hook            uintptr
	TemplateName    *uint16
	Reserved        uintptr
	ReservedValue   uint32
	FlagsEx         uint32
}

type browseInfo struct {
	Owner       HWND
	Root        uintptr
	DisplayName *uint16
	Title       *uint16
	Flags       uint32
	Callback    uintptr
	Parameter   uintptr
	Image       int32
}

const (
	CS_HREDRAW = 0x0002
	CS_VREDRAW = 0x0001

	WS_OVERLAPPEDWINDOW = 0x00CF0000
	WS_CHILD            = 0x40000000
	WS_VISIBLE          = 0x10000000
	WS_TABSTOP          = 0x00010000
	WS_BORDER           = 0x00800000
	ES_AUTOHSCROLL      = 0x0080
	CW_USEDEFAULT       = ^uint32(0x7fffffff)

	SW_HIDE       = 0
	SW_SHOWNORMAL = 1
	SW_RESTORE    = 9

	WM_CREATE           = 0x0001
	WM_DESTROY          = 0x0002
	WM_MOVE             = 0x0003
	WM_SIZE             = 0x0005
	WM_PAINT            = 0x000F
	WM_CLOSE            = 0x0010
	WM_QUERYENDSESSION  = 0x0011
	WM_ENDSESSION       = 0x0016
	WM_GETMINMAXINFO    = 0x0024
	WM_SETFONT          = 0x0030
	WM_ERASEBKGND       = 0x0014
	WM_COMMAND          = 0x0111
	WM_CTLCOLOREDIT     = 0x0133
	WM_SYSCOMMAND       = 0x0112
	WM_MOUSEMOVE        = 0x0200
	WM_LBUTTONDOWN      = 0x0201
	WM_LBUTTONDBLCLK    = 0x0203
	WM_RBUTTONUP        = 0x0205
	WM_KEYDOWN          = 0x0100
	WM_KEYUP            = 0x0101
	WM_HOTKEY           = 0x0312
	WM_POWERBROADCAST   = 0x0218
	WM_WTSSESSIONCHANGE = 0x02B1
	WM_DPICHANGED       = 0x02E0
	WM_APP              = 0x8000

	SIZE_MINIMIZED = 1
	SC_MINIMIZE    = 0xF020

	VK_UP           = 0x26
	VK_DOWN         = 0x28
	VK_RETURN       = 0x0D
	EM_SETLIMITTEXT = 0x00C5
	EM_SETCUEBANNER = 0x1501
	SM_CMONITORS    = 80

	PBT_APMSUSPEND          = 0x0004
	PBT_APMRESUMEAUTOMATIC  = 0x0012
	WTS_SESSION_LOCK        = 0x0007
	WTS_SESSION_UNLOCK      = 0x0008
	NOTIFY_FOR_THIS_SESSION = 0

	DT_LEFT         = 0x0000
	DT_VCENTER      = 0x0004
	DT_SINGLELINE   = 0x0020
	DT_END_ELLIPSIS = 0x00008000

	TRANSPARENT = 1

	SWP_NOZORDER   = 0x0004
	SWP_NOACTIVATE = 0x0010

	MONITOR_DEFAULTTONEAREST = 2

	NIM_ADD              = 0
	NIM_MODIFY           = 1
	NIM_DELETE           = 2
	NIM_SETVERSION       = 4
	NIF_MESSAGE          = 0x0001
	NIF_ICON             = 0x0002
	NIF_TIP              = 0x0004
	NOTIFYICON_VERSION_4 = 4

	MF_STRING       = 0x0000
	TPM_RIGHTBUTTON = 0x0002
	TPM_RETURNCMD   = 0x0100

	TH32CS_SNAPTHREAD = 0x00000004
	GR_GDIOBJECTS     = 0
	GR_USEROBJECTS    = 1

	COINIT_APARTMENTTHREADED      = 0x2
	DWMWA_USE_IMMERSIVE_DARK_MODE = 20
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	wtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")
	comdlg32 = windows.NewLazySystemDLL("comdlg32.dll")
	uxtheme  = windows.NewLazySystemDLL("uxtheme.dll")

	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procGetOpenFileNameW              = comdlg32.NewProc("GetOpenFileNameW")
	procSHBrowseForFolderW            = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListEx         = shell32.NewProc("SHGetPathFromIDListEx")
	procCoTaskMemFree                 = ole32.NewProc("CoTaskMemFree")
	procCommDlgExtendedError          = comdlg32.NewProc("CommDlgExtendedError")
	procSetWindowTheme                = uxtheme.NewProc("SetWindowTheme")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procShowWindow                    = user32.NewProc("ShowWindow")
	procUpdateWindow                  = user32.NewProc("UpdateWindow")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procPostMessageW                  = user32.NewProc("PostMessageW")
	procFindWindowW                   = user32.NewProc("FindWindowW")
	procSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	procIsIconic                      = user32.NewProc("IsIconic")
	procIsWindowVisible               = user32.NewProc("IsWindowVisible")
	procGetWindowRect                 = user32.NewProc("GetWindowRect")
	procGetClientRect                 = user32.NewProc("GetClientRect")
	procSetWindowPos                  = user32.NewProc("SetWindowPos")
	procLoadCursorW                   = user32.NewProc("LoadCursorW")
	procLoadIconW                     = user32.NewProc("LoadIconW")
	procBeginPaint                    = user32.NewProc("BeginPaint")
	procEndPaint                      = user32.NewProc("EndPaint")
	procFillRect                      = user32.NewProc("FillRect")
	procDrawTextW                     = user32.NewProc("DrawTextW")
	procSetWindowTextW                = user32.NewProc("SetWindowTextW")
	procGetWindowTextLengthW          = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW                = user32.NewProc("GetWindowTextW")
	procSendMessageW                  = user32.NewProc("SendMessageW")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procInvalidateRect                = user32.NewProc("InvalidateRect")
	procMonitorFromRect               = user32.NewProc("MonitorFromRect")
	procGetMonitorInfoW               = user32.NewProc("GetMonitorInfoW")
	procGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procRegisterWindowMessageW        = user32.NewProc("RegisterWindowMessageW")
	procRegisterHotKey                = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey              = user32.NewProc("UnregisterHotKey")
	procCreatePopupMenu               = user32.NewProc("CreatePopupMenu")
	procAppendMenuW                   = user32.NewProc("AppendMenuW")
	procTrackPopupMenu                = user32.NewProc("TrackPopupMenu")
	procDestroyMenu                   = user32.NewProc("DestroyMenu")
	procGetCursorPos                  = user32.NewProc("GetCursorPos")
	procSetCursorPos                  = user32.NewProc("SetCursorPos")
	procGetGuiResources               = user32.NewProc("GetGuiResources")

	procCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	procDeleteObject     = gdi32.NewProc("DeleteObject")
	procSetBkMode        = gdi32.NewProc("SetBkMode")
	procSetTextColor     = gdi32.NewProc("SetTextColor")
	procSetBkColor       = gdi32.NewProc("SetBkColor")
	procSelectObject     = gdi32.NewProc("SelectObject")
	procCreateFontW      = gdi32.NewProc("CreateFontW")

	procShellNotifyIconW                 = shell32.NewProc("Shell_NotifyIconW")
	procDwmSetWindowAttribute            = dwmapi.NewProc("DwmSetWindowAttribute")
	procCoInitializeEx                   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize                   = ole32.NewProc("CoUninitialize")
	procWTSRegisterSessionNotification   = wtsapi32.NewProc("WTSRegisterSessionNotification")
	procWTSUnRegisterSessionNotification = wtsapi32.NewProc("WTSUnRegisterSessionNotification")

	procGetModuleHandleW         = kernel32.NewProc("GetModuleHandleW")
	procCreateMutexW             = kernel32.NewProc("CreateMutexW")
	procGetCurrentProcess        = kernel32.NewProc("GetCurrentProcess")
	procGetCurrentProcessId      = kernel32.NewProc("GetCurrentProcessId")
	procGetProcessHandleCount    = kernel32.NewProc("GetProcessHandleCount")
	procCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	procThread32First            = kernel32.NewProc("Thread32First")
	procThread32Next             = kernel32.NewProc("Thread32Next")
)

func UTF16(value string) *uint16 {
	ptr, err := windows.UTF16PtrFromString(value)
	if err != nil {
		panic(err)
	}
	return ptr
}

func CopyUTF16(destination []uint16, value string) {
	encoded, _ := windows.UTF16FromString(value)
	copy(destination, encoded)
}

// SelectExecutable shows the native read-only file picker. Cancellation is not
// an error; selected is false in that case.
func SelectExecutable(owner HWND, initialDirectory string) (path string, selected bool, err error) {
	return selectFile(owner, initialDirectory, "Windows executables (*.exe)", "*.exe", "选择原神游戏主程序", "exe")
}

func SelectPluginPackage(owner HWND, initialDirectory string) (path string, selected bool, err error) {
	return selectFile(owner, initialDirectory, "Genshin Tools plugin packages (*.zip)", "*.zip", "选择本地插件包", "zip")
}

func SelectWaveFile(owner HWND, initialDirectory string) (path string, selected bool, err error) {
	return selectFile(owner, initialDirectory, "Wave audio (*.wav)", "*.wav", "选择启动声音", "wav")
}

var browseCallback = windows.NewCallback(func(hwnd uintptr, message uint32, _ uintptr, parameter uintptr) uintptr {
	const (
		bffmInitialized   = 1
		bffmSetSelectionW = 0x0467
	)
	if message == bffmInitialized && parameter != 0 {
		procSendMessageW.Call(hwnd, bffmSetSelectionW, 1, parameter)
	}
	return 0
})

func SelectFolder(owner HWND, initialDirectory string) (path string, selected bool, err error) {
	display := make([]uint16, 32768)
	title := UTF16("选择截图保存目录")
	var initial *uint16
	if initialDirectory != "" {
		initial = UTF16(initialDirectory)
	}
	info := browseInfo{Owner: owner, DisplayName: &display[0], Title: title, Flags: 0x0001 | 0x0040, Callback: browseCallback, Parameter: uintptr(unsafe.Pointer(initial))}
	item, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&info)))
	if item == 0 {
		return "", false, nil
	}
	defer procCoTaskMemFree.Call(item)
	buffer := make([]uint16, 32768)
	ok, _, pathErr := procSHGetPathFromIDListEx.Call(item, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
	if ok == 0 {
		if pathErr != nil && pathErr != syscall.Errno(0) {
			return "", false, fmt.Errorf("SHGetPathFromIDListEx: %w", pathErr)
		}
		return "", false, errors.New("SHGetPathFromIDListEx failed")
	}
	return windows.UTF16ToString(buffer), true, nil
}

func selectFile(owner HWND, initialDirectory, filterName, pattern, title, defaultExtension string) (path string, selected bool, err error) {
	buffer := make([]uint16, 32768)
	filter := append(syscall.StringToUTF16(filterName), syscall.StringToUTF16(pattern)...)
	filter = append(filter, 0)
	var initial *uint16
	if initialDirectory != "" {
		initial = UTF16(initialDirectory)
	}
	request := openFileName{
		Size:        uint32(unsafe.Sizeof(openFileName{})),
		Owner:       owner,
		Filter:      &filter[0],
		FilterIndex: 1,
		File:        &buffer[0],
		MaxFile:     uint32(len(buffer)),
		InitialDir:  initial,
		Title:       UTF16(title),
		Flags:       0x00080000 | 0x00001000 | 0x00000800 | 0x00000008,
		DefaultExt:  UTF16(defaultExtension),
	}
	value, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&request)))
	if value != 0 {
		return windows.UTF16ToString(buffer), true, nil
	}
	code, _, _ := procCommDlgExtendedError.Call()
	if code != 0 {
		return "", false, fmt.Errorf("GetOpenFileNameW failed with common-dialog error 0x%X", code)
	}
	return "", false, nil
}

func NewCallback(callback any) uintptr { return syscall.NewCallback(callback) }

func ModuleHandle() HINSTANCE { value, _, _ := procGetModuleHandleW.Call(0); return HINSTANCE(value) }
func LoadIcon(instance HINSTANCE, id uintptr) HICON {
	value, _, _ := procLoadIconW.Call(uintptr(instance), id)
	return HICON(value)
}
func LoadArrowCursor() HCURSOR { value, _, _ := procLoadCursorW.Call(0, 32512); return HCURSOR(value) }

func RegisterClass(class *WndClassEx) error {
	class.CbSize = uint32(unsafe.Sizeof(*class))
	value, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(class)))
	if value == 0 {
		return fmt.Errorf("RegisterClassExW: %w", errno(err))
	}
	return nil
}

func CreateWindow(className, title *uint16, x, y, width, height int32, instance HINSTANCE) (HWND, error) {
	value, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)), WS_OVERLAPPEDWINDOW, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0, 0, uintptr(instance), 0)
	if value == 0 {
		return 0, fmt.Errorf("CreateWindowExW: %w", errno(err))
	}
	return HWND(value), nil
}

func CreateControl(className, text string, style uint32, x, y, width, height int32, parent HWND, id uintptr, instance HINSTANCE) (HWND, error) {
	value, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(UTF16(className))), uintptr(unsafe.Pointer(UTF16(text))), uintptr(style), uintptr(x), uintptr(y), uintptr(width), uintptr(height), uintptr(parent), id, uintptr(instance), 0)
	if value == 0 {
		return 0, fmt.Errorf("CreateWindowExW(%s): %w", className, errno(err))
	}
	return HWND(value), nil
}

func DefWindowProc(hwnd HWND, message uint32, wParam, lParam uintptr) uintptr {
	value, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return value
}
func ShowWindow(hwnd HWND, command int32) { procShowWindow.Call(uintptr(hwnd), uintptr(command)) }
func UpdateWindow(hwnd HWND)              { procUpdateWindow.Call(uintptr(hwnd)) }
func DestroyWindow(hwnd HWND)             { procDestroyWindow.Call(uintptr(hwnd)) }
func PostQuitMessage(code int32)          { procPostQuitMessage.Call(uintptr(code)) }
func PostMessage(hwnd HWND, message uint32, wParam, lParam uintptr) bool {
	value, _, _ := procPostMessageW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return value != 0
}

func RegisterHotKey(hwnd HWND, id int32, modifiers, virtualKey uint32) error {
	result, _, err := procRegisterHotKey.Call(uintptr(hwnd), uintptr(id), uintptr(modifiers), uintptr(virtualKey))
	if result == 0 {
		return fmt.Errorf("RegisterHotKey: %w", err)
	}
	return nil
}

func UnregisterHotKey(hwnd HWND, id int32) bool {
	result, _, _ := procUnregisterHotKey.Call(uintptr(hwnd), uintptr(id))
	return result != 0
}

func GetMessage(message *Msg) (int32, error) {
	value, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(message)), 0, 0, 0)
	result := int32(value)
	if result == -1 {
		return result, errno(err)
	}
	return result, nil
}
func TranslateMessage(message *Msg) { procTranslateMessage.Call(uintptr(unsafe.Pointer(message))) }
func DispatchMessage(message *Msg)  { procDispatchMessageW.Call(uintptr(unsafe.Pointer(message))) }

func FindWindow(className string) HWND {
	value, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(UTF16(className))), 0)
	return HWND(value)
}
func SetForegroundWindow(hwnd HWND) { procSetForegroundWindow.Call(uintptr(hwnd)) }
func SetCursorPosition(x, y int32) bool {
	value, _, _ := procSetCursorPos.Call(uintptr(x), uintptr(y))
	return value != 0
}
func CursorPosition() (Point, bool) {
	var point Point
	value, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	return point, value != 0
}
func IsIconic(hwnd HWND) bool { value, _, _ := procIsIconic.Call(uintptr(hwnd)); return value != 0 }
func IsVisible(hwnd HWND) bool {
	value, _, _ := procIsWindowVisible.Call(uintptr(hwnd))
	return value != 0
}
func GetWindowRect(hwnd HWND) (Rect, bool) {
	var rect Rect
	value, _, _ := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rect)))
	return rect, value != 0
}
func GetClientRect(hwnd HWND) Rect {
	var rect Rect
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rect)))
	return rect
}
func SetWindowPos(hwnd HWND, rect Rect, flags uint32) {
	procSetWindowPos.Call(uintptr(hwnd), 0, uintptr(rect.Left), uintptr(rect.Top), uintptr(rect.Right-rect.Left), uintptr(rect.Bottom-rect.Top), uintptr(flags))
}
func SetWindowText(hwnd HWND, value string) {
	procSetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(UTF16(value))))
}

func GetWindowText(hwnd HWND) string {
	length, _, _ := procGetWindowTextLengthW.Call(uintptr(hwnd))
	buffer := make([]uint16, length+1)
	if len(buffer) == 0 {
		return ""
	}
	procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	return windows.UTF16ToString(buffer)
}

func SendMessage(hwnd HWND, message uint32, wParam, lParam uintptr) uintptr {
	value, _, _ := procSendMessageW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return value
}

func SetControlFont(hwnd HWND, font HFONT) { SendMessage(hwnd, WM_SETFONT, uintptr(font), 1) }
func SetTextLimit(hwnd HWND, limit uint32) { SendMessage(hwnd, EM_SETLIMITTEXT, uintptr(limit), 0) }
func SetCueBanner(hwnd HWND, value string) {
	SendMessage(hwnd, EM_SETCUEBANNER, 1, uintptr(unsafe.Pointer(UTF16(value))))
}
func EnableDarkControl(hwnd HWND) {
	procSetWindowTheme.Call(uintptr(hwnd), uintptr(unsafe.Pointer(UTF16("DarkMode_Explorer"))), 0)
}
func MonitorCount() int {
	value, _, _ := procGetSystemMetrics.Call(SM_CMONITORS)
	if int(value) < 1 {
		return 1
	}
	return int(value)
}
func Invalidate(hwnd HWND) { procInvalidateRect.Call(uintptr(hwnd), 0, 0) }

func BeginPaint(hwnd HWND, paint *PaintStruct) HDC {
	value, _, _ := procBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(paint)))
	return HDC(value)
}
func EndPaint(hwnd HWND, paint *PaintStruct) {
	procEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(paint)))
}
func CreateSolidBrush(color uint32) HBRUSH {
	value, _, _ := procCreateSolidBrush.Call(uintptr(color))
	return HBRUSH(value)
}
func DeleteObject(object uintptr) {
	if object != 0 {
		procDeleteObject.Call(object)
	}
}
func FillRect(dc HDC, rect *Rect, brush HBRUSH) {
	procFillRect.Call(uintptr(dc), uintptr(unsafe.Pointer(rect)), uintptr(brush))
}
func SetTransparentBackground(dc HDC)   { procSetBkMode.Call(uintptr(dc), TRANSPARENT) }
func SetTextColor(dc HDC, color uint32) { procSetTextColor.Call(uintptr(dc), uintptr(color)) }
func SetBackgroundColor(dc HDC, color uint32) {
	procSetBkColor.Call(uintptr(dc), uintptr(color))
}
func SelectObject(dc HDC, object uintptr) uintptr {
	value, _, _ := procSelectObject.Call(uintptr(dc), object)
	return value
}
func DrawText(dc HDC, text string, rect *Rect, format uint32) {
	encoded, _ := windows.UTF16FromString(text)
	procDrawTextW.Call(uintptr(dc), uintptr(unsafe.Pointer(&encoded[0])), uintptr(len(encoded)-1), uintptr(unsafe.Pointer(rect)), uintptr(format))
}
func CreateFont(height int32, weight int32, face string) HFONT {
	value, _, _ := procCreateFontW.Call(uintptr(height), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(UTF16(face))))
	return HFONT(value)
}

func EnablePerMonitorV2() { procSetProcessDpiAwarenessContext.Call(^uintptr(3)) }
func DPIForWindow(hwnd HWND) uint32 {
	value, _, _ := procGetDpiForWindow.Call(uintptr(hwnd))
	if value == 0 {
		return 96
	}
	return uint32(value)
}
func EnableDarkTitleBar(hwnd HWND) {
	enabled := int32(1)
	result, _, _ := procDwmSetWindowAttribute.Call(uintptr(hwnd), DWMWA_USE_IMMERSIVE_DARK_MODE, uintptr(unsafe.Pointer(&enabled)), unsafe.Sizeof(enabled))
	if result != 0 {
		procDwmSetWindowAttribute.Call(uintptr(hwnd), 19, uintptr(unsafe.Pointer(&enabled)), unsafe.Sizeof(enabled))
	}
}

func WorkAreaFor(rect Rect) Rect {
	monitor, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&rect)), MONITOR_DEFAULTTONEAREST)
	info := MonitorInfo{Size: uint32(unsafe.Sizeof(MonitorInfo{}))}
	if monitor != 0 {
		value, _, _ := procGetMonitorInfoW.Call(monitor, uintptr(unsafe.Pointer(&info)))
		if value != 0 {
			return info.Work
		}
	}
	return Rect{Left: 0, Top: 0, Right: 1920, Bottom: 1080}
}

func RegisterWindowMessage(name string) uint32 {
	value, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(UTF16(name))))
	return uint32(value)
}
func AddTrayIcon(data *NotifyIconData) bool {
	data.Size = uint32(unsafe.Sizeof(*data))
	value, _, _ := procShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(data)))
	if value == 0 {
		return false
	}
	data.Version = NOTIFYICON_VERSION_4
	procShellNotifyIconW.Call(NIM_SETVERSION, uintptr(unsafe.Pointer(data)))
	return true
}
func DeleteTrayIcon(data *NotifyIconData) {
	procShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(data)))
}

func ShowTrayMenu(hwnd HWND, showID, exitID uintptr) uintptr {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return 0
	}
	defer procDestroyMenu.Call(menu)
	procAppendMenuW.Call(menu, MF_STRING, showID, uintptr(unsafe.Pointer(UTF16("显示主窗口"))))
	procAppendMenuW.Call(menu, MF_STRING, exitID, uintptr(unsafe.Pointer(UTF16("退出"))))
	var point Point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	SetForegroundWindow(hwnd)
	command, _, _ := procTrackPopupMenu.Call(menu, TPM_RIGHTBUTTON|TPM_RETURNCMD, uintptr(point.X), uintptr(point.Y), 0, uintptr(hwnd), 0)
	return command
}

func InitializeCOM() error {
	result, _, _ := procCoInitializeEx.Call(0, COINIT_APARTMENTTHREADED)
	if int32(result) < 0 {
		return fmt.Errorf("CoInitializeEx HRESULT 0x%08X", uint32(result))
	}
	return nil
}
func UninitializeCOM() { procCoUninitialize.Call() }

func RegisterSessionNotifications(hwnd HWND) bool {
	value, _, _ := procWTSRegisterSessionNotification.Call(uintptr(hwnd), NOTIFY_FOR_THIS_SESSION)
	return value != 0
}

func UnregisterSessionNotifications(hwnd HWND) {
	procWTSUnRegisterSessionNotification.Call(uintptr(hwnd))
}

func CreateSingleInstanceMutex(name string) (windows.Handle, bool, error) {
	value, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(UTF16(name))))
	if value == 0 {
		return 0, false, errno(err)
	}
	return windows.Handle(value), errors.Is(err, windows.ERROR_ALREADY_EXISTS), nil
}

func SnapshotResources() ResourceSnapshot {
	process, _, _ := procGetCurrentProcess.Call()
	var handles uint32
	procGetProcessHandleCount.Call(process, uintptr(unsafe.Pointer(&handles)))
	gdi, _, _ := procGetGuiResources.Call(process, GR_GDIOBJECTS)
	user, _, _ := procGetGuiResources.Call(process, GR_USEROBJECTS)
	pid, _, _ := procGetCurrentProcessId.Call()
	var threads uint32
	snapshot, _, _ := procCreateToolhelp32Snapshot.Call(TH32CS_SNAPTHREAD, 0)
	if snapshot != ^uintptr(0) {
		entry := ThreadEntry32{Size: uint32(unsafe.Sizeof(ThreadEntry32{}))}
		value, _, _ := procThread32First.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
		for value != 0 {
			if entry.OwnerProcessID == uint32(pid) {
				threads++
			}
			value, _, _ = procThread32Next.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
		}
		windows.CloseHandle(windows.Handle(snapshot))
	}
	return ResourceSnapshot{Handles: handles, Threads: threads, GDI: uint32(gdi), USER: uint32(user)}
}

func CloseHandle(handle windows.Handle) { _ = windows.CloseHandle(handle) }

func Color(r, g, b byte) uint32           { return uint32(r) | uint32(g)<<8 | uint32(b)<<16 }
func Scale(value int32, dpi uint32) int32 { return value * int32(dpi) / 96 }

func errno(err error) error {
	if err == nil {
		return syscall.EINVAL
	}
	if value, ok := err.(syscall.Errno); ok && value == 0 {
		return syscall.EINVAL
	}
	return err
}
