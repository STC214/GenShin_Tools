//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	whKeyboardLL  = 13
	wmKeyDown     = 0x0100
	wmKeyUp       = 0x0101
	wmSysKeyDown  = 0x0104
	wmSysKeyUp    = 0x0105
	wmQuit        = 0x0012
	llkhfExtended = 0x01
	llkhfInjected = 0x10
)

type keyboardHook struct {
	VirtualKey uint32
	ScanCode   uint32
	Flags      uint32
	Time       uint32
	ExtraInfo  uintptr
}

type message struct {
	Window  uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   struct{ X, Y int32 }
}

type eventRecord struct {
	ElapsedMicros int64  `json:"elapsedMicros"`
	Message       string `json:"message"`
	VirtualKey    uint32 `json:"virtualKey"`
	ScanCode      uint32 `json:"scanCode"`
	Flags         uint32 `json:"flags"`
	ExtraInfo     uint64 `json:"extraInfo"`
	Injected      bool   `json:"injected"`
	Extended      bool   `json:"extended"`
	ForegroundPID uint32 `json:"foregroundPid"`
}

type keybdEventRecord struct {
	ElapsedMicros int64  `json:"elapsedMicros"`
	ThreadID      uint32 `json:"threadId"`
	VirtualKey    uint32 `json:"virtualKey"`
	ScanCode      uint32 `json:"scanCode"`
	Flags         uint32 `json:"flags"`
	ExtraInfo     uint32 `json:"extraInfo"`
}

type sendInputRecord struct {
	Type       uint32 `json:"type"`
	VirtualKey uint16 `json:"virtualKey,omitempty"`
	ScanCode   uint16 `json:"scanCode,omitempty"`
	Flags      uint32 `json:"flags,omitempty"`
	Time       uint32 `json:"time,omitempty"`
	ExtraInfo  uint32 `json:"extraInfo,omitempty"`
}

type apiCallRecord struct {
	ElapsedMicros int64             `json:"elapsedMicros"`
	ThreadID      uint32            `json:"threadId"`
	API           string            `json:"api"`
	Arguments     []uint32          `json:"arguments"`
	Inputs        []sendInputRecord `json:"inputs,omitempty"`
}

type report struct {
	SchemaVersion          int                `json:"schemaVersion"`
	StartedLocal           string             `json:"startedLocal"`
	HookInstalledBeforeAHK bool               `json:"hookInstalledBeforeAhk"`
	AHKPath                string             `json:"ahkPath"`
	AHKPID                 int                `json:"ahkPid"`
	DurationMS             int64              `json:"durationMs"`
	Events                 []eventRecord      `json:"events"`
	KeybdEvents            []keybdEventRecord `json:"keybdEvents"`
	APICalls               []apiCallRecord    `json:"apiCalls"`
	DebuggerError          string             `json:"debuggerError,omitempty"`
	Error                  string             `json:"error,omitempty"`
}

var (
	user32                    = windows.NewLazySystemDLL("user32.dll")
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procSetWindowsHookExW     = user32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx   = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx        = user32.NewProc("CallNextHookEx")
	procGetMessageW           = user32.NewProc("GetMessageW")
	procPostThreadMessageW    = user32.NewProc("PostThreadMessageW")
	procMessageBoxW           = user32.NewProc("MessageBoxW")
	procGetForegroundWindow   = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcID = user32.NewProc("GetWindowThreadProcessId")
	procGetModuleHandleW      = kernel32.NewProc("GetModuleHandleW")
	started                   time.Time
	eventsMu                  sync.Mutex
	events                    []eventRecord
)

