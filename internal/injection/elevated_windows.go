package injection

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
)

type shellExecuteInfo struct {
	Size       uint32
	Mask       uint32
	Window     uintptr
	Verb       *uint16
	File       *uint16
	Parameters *uint16
	Directory  *uint16
	Show       int32
	Instance   uintptr
	IDList     uintptr
	Class      *uint16
	ClassKey   uintptr
	HotKey     uint32
	Icon       uintptr
	Process    windows.Handle
}

var procShellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

func runElevatedHelper(ctx context.Context, helperPath, requestPath string) error {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(helperPath)
	if err != nil {
		return err
	}
	parameters, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{"--request", requestPath}))
	if err != nil {
		return err
	}
	directory, err := windows.UTF16PtrFromString(filepath.Dir(helperPath))
	if err != nil {
		return err
	}
	info := shellExecuteInfo{Size: uint32(unsafe.Sizeof(shellExecuteInfo{})), Mask: seeMaskNoCloseProcess | seeMaskNoAsync, Verb: verb, File: file, Parameters: parameters, Directory: directory, Show: 0}
	ok, _, callErr := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && errno == windows.ERROR_CANCELLED {
			return errors.New("administrator authorization was canceled")
		}
		return fmt.Errorf("ShellExecuteExW runas: %w", callErr)
	}
	if info.Process == 0 {
		return errors.New("elevated helper returned no process handle")
	}
	defer windows.CloseHandle(info.Process)
	for {
		select {
		case <-ctx.Done():
			_ = windows.TerminateProcess(info.Process, 0xE0000002)
			_, _ = windows.WaitForSingleObject(info.Process, 2000)
			return ctx.Err()
		default:
		}
		status, err := windows.WaitForSingleObject(info.Process, 100)
		if err != nil {
			return err
		}
		if status == waitObject0 {
			var exitCode uint32
			if err := windows.GetExitCodeProcess(info.Process, &exitCode); err != nil {
				return err
			}
			if exitCode != 0 {
				return fmt.Errorf("elevated helper exited with code %d", exitCode)
			}
			return nil
		}
		if status != waitTimeout {
			return fmt.Errorf("unexpected helper wait status 0x%08X", status)
		}
	}
}
