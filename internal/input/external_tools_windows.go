package input

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	bundledAHKRuntimeSHA256 = "09ae8c2a0eb2a5636231a4a228f89502bcce5c682d52b10ca803b8fef9cad2f5"
)

// ExternalCompatibilityTool is an exact, pre-injection input utility instance
// which must reinstall its global hooks after the injected module has started.
// Only the two user-confirmed tools are eligible.
type ExternalCompatibilityTool struct {
	PID          uint32
	Path         string
	CreationTime int64
}

type ExternalToolRestartResult struct {
	Path            string
	OldPIDs         []uint32
	NewPID          uint32
	NewCreationTime int64
	Job             windows.Handle
	Error           error
}

func CaptureExternalCompatibilityTools() []ExternalCompatibilityTool {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafeSizeofProcessEntry32())}
	var tools []ExternalCompatibilityTool
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if !supportedCompatibilityTool(name) || entry.ProcessID == 0 {
			continue
		}
		path := queryProcessPath(entry.ProcessID)
		if path == "" || !supportedCompatibilityTool(filepath.Base(path)) {
			continue
		}
		creationTime := processCreationTime(entry.ProcessID)
		if creationTime == 0 {
			continue
		}
		tools = append(tools, ExternalCompatibilityTool{
			PID:          entry.ProcessID,
			Path:         path,
			CreationTime: creationTime,
		})
	}
	return tools
}

// StopExternalCompatibilityTools removes captured user input hooks before the
// suspended game is created and plugins are injected. The exact captured
// identities are retained so the caller can restart them only after every
// injected module has finished loading.
func StopExternalCompatibilityTools(tools []ExternalCompatibilityTool) error {
	var result error
	for _, tool := range tools {
		if !supportedCompatibilityTool(filepath.Base(tool.Path)) || !filepath.IsAbs(tool.Path) ||
			tool.PID == 0 || tool.CreationTime == 0 {
			result = errors.Join(result, fmt.Errorf("reject invalid captured compatibility tool %q PID %d", tool.Path, tool.PID))
			continue
		}
		if err := stopCapturedCompatibilityTool(tool); err != nil {
			result = errors.Join(result, fmt.Errorf("stop pre-injection %s PID %d: %w", filepath.Base(tool.Path), tool.PID, err))
		}
	}
	return result
}

// StopRestartedCompatibilityTools rolls back replacements created by
// RestartExternalCompatibilityTools when the launch epoch becomes stale before
// the caller can commit them. A replacement is stopped only by its exact
// path/PID/creation-time identity.
func StopRestartedCompatibilityTools(results []ExternalToolRestartResult) error {
	var result error
	for _, restarted := range results {
		if restarted.NewPID == 0 {
			CloseExternalCompatibilityJob(restarted.Job)
			continue
		}
		identity := ExternalCompatibilityTool{
			PID:          restarted.NewPID,
			Path:         restarted.Path,
			CreationTime: restarted.NewCreationTime,
		}
		if identity.CreationTime == 0 {
			identity.CreationTime = processCreationTime(identity.PID)
		}
		err := stopCapturedCompatibilityTool(identity)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("stop stale replacement %s PID %d: %w", filepath.Base(restarted.Path), restarted.NewPID, err))
		}
		CloseExternalCompatibilityJob(restarted.Job)
	}
	return result
}

// unsafeSizeofProcessEntry32 is isolated so the selection logic remains easy
// to exercise without exposing unsafe outside this Win32 boundary.
func unsafeSizeofProcessEntry32() uintptr {
	var entry windows.ProcessEntry32
	return unsafe.Sizeof(entry)
}

func supportedCompatibilityTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "ahk_f.exe", "quickinput.exe":
		return true
	default:
		return false
	}
}

func queryProcessPath(processID uint32) string {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(process)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil || size == 0 {
		return ""
	}
	return windows.UTF16ToString(buffer[:size])
}

