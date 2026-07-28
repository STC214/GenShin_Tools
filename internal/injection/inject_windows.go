package injection

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
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

func launchSuspendedAndInject(executable, workingDirectory string, arguments []string, dllPaths []string, timeout time.Duration) (int, error) {
	if len(dllPaths) == 0 || len(dllPaths) > 32 {
		return 0, errors.New("injection requires 1..32 module paths")
	}
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
	deadline := time.Now().Add(timeout)
	for _, dllPath := range dllPaths {
		if err := injectRemoteDLL(process.Process, process.ProcessId, dllPath, deadline); err != nil {
			return 0, fmt.Errorf("inject %s: %w", filepath.Base(dllPath), err)
		}
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

func injectRemoteDLL(process windows.Handle, processID uint32, dllPath string, deadline time.Time) error {
	remoteLoadLibrary := procLoadLibraryW.Addr()
	dllUTF16, err := windows.UTF16FromString(dllPath)
	if err != nil {
		return err
	}
	byteSize := uintptr(len(dllUTF16) * 2)
	remoteMemory, _, callErr := procVirtualAllocEx.Call(uintptr(process), 0, byteSize, memCommitReserve, pageReadWrite)
	if remoteMemory == 0 {
		return fmt.Errorf("VirtualAllocEx: %w", callErr)
	}
	defer procVirtualFreeEx.Call(uintptr(process), remoteMemory, 0, memRelease)
	bytes := unsafe.Slice((*byte)(unsafe.Pointer(&dllUTF16[0])), int(byteSize))
	var written uintptr
	if err := windows.WriteProcessMemory(process, remoteMemory, &bytes[0], byteSize, &written); err != nil || written != byteSize {
		return fmt.Errorf("WriteProcessMemory wrote %d/%d: %w", written, byteSize, err)
	}
	remoteThread, _, callErr := procCreateRemoteThread.Call(uintptr(process), 0, 0, remoteLoadLibrary, remoteMemory, 0, 0)
	if remoteThread == 0 {
		return fmt.Errorf("CreateRemoteThread: %w", callErr)
	}
	thread := windows.Handle(remoteThread)
	defer windows.CloseHandle(thread)
	for {
		wait := uint32(100)
		if remaining := time.Until(deadline); remaining <= 0 {
			return errors.New("remote LoadLibraryW timed out")
		} else if remaining < 100*time.Millisecond {
			wait = uint32(max(1, remaining.Milliseconds()))
		}
		status, err := windows.WaitForSingleObject(thread, wait)
		if err != nil {
			return err
		}
		if status == waitObject0 {
			break
		}
		if status != waitTimeout {
			return fmt.Errorf("unexpected remote thread wait status 0x%08X", status)
		}
	}
	loaded, err := remoteModuleLoaded(processID, dllPath)
	if err != nil {
		return err
	}
	if !loaded {
		return errors.New("remote LoadLibraryW completed but the module is absent")
	}
	return nil
}

func remoteModuleLoaded(pid uint32, dllPath string) (bool, error) {
	modules, err := loadedModules(pid)
	if err != nil {
		return false, err
	}
	want := filepath.Clean(dllPath)
	for _, module := range modules {
		if strings.EqualFold(module.path, want) {
			return true, nil
		}
	}
	return false, nil
}

type loadedModule struct {
	path string
	base uintptr
	size uint32
}

func loadedModules(pid uint32) ([]loadedModule, error) {
	if pid == 0 {
		return nil, errors.New("game PID is required")
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(th32csSnapModule|th32csSnapModule32, pid)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	modules := make([]loadedModule, 0, 64)
	entry := windows.ModuleEntry32{Size: uint32(unsafe.Sizeof(windows.ModuleEntry32{}))}
	for err = windows.Module32First(snapshot, &entry); err == nil; err = windows.Module32Next(snapshot, &entry) {
		path := filepath.Clean(windows.UTF16ToString(entry.ExePath[:]))
		if path != "." && path != "" {
			modules = append(modules, loadedModule{path: path, base: entry.ModBaseAddr, size: entry.ModBaseSize})
		}
	}
	if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return modules, nil
	}
	return nil, err
}

// ModulesLoaded verifies that every audited injection target is still present
// in the exact game process. The launcher uses this after helper completion so
// input hooks cannot be finalized from a stale success result after a module
// immediately unloaded or the process lifetime changed.
func ModulesLoaded(pid uint32, dllPaths []string) (bool, error) {
	loaded, _, err := ModuleReadiness(pid, dllPaths)
	return loaded, err
}

// ModuleReadiness verifies the audited injection targets and fingerprints the
// complete module set from one Toolhelp snapshot. A changing fingerprint means
// plugin initialization is still loading native dependencies, so the launcher
// restarts its continuous stabilization window before installing input hooks.
func ModuleReadiness(pid uint32, dllPaths []string) (loaded bool, fingerprint string, err error) {
	if pid == 0 || len(dllPaths) == 0 {
		return false, "", errors.New("game PID and injected module paths are required")
	}
	modules, err := loadedModules(pid)
	if err != nil {
		return false, "", err
	}
	normalized := make(map[string]struct{}, len(modules))
	for _, module := range modules {
		lower := strings.ToLower(filepath.Clean(module.path))
		normalized[lower] = struct{}{}
	}
	fingerprint = moduleSetFingerprint(modules)
	for _, dllPath := range dllPaths {
		if !filepath.IsAbs(dllPath) {
			return false, "", fmt.Errorf("injected module path is not absolute: %q", dllPath)
		}
		if _, exists := normalized[strings.ToLower(filepath.Clean(dllPath))]; !exists {
			return false, fingerprint, nil
		}
	}
	return true, fingerprint, nil
}

func moduleSetFingerprint(modules []loadedModule) string {
	fingerprintModules := make([]string, 0, len(modules))
	for _, module := range modules {
		lower := strings.ToLower(filepath.Clean(module.path))
		// Include the mapped address and image size as well as the path. A DLL
		// unloaded and reloaded from the same file is a new module lifetime and
		// must restart the input-hook stabilization interval.
		fingerprintModules = append(fingerprintModules, fmt.Sprintf("%s\x00%x:%d", lower, module.base, module.size))
	}
	sort.Strings(fingerprintModules)
	hash := sha256.New()
	for _, module := range fingerprintModules {
		_, _ = hash.Write([]byte(module))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// ReadyEventSignaled checks an opt-in module readiness handshake without
// creating or mutating the event. Modules should create a manual-reset event
// and signal it only after all asynchronous initialization and hook setup has
// completed.
func ReadyEventSignaled(name string) (bool, error) {
	if !strings.HasPrefix(name, `Local\GenshinTools.PluginReady.`) ||
		strings.ContainsAny(name, "\x00/{}") {
		return false, errors.New("module readiness event name is invalid")
	}
	nameUTF16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false, err
	}
	event, err := windows.OpenEvent(windows.SYNCHRONIZE, false, nameUTF16)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return false, nil
		}
		return false, err
	}
	defer windows.CloseHandle(event)
	status, err := windows.WaitForSingleObject(event, 0)
	if err != nil {
		return false, err
	}
	switch status {
	case waitObject0:
		return true, nil
	case waitTimeout:
		return false, nil
	default:
		return false, fmt.Errorf("unexpected readiness event wait status 0x%08X", status)
	}
}
