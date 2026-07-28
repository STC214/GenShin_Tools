package input

import (
	"bufio"
	"bytes"
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

type keyboardWorkerRequest struct {
	Enabled       bool     `json:"enabled"`
	RepeatKeys    []uint32 `json:"repeatKeys"`
	IntervalMS    int      `json:"intervalMs"`
	GameProcesses []uint32 `json:"gameProcesses"`
	StatusOnly    bool     `json:"statusOnly,omitempty"`
}

type keyboardWorkerResponse struct {
	OK          bool                      `json:"ok"`
	Error       string                    `json:"error,omitempty"`
	Diagnostics KeyboardWorkerDiagnostics `json:"diagnostics"`
}

// KeyboardWorkerDiagnostics is a bounded snapshot returned by the x86 worker.
// Counters are monotonic for one worker lifetime, allowing the parent log to
// distinguish hook capture, foreground gating and driver delivery failures.
type KeyboardWorkerDiagnostics struct {
	ConfiguredKeyEvents uint64   `json:"configuredKeyEvents"`
	ForegroundMisses    uint64   `json:"foregroundMisses"`
	OutputGateMisses    uint64   `json:"outputGateMisses"`
	TriggerDowns        uint64   `json:"triggerDowns"`
	TriggerUps          uint64   `json:"triggerUps"`
	RepeatStarts        uint64   `json:"repeatStarts"`
	RepeatStops         uint64   `json:"repeatStops"`
	OutputPairs         uint64   `json:"outputPairs"`
	OutputFailures      uint64   `json:"outputFailures"`
	SyntheticHookEvents uint64   `json:"syntheticHookEvents"`
	ReleaseChecks       uint64   `json:"releaseChecks"`
	LastKey             uint32   `json:"lastKey"`
	LastScanCode        uint32   `json:"lastScanCode"`
	LastDevice          uint32   `json:"lastDevice"`
	LastOutputDevice    uint32   `json:"lastOutputDevice"`
	ForegroundPID       uint32   `json:"foregroundPid"`
	HeldKeys            []uint32 `json:"heldKeys,omitempty"`
	HeldDevices         []uint32 `json:"heldDevices,omitempty"`
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *synchronizedBuffer) Reset() {
	buffer.mu.Lock()
	buffer.buffer.Reset()
	buffer.mu.Unlock()
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

type keyboardWorkerController struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	output      *bufio.Reader
	processDone chan struct{}
	stderr      synchronizedBuffer
	active      atomic.Bool
	last        *keyboardWorkerRequest
	lastError   string
	closed      bool
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

func (worker *keyboardWorkerController) LastError() string {
	if worker == nil {
		return "keyboard worker is unavailable"
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.lastError
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
	// The keyboard hook and Interception device handle must not exist before
	// a verified game process exists. Stopping the worker here also unloads
	// the complete repeat module when the last game process exits.
	if len(request.GameProcesses) == 0 || !request.Enabled || len(request.RepeatKeys) == 0 {
		worker.stopLocked()
		worker.lastError = ""
		return nil
	}
	if worker.cmd != nil && sameKeyboardWorkerRequest(worker.last, &request) {
		worker.active.Store(request.Enabled && len(request.RepeatKeys) != 0 && len(request.GameProcesses) != 0)
		return nil
	}
	if worker.cmd == nil {
		if err := worker.startLocked(); err != nil {
			worker.active.Store(false)
			worker.lastError = err.Error()
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
		message := "keyboard worker configuration timed out"
		worker.stopLocked()
		worker.lastError = message
		return errors.New(message)
	}
	if exchange.err != nil {
		message := fmt.Sprintf("read keyboard worker confirmation: %v", exchange.err)
		if detail := worker.stderr.String(); detail != "" {
			message += ": " + detail
		}
		worker.stopLocked()
		worker.lastError = message
		return errors.New(message)
	}
	response := exchange.response
	if !response.OK {
		message := response.Error
		// The child may already own Interception keyboard filters. Any rejected
		// configuration must terminate it immediately so closing the device
		// handles releases those filters.
		worker.stopLocked()
		worker.lastError = message
		return errors.New(message)
	}
	saved := request
	saved.RepeatKeys = append([]uint32(nil), request.RepeatKeys...)
	saved.GameProcesses = append([]uint32(nil), request.GameProcesses...)
	worker.last = &saved
	worker.lastError = ""
	worker.active.Store(request.Enabled && len(request.RepeatKeys) != 0 && len(request.GameProcesses) != 0)
	return nil
}

func (worker *keyboardWorkerController) Diagnostics() (KeyboardWorkerDiagnostics, error) {
	if worker == nil {
		return KeyboardWorkerDiagnostics{}, errors.New("keyboard worker is unavailable")
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.closed || worker.cmd == nil || worker.stdin == nil || worker.output == nil {
		return KeyboardWorkerDiagnostics{}, errors.New("keyboard worker is not running")
	}
	type exchangeResult struct {
		response keyboardWorkerResponse
		err      error
	}
	result := make(chan exchangeResult, 1)
	go func() {
		if err := json.NewEncoder(worker.stdin).Encode(keyboardWorkerRequest{StatusOnly: true}); err != nil {
			result <- exchangeResult{err: fmt.Errorf("write keyboard worker diagnostic request: %w", err)}
			return
		}
		var response keyboardWorkerResponse
		err := json.NewDecoder(worker.output).Decode(&response)
		result <- exchangeResult{response: response, err: err}
	}()
	select {
	case exchange := <-result:
		if exchange.err != nil {
			return KeyboardWorkerDiagnostics{}, fmt.Errorf("read keyboard worker diagnostics: %w", exchange.err)
		}
		if !exchange.response.OK {
			return KeyboardWorkerDiagnostics{}, errors.New(exchange.response.Error)
		}
		return exchange.response.Diagnostics, nil
	case <-time.After(750 * time.Millisecond):
		message := "keyboard worker diagnostic request timed out"
		// The abandoned decoder would otherwise race a later request for the
		// same stdout frame. Stop the worker and let normal lifecycle recovery
		// create a fresh protocol stream.
		var saved *keyboardWorkerRequest
		if worker.last != nil {
			request := *worker.last
			request.RepeatKeys = append([]uint32(nil), worker.last.RepeatKeys...)
			request.GameProcesses = append([]uint32(nil), worker.last.GameProcesses...)
			saved = &request
		}
		worker.stopLocked()
		worker.last = saved
		worker.lastError = message
		return KeyboardWorkerDiagnostics{}, errors.New(message)
	}
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
		lastError := worker.lastError
		worker.mu.Unlock()
		if lastError != "" {
			return errors.New(lastError)
		}
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
	worker.stderr.Reset()
	command.Stderr = &worker.stderr
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
	processDone := make(chan struct{})
	worker.processDone = processDone
	go func(command *exec.Cmd, done chan struct{}) {
		waitErr := command.Wait()
		close(done)
		worker.mu.Lock()
		defer worker.mu.Unlock()
		if worker.cmd == command {
			worker.active.Store(false)
			if waitErr != nil && worker.lastError == "" {
				worker.lastError = fmt.Sprintf("keyboard worker exited: %v", waitErr)
				if detail := worker.stderr.String(); detail != "" {
					worker.lastError += ": " + detail
				}
			}
			worker.cmd = nil
			worker.stdin = nil
			worker.output = nil
			worker.processDone = nil
			worker.last = nil
		}
	}(command, processDone)
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
	if worker.processDone != nil {
		select {
		case <-worker.processDone:
		case <-time.After(2 * time.Second):
			if worker.lastError == "" {
				worker.lastError = "keyboard worker did not exit within two seconds"
			}
		}
	}
	worker.cmd = nil
	worker.stdin = nil
	worker.output = nil
	worker.processDone = nil
	worker.last = nil
}

type keyboardWorkerConfig struct {
	enabled   bool
	keys      map[uint32]struct{}
	interval  time.Duration
	processes map[uint32]struct{}
}

type keyboardWorkerRuntime struct {
	config              atomic.Pointer[keyboardWorkerConfig]
	held                [1024]atomic.Bool
	heldDevice          [1024]atomic.Uint32
	generation          [1024]atomic.Uint64
	done                chan struct{}
	wg                  sync.WaitGroup
	faultMu             sync.Mutex
	fault               error
	outputMu            sync.Mutex
	backend             *interceptionKeyboardBackend
	threadID            uint32
	gameForegroundTest  func(*keyboardWorkerConfig) bool
	emitTest            func(uint32, time.Duration) error
	configuredKeyEvents atomic.Uint64
	foregroundMisses    atomic.Uint64
	outputGateMisses    atomic.Uint64
	triggerDowns        atomic.Uint64
	triggerUps          atomic.Uint64
	repeatStarts        atomic.Uint64
	repeatStops         atomic.Uint64
	outputPairs         atomic.Uint64
	outputFailures      atomic.Uint64
	syntheticHookEvents atomic.Uint64
	releaseChecks       atomic.Uint64
	lastKey             atomic.Uint32
	lastScanCode        atomic.Uint32
	lastDevice          atomic.Uint32
	lastOutputDevice    atomic.Uint32
	foregroundPID       atomic.Uint32
}

func RunKeyboardWorker(input io.Reader, output io.Writer) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	backend, err := newInterceptionKeyboardBackend()
	if err != nil {
		return err
	}
	defer backend.Close()
	worker := &keyboardWorkerRuntime{done: make(chan struct{}), backend: backend}
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
	worker.threadID = threadID
	go worker.readConfigurations(input, output, threadID)
	var msg message
	var loopError error
	for {
		value, _, callErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(value) == 0 {
			break
		}
		if int32(value) == -1 {
			loopError = fmt.Errorf("GetMessageW keyboard worker: %w", normalizeCallError(callErr))
			break
		}
	}
	close(worker.done)
	worker.wg.Wait()
	worker.releaseHeldKeys()
	runtime.KeepAlive(callback)
	return errors.Join(loopError, worker.runtimeFault())
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
		if request.StatusOnly {
			response.Diagnostics = worker.diagnostics()
		} else if request.IntervalMS < 1 || request.IntervalMS > 5000 {
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
				key = NormalizeKeyCode(key)
				_, err := interceptionKeyboardData(key, false)
				if err != nil {
					response.OK = false
					response.Error = fmt.Sprintf("map keyboard worker repeat key 0x%X: %v", key, err)
					break
				}
				keys[key] = struct{}{}
			}
			processes := make(map[uint32]struct{}, len(request.GameProcesses))
			for _, processID := range request.GameProcesses {
				if processID != 0 {
					processes[processID] = struct{}{}
				}
			}
			if response.OK {
				worker.releaseHeldKeys()
				worker.config.Store(&keyboardWorkerConfig{
					enabled:   request.Enabled,
					keys:      keys,
					interval:  time.Duration(request.IntervalMS) * time.Millisecond,
					processes: processes,
				})
				for index := range worker.held {
					worker.held[index].Store(false)
					worker.heldDevice[index].Store(0)
					worker.generation[index].Add(1)
				}
			}
			response.Diagnostics = worker.diagnostics()
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
		worker := activeKeyboardWorker.Load()
		if worker != nil {
			worker.handleKeyboardHookEvent(message, data)
		}
	}
	result, _, _ := procCallNextHookEx.Call(0, uintptr(code), message, dataPointer)
	return result
}

func (worker *keyboardWorkerRuntime) handleKeyboardHookEvent(message uintptr, data *keyboardHook) {
	if worker == nil || data == nil {
		return
	}
	// Interception inserts into the keyboard device stack and therefore is
	// not guaranteed to carry LLKHF_INJECTED. The information marker is the
	// same recursion boundary used by FlairBloom.
	if data.ExtraInfo == interceptionMarker {
		worker.syntheticHookEvents.Add(1)
		return
	}
	if data.Flags&llkhfInjected != 0 {
		return
	}
	down := message == wMKeyDown || message == wMSysKeyDown
	up := message == wMKeyUp || message == wMSysKeyUp
	if !down && !up {
		return
	}
	key := EncodeKeyCode(data.VirtualKey, data.Flags&llkhfExtended != 0)
	if up {
		config := worker.config.Load()
		if config != nil {
			if _, configured := config.keys[NormalizeKeyCode(key)]; configured {
				worker.releaseChecks.Add(1)
			}
		}
	}
	worker.handlePhysicalDevice(key, down, 0)
}

func (worker *keyboardWorkerRuntime) handlePhysical(key uint32, down bool) {
	worker.handlePhysicalDevice(key, down, 0)
}

func (worker *keyboardWorkerRuntime) handlePhysicalDevice(key uint32, down bool, device uint32) {
	key = NormalizeKeyCode(key)
	index := int(key & 0x3ff)
	if !down {
		if worker.held[index].Swap(false) {
			worker.generation[index].Add(1)
		}
		worker.heldDevice[index].Store(0)
	}
	config := worker.config.Load()
	if config == nil || !config.enabled {
		return
	}
	if _, ok := config.keys[key]; !ok {
		return
	}
	worker.configuredKeyEvents.Add(1)
	worker.lastKey.Store(key)
	foregroundPID, foreground := worker.gameForegroundPID(config)
	worker.foregroundPID.Store(foregroundPID)
	if !foreground {
		worker.foregroundMisses.Add(1)
		return
	}
	if !down {
		worker.triggerUps.Add(1)
		return
	}
	worker.triggerDowns.Add(1)
	if !worker.held[index].CompareAndSwap(false, true) {
		return
	}
	if device == 0 {
		device = 1
	}
	worker.heldDevice[index].Store(device)
	generation := worker.generation[index].Add(1)
	worker.repeatStarts.Add(1)
	worker.wg.Add(1)
	go worker.repeatKey(key, generation)
}

func (worker *keyboardWorkerRuntime) repeatKey(key uint32, generation uint64) {
	defer worker.wg.Done()
	defer worker.repeatStops.Add(1)
	index := int(key & 0x3ff)
	// The capture path forwards the original physical make stroke first. Each
	// iteration emits the balanced make/break pair consumed by the game. A
	// quarantined physical break does not change held/generation, so an
	// unrelated key cannot pause this loop while release is being confirmed.
	for worker.held[index].Load() && worker.generation[index].Load() == generation {
		config := worker.config.Load()
		if config == nil || !config.enabled {
			return
		}
		if _, ok := config.keys[key]; !ok {
			return
		}
		next := time.Now().Add(config.interval)
		foregroundPID, foreground := worker.gameForegroundPID(config)
		worker.foregroundPID.Store(foregroundPID)
		if foreground {
			if data, err := interceptionKeyboardData(key, false); err == nil {
				worker.lastScanCode.Store(uint32(data.MakeCode) | uint32(data.Flags&^interceptionKeyUp)<<16)
			}
			worker.lastOutputDevice.Store(1)
			worker.outputMu.Lock()
			active := worker.held[index].Load() && worker.generation[index].Load() == generation
			var emitErr error
			if active {
				emitErr = worker.emitKey(key, config.interval)
			}
			worker.outputMu.Unlock()
			if emitErr != nil {
				worker.held[index].Store(false)
				worker.outputFailures.Add(1)
				worker.fail(fmt.Errorf("Interception output for key 0x%X failed: %w", key, emitErr))
				return
			}
			if active {
				worker.outputPairs.Add(1)
			}
		} else {
			worker.outputGateMisses.Add(1)
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

func (worker *keyboardWorkerRuntime) fail(err error) {
	if err == nil {
		return
	}
	worker.faultMu.Lock()
	if worker.fault == nil {
		worker.fault = err
	}
	worker.faultMu.Unlock()
	if worker.threadID != 0 {
		procPostThreadMessageW.Call(uintptr(worker.threadID), wMQuit, 0, 0)
	}
}

func (worker *keyboardWorkerRuntime) runtimeFault() error {
	worker.faultMu.Lock()
	defer worker.faultMu.Unlock()
	return worker.fault
}

func (worker *keyboardWorkerRuntime) gameForeground(config *keyboardWorkerConfig) bool {
	_, foreground := worker.gameForegroundPID(config)
	return foreground
}

func (worker *keyboardWorkerRuntime) gameForegroundPID(config *keyboardWorkerConfig) (uint32, bool) {
	if worker.gameForegroundTest != nil {
		return 0, worker.gameForegroundTest(config)
	}
	foreground := windows.GetForegroundWindow()
	if foreground == 0 {
		return 0, false
	}
	processID := windowProcessID(foreground)
	_, ok := config.processes[processID]
	return processID, ok
}

func (worker *keyboardWorkerRuntime) diagnostics() KeyboardWorkerDiagnostics {
	diagnostics := KeyboardWorkerDiagnostics{
		ConfiguredKeyEvents: worker.configuredKeyEvents.Load(),
		ForegroundMisses:    worker.foregroundMisses.Load(),
		OutputGateMisses:    worker.outputGateMisses.Load(),
		TriggerDowns:        worker.triggerDowns.Load(),
		TriggerUps:          worker.triggerUps.Load(),
		RepeatStarts:        worker.repeatStarts.Load(),
		RepeatStops:         worker.repeatStops.Load(),
		OutputPairs:         worker.outputPairs.Load(),
		OutputFailures:      worker.outputFailures.Load(),
		SyntheticHookEvents: worker.syntheticHookEvents.Load(),
		ReleaseChecks:       worker.releaseChecks.Load(),
		LastKey:             worker.lastKey.Load(),
		LastScanCode:        worker.lastScanCode.Load(),
		LastDevice:          worker.lastDevice.Load(),
		LastOutputDevice:    worker.lastOutputDevice.Load(),
		ForegroundPID:       worker.foregroundPID.Load(),
	}
	config := worker.config.Load()
	if config != nil {
		for key := range config.keys {
			if worker.held[int(key&0x3ff)].Load() {
				diagnostics.HeldKeys = append(diagnostics.HeldKeys, key)
				diagnostics.HeldDevices = append(diagnostics.HeldDevices, worker.heldDevice[int(key&0x3ff)].Load())
			}
		}
	}
	return diagnostics
}

func (worker *keyboardWorkerRuntime) emitKey(key uint32, interval time.Duration) error {
	if worker.emitTest != nil {
		return worker.emitTest(key, interval)
	}
	if worker.backend == nil {
		return errors.New("Interception keyboard backend is unavailable")
	}
	// FlairBloom's audited Interception backend selects the first logical
	// keyboard returned by find_keyboard(), which is device 1. Keep output
	// independent from the physical trigger device for identical routing.
	return worker.backend.PressDevice(1, key, interval)
}

func (worker *keyboardWorkerRuntime) releaseHeldKeys() {
	for index := range worker.held {
		if !worker.held[index].Swap(false) {
			continue
		}
		worker.generation[index].Add(1)
		worker.heldDevice[index].Store(0)
		if worker.backend == nil {
			continue
		}
		key := uint32(index)
		worker.releaseKeyDevice(key, 1)
	}
}

func (worker *keyboardWorkerRuntime) releaseKeyDevice(key, device uint32) {
	if worker.backend == nil || device == 0 {
		return
	}
	worker.outputMu.Lock()
	_ = worker.backend.ReleaseDevice(device, key)
	worker.outputMu.Unlock()
}
