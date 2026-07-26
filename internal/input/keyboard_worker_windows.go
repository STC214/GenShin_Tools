package input

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const autoHotkeyKeyIgnoreLevel0 uintptr = 0xFFC3D44D

type keyboardWorkerRequest struct {
	Enabled       bool     `json:"enabled"`
	RepeatKeys    []uint32 `json:"repeatKeys"`
	IntervalMS    int      `json:"intervalMs"`
	GameProcesses []uint32 `json:"gameProcesses"`
}

type keyboardWorkerResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type keyboardWorkerController struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	output *bufio.Reader
	active atomic.Bool
	last   *keyboardWorkerRequest
	closed bool
}

func newKeyboardWorkerController() *keyboardWorkerController {
	return &keyboardWorkerController{}
}

func (worker *keyboardWorkerController) Active() bool {
	return worker != nil && worker.active.Load()
}

func (worker *keyboardWorkerController) PID() int {
	if worker == nil {
		return 0
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.cmd == nil || worker.cmd.Process == nil {
		return 0
	}
	return worker.cmd.Process.Pid
}

func (worker *keyboardWorkerController) Configure(request keyboardWorkerRequest) error {
	if worker == nil {
		return errors.New("keyboard worker is unavailable")
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.closed {
		return errors.New("keyboard worker is closed")
	}
	if worker.cmd != nil && sameKeyboardWorkerRequest(worker.last, &request) {
		worker.active.Store(request.Enabled && len(request.RepeatKeys) != 0 && len(request.GameProcesses) != 0)
		return nil
	}
	if len(request.GameProcesses) == 0 {
		worker.active.Store(false)
		if worker.cmd == nil {
			return nil
		}
	}
	if worker.cmd == nil {
		if err := worker.startLocked(); err != nil {
			worker.active.Store(false)
			return err
		}
	}
	type exchangeResult struct {
		response keyboardWorkerResponse
		err      error
	}
	stdin, output := worker.stdin, worker.output
	result := make(chan exchangeResult, 1)
	go func() {
		if err := json.NewEncoder(stdin).Encode(request); err != nil {
			result <- exchangeResult{err: fmt.Errorf("write keyboard worker configuration: %w", err)}
			return
		}
		var response keyboardWorkerResponse
		err := json.NewDecoder(output).Decode(&response)
		result <- exchangeResult{response: response, err: err}
	}()
	var exchange exchangeResult
	select {
	case exchange = <-result:
	case <-time.After(2 * time.Second):
		worker.stopLocked()
		return errors.New("keyboard worker configuration timed out")
	}
	if exchange.err != nil {
		worker.stopLocked()
		return fmt.Errorf("read keyboard worker confirmation: %w", exchange.err)
	}
	response := exchange.response
	if !response.OK {
		worker.active.Store(false)
		return errors.New(response.Error)
	}
	saved := request
	saved.RepeatKeys = append([]uint32(nil), request.RepeatKeys...)
	saved.GameProcesses = append([]uint32(nil), request.GameProcesses...)
	worker.last = &saved
	worker.active.Store(request.Enabled && len(request.RepeatKeys) != 0 && len(request.GameProcesses) != 0)
	return nil
}

func (worker *keyboardWorkerController) Restart() error {
	if worker == nil {
		return errors.New("keyboard worker is unavailable")
	}
	worker.mu.Lock()
	if worker.closed {
		worker.mu.Unlock()
		return errors.New("keyboard worker is closed")
	}
	if worker.last == nil {
		worker.mu.Unlock()
		return nil
	}
	request := *worker.last
	request.RepeatKeys = append([]uint32(nil), worker.last.RepeatKeys...)
	request.GameProcesses = append([]uint32(nil), worker.last.GameProcesses...)
	worker.stopLocked()
	worker.mu.Unlock()
	return worker.Configure(request)
}

func sameKeyboardWorkerRequest(left, right *keyboardWorkerRequest) bool {
	if left == nil || right == nil || left.Enabled != right.Enabled || left.IntervalMS != right.IntervalMS ||
		len(left.RepeatKeys) != len(right.RepeatKeys) || len(left.GameProcesses) != len(right.GameProcesses) {
		return false
	}
	for index := range left.RepeatKeys {
		if left.RepeatKeys[index] != right.RepeatKeys[index] {
			return false
		}
	}
	remaining := make(map[uint32]int, len(left.GameProcesses))
	for _, processID := range left.GameProcesses {
		remaining[processID]++
	}
	for _, processID := range right.GameProcesses {
		if remaining[processID] == 0 {
			return false
		}
		remaining[processID]--
	}
	return true
}

func (worker *keyboardWorkerController) startLocked() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	helper := filepath.Join(filepath.Dir(executable), "GenshinTools-input.exe")
	if _, err := os.Stat(helper); err != nil {
		return fmt.Errorf("keyboard worker not found: %w", err)
	}
	command := exec.Command(helper)
	command.Dir = filepath.Dir(executable)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		stdin.Close()
		return err
	}
	if err := command.Start(); err != nil {
		stdin.Close()
		return err
	}
	worker.cmd = command
	worker.stdin = stdin
	worker.output = bufio.NewReader(stdout)
	go func(command *exec.Cmd) {
		_ = command.Wait()
		worker.mu.Lock()
		defer worker.mu.Unlock()
		if worker.cmd == command {
			worker.active.Store(false)
			worker.cmd = nil
			worker.stdin = nil
			worker.output = nil
			worker.last = nil
		}
	}(command)
	return nil
}