// RestartExternalCompatibilityTools replaces every captured lifetime for an
// exact executable path. It never matches by a broad process-name kill.
func RestartExternalCompatibilityTools(tools []ExternalCompatibilityTool) []ExternalToolRestartResult {
	groups := make(map[string][]ExternalCompatibilityTool, len(tools))
	order := make([]string, 0, len(tools))
	for _, tool := range tools {
		if !supportedCompatibilityTool(filepath.Base(tool.Path)) || !filepath.IsAbs(tool.Path) {
			order = append(order, tool.Path)
			groups[tool.Path] = append(groups[tool.Path], tool)
			continue
		}
		canonical := strings.ToLower(filepath.Clean(tool.Path))
		if _, exists := groups[canonical]; !exists {
			order = append(order, canonical)
		}
		groups[canonical] = append(groups[canonical], tool)
	}
	results := make([]ExternalToolRestartResult, 0, len(order))
	for _, canonical := range order {
		captured := groups[canonical]
		result := ExternalToolRestartResult{Path: captured[0].Path}
		if !supportedCompatibilityTool(filepath.Base(result.Path)) || !filepath.IsAbs(result.Path) {
			result.Error = fmt.Errorf("reject unsupported compatibility tool %q", result.Path)
			results = append(results, result)
			continue
		}
		for _, tool := range captured {
			result.OldPIDs = append(result.OldPIDs, tool.PID)
		}
		if strings.EqualFold(filepath.Base(result.Path), "AHK_F.exe") {
			restartAHKCompatibilityTool(captured, &result)
			results = append(results, result)
			continue
		}
		for _, tool := range captured {
			if err := stopCapturedCompatibilityTool(tool); err != nil {
				result.Error = errors.Join(result.Error, fmt.Errorf("stop PID %d: %w", tool.PID, err))
			}
		}
		if result.Error != nil {
			results = append(results, result)
			continue
		}
		time.Sleep(300 * time.Millisecond)
		result.NewPID, result.Error = startReplacementCompatibilityTool(result.Path)
		if result.Error == nil {
			result.NewCreationTime = processCreationTime(result.NewPID)
			result.Error = verifyReplacementCompatibilityTool(result.Path, result.NewPID, time.Second)
			if result.Error == nil && result.NewCreationTime == 0 {
				result.Error = fmt.Errorf("replacement PID %d has no verifiable creation time", result.NewPID)
			}
		}
		if result.Error != nil {
			cleanupFailedReplacement(&result)
		}
		results = append(results, result)
	}
	return results
}

// restartAHKCompatibilityTool starts the /restart replacement before forcibly
// stopping the captured instance. If elevation is required and UAC is
// cancelled, the user's original working AHK therefore remains alive.
func restartAHKCompatibilityTool(captured []ExternalCompatibilityTool, result *ExternalToolRestartResult) {
	restartAHKCompatibilityToolWith(
		captured,
		result,
		startReplacementCompatibilityTool,
		verifyReplacementCompatibilityTool,
		stopCapturedCompatibilityTool,
	)
	if result.NewPID != 0 {
		result.NewCreationTime = processCreationTime(result.NewPID)
	}
	if result.Error == nil {
		if result.NewCreationTime == 0 {
			result.Error = fmt.Errorf("replacement PID %d has no verifiable creation time", result.NewPID)
		}
	}
	if result.Error != nil {
		cleanupFailedReplacement(result)
	}
}

func cleanupFailedReplacement(result *ExternalToolRestartResult) {
	cleanupFailedReplacementWith(result, processCreationTime, stopCapturedCompatibilityTool)
}

func cleanupFailedReplacementWith(
	result *ExternalToolRestartResult,
	creationTime func(uint32) int64,
	stop func(ExternalCompatibilityTool) error,
) {
	if result == nil || result.NewPID == 0 || !filepath.IsAbs(result.Path) {
		return
	}
	identity := ExternalCompatibilityTool{
		PID:          result.NewPID,
		Path:         result.Path,
		CreationTime: result.NewCreationTime,
	}
	if identity.CreationTime == 0 {
		identity.CreationTime = creationTime(identity.PID)
	}
	if err := stop(identity); err != nil {
		result.Error = errors.Join(result.Error, fmt.Errorf("clean up failed replacement PID %d: %w", result.NewPID, err))
		return
	}
	result.NewPID = 0
	result.NewCreationTime = 0
}

func restartAHKCompatibilityToolWith(
	captured []ExternalCompatibilityTool,
	result *ExternalToolRestartResult,
	start func(string) (uint32, error),
	verify func(string, uint32, time.Duration) error,
	stop func(ExternalCompatibilityTool) error,
) {
	result.NewPID, result.Error = start(result.Path)
	if result.Error != nil {
		return
	}
	result.Error = verify(result.Path, result.NewPID, time.Second)
	if result.Error != nil {
		return
	}
	for _, tool := range captured {
		if tool.PID == result.NewPID {
			continue
		}
		if err := stop(tool); err != nil {
			result.Error = errors.Join(result.Error, fmt.Errorf("stop replaced PID %d: %w", tool.PID, err))
		}
	}
}

func startReplacementCompatibilityTool(path string) (uint32, error) {
	return startCompatibilityToolWithArguments(path, compatibilityToolArguments(path))
}

func startCompatibilityToolWithArguments(path string, arguments []string) (uint32, error) {
	command := exec.Command(path, arguments...)
	command.Dir = filepath.Dir(path)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if err := command.Start(); err == nil {
		processID := uint32(command.Process.Pid)
		_ = command.Process.Release()
		return processID, nil
	} else if !requiresElevatedCompatibilityStart(err) {
		return 0, fmt.Errorf("start replacement: %w", err)
	}
	processID, err := startElevatedCompatibilityTool(path, arguments)
	if err != nil {
		return 0, fmt.Errorf("start elevated replacement: %w", err)
	}
	return processID, nil
}