func main() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	executable, _ := os.Executable()
	outputPath := filepath.Join(filepath.Dir(executable), "GenshinTools-input-probe.json")
	ahkPath := `D:\tools\01_Game_tools\SETAHK\AHK_F.exe`
	result := report{
		SchemaVersion:          3,
		StartedLocal:           time.Now().Format(time.RFC3339),
		HookInstalledBeforeAHK: true,
		AHKPath:                ahkPath,
	}
	if _, err := os.Stat(ahkPath); err != nil {
		result.Error = err.Error()
		writeReport(outputPath, result)
		show("未找到 AHK_F.exe：\n"+ahkPath, "输入探针")
		return
	}

	show(
		"请先关闭 Genshin Tools 和已在运行的 AHK_F.exe，保持原神正在运行。\n\n"+
			"点击“确定”后切回原神；约 3 秒后按住 F 至少 4 秒。"+
			"探针会启动 AHK_F.exe，并在 12 秒后自动完成。",
		"Genshin Tools 输入探针",
	)

	module, _, _ := procGetModuleHandleW.Call(0)
	callback := syscall.NewCallback(keyboardCallback)
	hook, _, callErr := procSetWindowsHookExW.Call(whKeyboardLL, callback, module, 0)
	if hook == 0 {
		result.Error = fmt.Sprintf("SetWindowsHookExW: %v", callErr)
		writeReport(outputPath, result)
		show(result.Error, "输入探针")
		return
	}
	defer procUnhookWindowsHookEx.Call(hook)

	command := exec.Command(ahkPath)
	command.Dir = filepath.Dir(ahkPath)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := command.Start(); err != nil {
		result.Error = err.Error()
		writeReport(outputPath, result)
		show("启动 AHK_F.exe 失败："+err.Error(), "输入探针")
		return
	}
	result.AHKPID = command.Process.Pid
	defer command.Process.Kill()

	threadID := windows.GetCurrentThreadId()
	started = time.Now()
	debugger, debuggerErr := startKeybdDebugger(result.AHKPID, started)
	if debuggerErr != nil {
		result.DebuggerError = debuggerErr.Error()
	}
	go func() {
		time.Sleep(12 * time.Second)
		procPostThreadMessageW.Call(uintptr(threadID), wmQuit, 0, 0)
	}()

	var msg message
	for {
		value, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(value) <= 0 {
			break
		}
	}

	result.DurationMS = time.Since(started).Milliseconds()
	if debugger != nil {
		result.APICalls, debuggerErr = debugger.Stop()
		if debuggerErr != nil {
			result.DebuggerError = debuggerErr.Error()
		}
		for _, call := range result.APICalls {
			if call.API != "keybd_event" || len(call.Arguments) < 4 {
				continue
			}
			result.KeybdEvents = append(result.KeybdEvents, keybdEventRecord{
				ElapsedMicros: call.ElapsedMicros,
				ThreadID:      call.ThreadID,
				VirtualKey:    call.Arguments[0],
				ScanCode:      call.Arguments[1],
				Flags:         call.Arguments[2],
				ExtraInfo:     call.Arguments[3],
			})
		}
	}
	eventsMu.Lock()
	result.Events = append([]eventRecord(nil), events...)
	eventsMu.Unlock()
	writeReport(outputPath, result)
	_ = command.Process.Kill()
	show(
		fmt.Sprintf(
			"记录完成：%d 条 F 钩子事件，%d 次候选输入 API 调用。\n\n结果已保存到：\n%s",
			len(result.Events),
			len(result.APICalls),
			outputPath,
		),
		"输入探针",
	)
	runtime.KeepAlive(callback)
}

func keyboardCallback(code int, messageID, dataPointer uintptr) uintptr {
	if code >= 0 && dataPointer != 0 {
		data := (*keyboardHook)(unsafe.Pointer(dataPointer))
		if data.VirtualKey == 'F' {
			var processID uint32
			foreground, _, _ := procGetForegroundWindow.Call()
			if foreground != 0 {
				procGetWindowThreadProcID.Call(foreground, uintptr(unsafe.Pointer(&processID)))
			}
			eventsMu.Lock()
			events = append(events, eventRecord{
				ElapsedMicros: time.Since(started).Microseconds(),
				Message:       messageName(messageID),
				VirtualKey:    data.VirtualKey,
				ScanCode:      data.ScanCode,
				Flags:         data.Flags,
				ExtraInfo:     uint64(data.ExtraInfo),
				Injected:      data.Flags&llkhfInjected != 0,
				Extended:      data.Flags&llkhfExtended != 0,
				ForegroundPID: processID,
			})
			eventsMu.Unlock()
		}
	}
	value, _, _ := procCallNextHookEx.Call(0, uintptr(code), messageID, dataPointer)
	return value
}

func messageName(value uintptr) string {
	switch value {
	case wmKeyDown:
		return "keydown"
	case wmKeyUp:
		return "keyup"
	case wmSysKeyDown:
		return "syskeydown"
	case wmSysKeyUp:
		return "syskeyup"
	default:
		return fmt.Sprintf("0x%X", value)
	}
}

func writeReport(path string, value report) {
	data, _ := json.MarshalIndent(value, "", "  ")
	_ = os.WriteFile(path, append(data, '\n'), 0o600)
}

func show(text, title string) {
	textPointer, _ := windows.UTF16PtrFromString(text)
	titlePointer, _ := windows.UTF16PtrFromString(title)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(textPointer)), uintptr(unsafe.Pointer(titlePointer)), 0x40)
}