func (worker *keyboardWorkerController) Close() {
	if worker == nil {
		return
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	worker.closed = true
	worker.stopLocked()
}

func (worker *keyboardWorkerController) stopLocked() {
	worker.active.Store(false)
	if worker.stdin != nil {
		_ = worker.stdin.Close()
	}
	if worker.cmd != nil && worker.cmd.Process != nil {
		_ = worker.cmd.Process.Kill()
	}
	worker.cmd = nil
	worker.stdin = nil
	worker.output = nil
	worker.last = nil
}

type keyboardWorkerConfig struct {
	enabled   bool
	keys      map[uint32]struct{}
	interval  time.Duration
	processes map[uint32]struct{}
}

type keyboardWorkerRuntime struct {
	config             atomic.Pointer[keyboardWorkerConfig]
	held               [256]atomic.Bool
	done               chan struct{}
	wg                 sync.WaitGroup
	gameForegroundTest func(*keyboardWorkerConfig) bool
}

func RunKeyboardWorker(input io.Reader, output io.Writer) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	worker := &keyboardWorkerRuntime{done: make(chan struct{})}
	worker.config.Store(&keyboardWorkerConfig{
		keys:      map[uint32]struct{}{},
		processes: map[uint32]struct{}{},
		interval:  5 * time.Millisecond,
	})
	activeKeyboardWorker.Store(worker)
	defer activeKeyboardWorker.Store(nil)

	module, _, callErr := procGetModuleHandleW.Call(0)
	if module == 0 {
		return fmt.Errorf("GetModuleHandleW: %w", normalizeCallError(callErr))
	}
	callback := syscall.NewCallback(keyboardWorkerHookCallback)
	hook, _, callErr := procSetWindowsHookExW.Call(whKeyboardLL, callback, module, 0)
	if hook == 0 {
		return fmt.Errorf("SetWindowsHookExW keyboard worker: %w", normalizeCallError(callErr))
	}
	defer procUnhookWindowsHookEx.Call(hook)
	threadID := windows.GetCurrentThreadId()
	go worker.readConfigurations(input, output, threadID)
	var msg message
	for {
		value, _, callErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(value) == 0 {
			break
		}
		if int32(value) == -1 {
			return fmt.Errorf("GetMessageW keyboard worker: %w", normalizeCallError(callErr))
		}
	}
	close(worker.done)
	worker.wg.Wait()
	runtime.KeepAlive(callback)
	return nil
}

func (worker *keyboardWorkerRuntime) readConfigurations(input io.Reader, output io.Writer, threadID uint32) {
	decoder := json.NewDecoder(input)
	encoder := json.NewEncoder(output)
	for {
		var request keyboardWorkerRequest
		if err := decoder.Decode(&request); err != nil {
			procPostThreadMessageW.Call(uintptr(threadID), wMQuit, 0, 0)
			return
		}
		response := keyboardWorkerResponse{OK: true}
		if request.IntervalMS < 1 || request.IntervalMS > 5000 {
			response.OK = false
			response.Error = "keyboard worker interval must be 1..5000 ms"
		} else {
			keys := make(map[uint32]struct{}, len(request.RepeatKeys))
			for _, key := range request.RepeatKeys {
				if !ValidKeyCode(key) {
					response.OK = false
					response.Error = "keyboard worker received an invalid repeat key"
					break
				}
				keys[NormalizeKeyCode(key)] = struct{}{}
			}
			processes := make(map[uint32]struct{}, len(request.GameProcesses))
			for _, processID := range request.GameProcesses {
				if processID != 0 {
					processes[processID] = struct{}{}
				}
			}
			if response.OK {
				worker.config.Store(&keyboardWorkerConfig{
					enabled:   request.Enabled,
					keys:      keys,
					interval:  time.Duration(request.IntervalMS) * time.Millisecond,
					processes: processes,
				})
				for index := range worker.held {
					worker.held[index].Store(false)
				}
			}
		}
		if err := encoder.Encode(response); err != nil {
			return
		}
	}
}