// StartBundledAHK starts the exact project-owner supplied compiled utility.
// It is a complete legacy program, not an AutoHotkey interpreter plus script.
func StartBundledAHK(runtimePath string, processIDs []uint32) ExternalToolRestartResult {
	result := ExternalToolRestartResult{Path: runtimePath}
	if !filepath.IsAbs(runtimePath) || !strings.EqualFold(filepath.Base(runtimePath), "AHK_F.exe") {
		result.Error = errors.New("bundled AHK must be an absolute audited AHK_F.exe path")
		return result
	}
	if err := verifyBundledAHKFile(runtimePath, 16<<20, bundledAHKRuntimeSHA256); err != nil {
		result.Error = fmt.Errorf("bundled AHK binary audit failed: %w", err)
		return result
	}
	hasGame := false
	for _, processID := range processIDs {
		if processID != 0 {
			hasGame = true
			break
		}
	}
	if !hasGame {
		result.Error = errors.New("bundled AHK requires at least one verified game PID")
		return result
	}
	result.NewPID, result.Error = startCompatibilityToolWithArguments(runtimePath, compatibilityToolArguments(runtimePath))
	if result.Error == nil {
		result.Error = verifyReplacementCompatibilityTool(runtimePath, result.NewPID, time.Millisecond)
	}
	if result.Error == nil {
		result.NewCreationTime = processCreationTime(result.NewPID)
		if result.NewCreationTime == 0 {
			result.Error = fmt.Errorf("bundled AHK PID %d has no verifiable creation time", result.NewPID)
		}
	}
	if result.Error == nil {
		result.Job, result.Error = attachKillOnCloseJob(result.NewPID)
	}
	if result.Error != nil {
		// Preserve the exact identity when cleanup cannot be proven, and join
		// that failure into Error. Callers can then retry rollback and must not
		// blindly start another instance.
		cleanupFailedReplacement(&result)
	}
	return result
}

func verifyBundledAHKFile(path string, maximumSize int64, expectedSHA256 string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumSize {
		return errors.New("file size or type is invalid")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), expectedSHA256) {
		return errors.New("SHA-256 does not match the audited release file")
	}
	return nil
}

func StopExternalCompatibilityTool(processID uint32, path string, creationTime int64) error {
	if processID == 0 || !filepath.IsAbs(path) || creationTime == 0 {
		return errors.New("external compatibility tool identity is invalid")
	}
	return stopCapturedCompatibilityTool(ExternalCompatibilityTool{
		PID:          processID,
		Path:         path,
		CreationTime: creationTime,
	})
}

func attachKillOnCloseJob(processID uint32) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create bundled AHK job: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("set bundled AHK job limits: %w", err)
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		processID,
	)
	if err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("open bundled AHK for job assignment: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("assign bundled AHK to kill-on-close job: %w", err)
	}
	return job, nil
}

func CloseExternalCompatibilityJob(job windows.Handle) {
	if job != 0 && job != windows.InvalidHandle {
		_ = windows.CloseHandle(job)
	}
}

func requiresElevatedCompatibilityStart(err error) bool {
	return errors.Is(err, windows.ERROR_ELEVATION_REQUIRED)
}

func compatibilityToolArguments(path string) []string {
	if strings.EqualFold(filepath.Base(path), "AHK_F.exe") {
		return []string{"/restart"}
	}
	return nil
}

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

func startElevatedCompatibilityTool(path string, arguments []string) (uint32, error) {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var parameters *uint16
	if len(arguments) != 0 {
		parameters, err = windows.UTF16PtrFromString(windows.ComposeCommandLine(arguments))
		if err != nil {
			return 0, err
		}
	}
	directory, err := windows.UTF16PtrFromString(filepath.Dir(path))
	if err != nil {
		return 0, err
	}
	info := shellExecuteInfo{
		Size:       uint32(unsafe.Sizeof(shellExecuteInfo{})),
		Mask:       seeMaskNoCloseProcess | seeMaskNoAsync,
		Verb:       verb,
		File:       file,
		Parameters: parameters,
		Directory:  directory,
		Show:       1,
	}
	ok, _, callErr := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return 0, errors.New("administrator authorization was canceled; original AHK was left running")
		}
		return 0, fmt.Errorf("ShellExecuteExW runas: %w", normalizeCallError(callErr))
	}
	if info.Process == 0 {
		return 0, errors.New("ShellExecuteExW returned no replacement process handle")
	}
	defer windows.CloseHandle(info.Process)
	processID, err := windows.GetProcessId(info.Process)
	if err != nil || processID == 0 {
		return 0, fmt.Errorf("query elevated replacement PID: %w", err)
	}
	return processID, nil
}

