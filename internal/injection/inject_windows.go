package injection

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	createSuspended    = 0x00000004
	createUnicodeEnv   = 0x00000400
	memCommitReserve   = 0x00003000
	memRelease         = 0x00008000
	pageReadWrite      = 0x00000004
	waitObject0        = 0x00000000
	waitTimeout        = 0x00000102
	th32csSnapModule   = 0x00000008
	th32csSnapModule32 = 0x00000010
)

var (
	injectKernel32         = windows.NewLazySystemDLL("kernel32.dll")
	procVirtualAllocEx     = injectKernel32.NewProc("VirtualAllocEx")
	procVirtualFreeEx      = injectKernel32.NewProc("VirtualFreeEx")
	procCreateRemoteThread = injectKernel32.NewProc("CreateRemoteThread")
	procLoadLibraryW       = injectKernel32.NewProc("LoadLibraryW")
)

func launchSuspendedAndInject(executable, workingDirectory string, arguments []string, dllPath string, timeout time.Duration) (int, error) {
	executableLock, err := lockFileReadOnly(executable)
	if err != nil {
		return 0, fmt.Errorf("lock inspected game executable: %w", err)
	}
	defer windows.CloseHandle(executableLock)
	commandLine := windows.ComposeCommandLine(append([]string{executable}, arguments...))
	commandUTF16, err := syscall.UTF16FromString(commandLine)
	if err != nil {
		return 0, err
	}
	directory, err := windows.UTF16PtrFromString(workingDirectory)
	if err != nil {
		return 0, err
	}
	startup := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	var process windows.ProcessInformation
	if err := windows.CreateProcess(nil, &commandUTF16[0], nil, nil, false, createSuspended|createUnicodeEnv, nil, directory, &startup, &process); err != nil {
		return 0, fmt.Errorf("CreateProcessW suspended: %w", err)
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		_ = windows.TerminateProcess(process.Process, 0xE0000001)
		windows.CloseHandle(process.Thread)
		windows.CloseHandle(process.Process)
		return 0, fmt.Errorf("CreateJobObject: %w", err)
	}
	defer windows.CloseHandle(job)
	jobLimits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	jobLimits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&jobLimits)), uint32(unsafe.Sizeof(jobLimits))); err != nil {
		_ = windows.TerminateProcess(process.Process, 0xE0000001)
		windows.CloseHandle(process.Thread)
		windows.CloseHandle(process.Process)
		return 0, fmt.Errorf("configure injection job: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, process.Process); err != nil {
		_ = windows.TerminateProcess(process.Process, 0xE0000001)
		windows.CloseHandle(process.Thread)
		windows.CloseHandle(process.Process)
		return 0, fmt.Errorf("assign suspended game to injection job: %w", err)
	}
	owned := true
	defer func() {
		if owned {
			_ = windows.TerminateProcess(process.Process, 0xE0000001)
		}
		windows.CloseHandle(process.Thread)
		windows.CloseHandle(process.Process)
	}()
	remoteLoadLibrary := procLoadLibraryW.Addr()
	dllUTF16, err := windows.UTF16FromString(dllPath)
	if err != nil {
		return 0, err
	}
	byteSize := uintptr(len(dllUTF16) * 2)
	remoteMemory, _, callErr := procVirtualAllocEx.Call(uintptr(process.Process), 0, byteSize, memCommitReserve, pageReadWrite)
	if remoteMemory == 0 {
		return 0, fmt.Errorf("VirtualAllocEx: %w", callErr)
	}
	defer procVirtualFreeEx.Call(uintptr(process.Process), remoteMemory, 0, memRelease)
	bytes := unsafe.Slice((*byte)(unsafe.Pointer(&dllUTF16[0])), int(byteSize))
	var written uintptr
	if err := windows.WriteProcessMemory(process.Process, remoteMemory, &bytes[0], byteSize, &written); err != nil || written != byteSize {
		return 0, fmt.Errorf("WriteProcessMemory wrote %d/%d: %w", written, byteSize, err)
	}
	remoteThread, _, callErr := procCreateRemoteThread.Call(uintptr(process.Process), 0, 0, remoteLoadLibrary, remoteMemory, 0, 0)
	if remoteThread == 0 {
		return 0, fmt.Errorf("CreateRemoteThread: %w", callErr)
	}
	thread := windows.Handle(remoteThread)
	defer windows.CloseHandle(thread)
	deadline := time.Now().Add(timeout)
	for {
		wait := uint32(100)
		if remaining := time.Until(deadline); remaining <= 0 {
			return 0, errors.New("remote LoadLibraryW timed out")
		} else if remaining < 100*time.Millisecond {
			wait = uint32(max(1, remaining.Milliseconds()))
		}
		status, err := windows.WaitForSingleObject(thread, wait)
		if err != nil {
			return 0, err
		}
		if status == waitObject0 {
			break
		}
		if status != waitTimeout {
			return 0, fmt.Errorf("unexpected remote thread wait status 0x%08X", status)
		}
	}
	loaded, err := remoteModuleLoaded(process.ProcessId, dllPath)
	if err != nil {
		return 0, err
	}
	if !loaded {
		return 0, errors.New("remote LoadLibraryW completed but the module is absent")
	}
	if _, err := windows.ResumeThread(process.Thread); err != nil {
		return 0, fmt.Errorf("ResumeThread: %w", err)
	}
	jobLimits.BasicLimitInformation.LimitFlags = 0
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&jobLimits)), uint32(unsafe.Sizeof(jobLimits))); err != nil {
		return 0, fmt.Errorf("release successful game from kill-on-close job: %w", err)
	}
	owned = false
	return int(process.ProcessId), nil
}

func remoteModuleLoaded(pid uint32, dllPath string) (bool, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(th32csSnapModule|th32csSnapModule32, pid)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(snapshot)
	want := filepath.Clean(dllPath)
	entry := windows.ModuleEntry32{Size: uint32(unsafe.Sizeof(windows.ModuleEntry32{}))}
	for err = windows.Module32First(snapshot, &entry); err == nil; err = windows.Module32Next(snapshot, &entry) {
		if strings.EqualFold(filepath.Clean(windows.UTF16ToString(entry.ExePath[:])), want) {
			return true, nil
		}
	}
	if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return false, nil
	}
	return false, err
}