var activeKeyboardWorker atomic.Pointer[keyboardWorkerRuntime]

func keyboardWorkerHookCallback(code int, message, dataPointer uintptr) uintptr {
	if code >= 0 && dataPointer != 0 {
		data := (*keyboardHook)(unsafe.Pointer(dataPointer))
		if data.Flags&llkhfInjected == 0 {
			down := message == wMKeyDown || message == wMSysKeyDown
			up := message == wMKeyUp || message == wMSysKeyUp
			if down || up {
				if worker := activeKeyboardWorker.Load(); worker != nil {
					if worker.handlePhysical(EncodeKeyCode(data.VirtualKey, data.Flags&llkhfExtended != 0), down) {
						// Match the observed AHK_F hotkey path: while the
						// verified game is foreground, the physical trigger
						// does not reach the game. Only the balanced synthetic
						// replacement pairs do. Unrelated keys always continue.
						return 1
					}
				}
			}
		}
	}
	result, _, _ := procCallNextHookEx.Call(0, uintptr(code), message, dataPointer)
	return result
}

func (worker *keyboardWorkerRuntime) handlePhysical(key uint32, down bool) bool {
	key = NormalizeKeyCode(key)
	virtualKey := VirtualKey(key)
	if !down {
		worker.held[virtualKey].Store(false)
	}
	config := worker.config.Load()
	if config == nil || !config.enabled {
		return false
	}
	if _, ok := config.keys[key]; !ok || !worker.gameForeground(config) {
		return false
	}
	if !down {
		return true
	}
	if !worker.held[virtualKey].CompareAndSwap(false, true) {
		return true
	}
	worker.wg.Add(1)
	go worker.repeatKey(key)
	return true
}

func (worker *keyboardWorkerRuntime) repeatKey(key uint32) {
	defer worker.wg.Done()
	virtualKey := VirtualKey(key)
	// AutoHotkey dispatches a hotkey after its low-level hook callback has
	// returned. Do the same so the physical event is fully suppressed before
	// the replacement down/up pair enters the system input stream.
	startDelay := time.NewTimer(time.Millisecond)
	select {
	case <-worker.done:
		if !startDelay.Stop() {
			<-startDelay.C
		}
		return
	case <-startDelay.C:
	}
	for worker.held[virtualKey].Load() {
		config := worker.config.Load()
		if config == nil || !config.enabled {
			return
		}
		if _, ok := config.keys[key]; !ok {
			return
		}
		next := time.Now().Add(config.interval)
		if worker.gameForeground(config) {
			worker.emitKey(key)
		}
		delay := time.Until(next)
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-worker.done:
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (worker *keyboardWorkerRuntime) gameForeground(config *keyboardWorkerConfig) bool {
	if worker.gameForegroundTest != nil {
		return worker.gameForegroundTest(config)
	}
	foreground := windows.GetForegroundWindow()
	if foreground == 0 {
		return false
	}
	processID := windowProcessID(foreground)
	_, ok := config.processes[processID]
	return ok
}

func (worker *keyboardWorkerRuntime) emitKey(key uint32) {
	virtualKey := VirtualKey(key)
	foreground := windows.GetForegroundWindow()
	threadID, _, _ := procGetWindowThreadProcessID.Call(uintptr(foreground), 0)
	layout, _, _ := procGetKeyboardLayout.Call(threadID)
	scan, _, _ := procMapVirtualKeyExW.Call(uintptr(virtualKey), mapvkVKToVSCEx, layout)
	flags := uintptr(0)
	if KeyIsExtended(key) || scan&0xff00 == 0xe000 || scan&0xff00 == 0xe100 {
		flags |= keyeventfExtendedKey
	}
	procKeybdEvent.Call(uintptr(virtualKey), scan&0xff, flags, autoHotkeyKeyIgnoreLevel0)
	time.Sleep(time.Millisecond)
	procKeybdEvent.Call(uintptr(virtualKey), scan&0xff, flags|keyeventfKeyUp, autoHotkeyKeyIgnoreLevel0)
}