func verifyReplacementCompatibilityTool(path string, processID uint32, stableFor time.Duration) error {
	if processID == 0 {
		return errors.New("replacement returned no PID")
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, processID)
	if err != nil {
		return fmt.Errorf("open replacement PID %d: %w", processID, err)
	}
	defer windows.CloseHandle(process)
	wait := uint32(max(stableFor.Milliseconds(), 1))
	status, err := windows.WaitForSingleObject(process, wait)
	if err != nil {
		return fmt.Errorf("wait replacement PID %d: %w", processID, err)
	}
	if status == windows.WAIT_OBJECT_0 {
		var exitCode uint32
		if err := windows.GetExitCodeProcess(process, &exitCode); err != nil {
			return fmt.Errorf("replacement PID %d exited and its code is unavailable: %w", processID, err)
		}
		return fmt.Errorf("replacement PID %d exited during health check with code %d", processID, exitCode)
	}
	if status != uint32(windows.WAIT_TIMEOUT) {
		return fmt.Errorf("replacement PID %d returned wait status 0x%08X", processID, status)
	}
	if currentPath := queryProcessPath(processID); currentPath == "" || !strings.EqualFold(filepath.Clean(currentPath), filepath.Clean(path)) {
		return fmt.Errorf("replacement PID %d image does not match %q", processID, path)
	}
	if processCreationTime(processID) == 0 {
		return fmt.Errorf("replacement PID %d has no verifiable creation time", processID)
	}
	return nil
}

const (
	wMCommand        = 0x0111
	ahkTraySuspendID = 65305
)

var ahkWindowSearch struct {
	sync.Mutex
	processID uint32
	window    uintptr
}

var ahkWindowEnumCallback = syscall.NewCallback(func(window, _ uintptr) uintptr {
	if windowProcessID(windows.HWND(window)) != ahkWindowSearch.processID {
		return 1
	}
	var class [64]uint16
	length, _, _ := procGetClassNameW.Call(window, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
	if length != 0 && strings.EqualFold(windows.UTF16ToString(class[:length]), "AutoHotkey") {
		ahkWindowSearch.window = window
		return 0
	}
	return 1
})

func findExternalAHKWindow(processID uint32) uintptr {
	if processID == 0 {
		return 0
	}
	ahkWindowSearch.Lock()
	defer ahkWindowSearch.Unlock()
	ahkWindowSearch.processID = processID
	ahkWindowSearch.window = 0
	procEnumWindows.Call(ahkWindowEnumCallback, 0)
	return ahkWindowSearch.window
}

// ToggleExternalAHKSuspend invokes AutoHotkey's stable standard tray command.
// The caller owns desired-state tracking because this old runtime exposes a
// toggle command rather than an idempotent set-state command.
func ToggleExternalAHKSuspend(processID uint32) error {
	window := findExternalAHKWindow(processID)
	if window == 0 {
		return fmt.Errorf("AutoHotkey main window was not found for PID %d", processID)
	}
	result, _, callErr := procPostMessageW.Call(window, wMCommand, ahkTraySuspendID, 0)
	if result == 0 {
		return fmt.Errorf("post AutoHotkey Suspend command to PID %d: %w", processID, normalizeCallError(callErr))
	}
	return nil
}

func ExternalCompatibilityToolRunning(processID uint32, path string, creationTime int64) bool {
	if processID == 0 || !filepath.IsAbs(path) || creationTime == 0 {
		return false
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, processID)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(process)
	status, err := windows.WaitForSingleObject(process, 0)
	return err == nil && status == uint32(windows.WAIT_TIMEOUT) &&
		strings.EqualFold(filepath.Clean(queryProcessPath(processID)), filepath.Clean(path)) &&
		processCreationTime(processID) == creationTime
}

func stopCapturedCompatibilityTool(tool ExternalCompatibilityTool) error {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, tool.PID)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if tool.CreationTime != 0 && processCreationTime(tool.PID) != tool.CreationTime {
		return errors.New("captured PID now belongs to another process")
	}
	currentPath := queryProcessPath(tool.PID)
	if currentPath == "" || !strings.EqualFold(filepath.Clean(currentPath), filepath.Clean(tool.Path)) {
		return errors.New("captured process image changed")
	}
	if err := windows.TerminateProcess(process, 0); err != nil {
		return err
	}
	status, err := windows.WaitForSingleObject(process, uint32((2 * time.Second).Milliseconds()))
	if err != nil {
		return err
	}
	if status != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("process did not exit (wait status 0x%08X)", status)
	}
	return nil
}
