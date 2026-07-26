//go:build windows && 386

package main

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	exceptionDebugEvent    = 1
	exitProcessDebugEvent  = 5
	exceptionBreakpoint    = 0x80000003
	exceptionSingleStep    = 0x80000004
	dbgContinue            = 0x00010002
	dbgExceptionNotHandled = 0x80010001
	contextI386            = 0x00010000
	contextControl         = contextI386 | 0x00000001
	contextInteger         = contextI386 | 0x00000002
	threadSuspendResume    = 0x0002
	errorSemTimeout        = syscall.Errno(121)
)

type x86FloatingSaveArea struct {
	ControlWord   uint32
	StatusWord    uint32
	TagWord       uint32
	ErrorOffset   uint32
	ErrorSelector uint32
	DataOffset    uint32
	DataSelector  uint32
	RegisterArea  [80]byte
	Cr0NpxState   uint32
}

type x86Context struct {
	ContextFlags uint32
	Dr0          uint32
	Dr1          uint32
	Dr2          uint32
	Dr3          uint32
	Dr6          uint32
	Dr7          uint32
	FloatSave    x86FloatingSaveArea
	SegGs        uint32
	SegFs        uint32
	SegEs        uint32
	SegDs        uint32
	Edi          uint32
	Esi          uint32
	Ebx          uint32
	Edx          uint32
	Ecx          uint32
	Eax          uint32
	Ebp          uint32
	Eip          uint32
	SegCs        uint32
	EFlags       uint32
	Esp          uint32
	SegSs        uint32
	Extended     [512]byte
}

type debugEvent struct {
	Code      uint32
	ProcessID uint32
	ThreadID  uint32
	Info      [84]byte
}

type apiBreakpoint struct {
	name          string
	address       uintptr
	argumentCount int
	originalByte  byte
	armed         bool
}

type keybdDebugger struct {
	process     windows.Handle
	processID   uint32
	breakpoints []apiBreakpoint
	started     time.Time
	stop        chan struct{}
	done        chan struct{}
	ready       chan struct{}
	stopOnce    sync.Once
	eventsMu    sync.Mutex
	events      []apiCallRecord
	runError    error
	initError   error
	stepping    map[uint32]int
}

var (
	debugKernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procDebugActiveProcess        = debugKernel32.NewProc("DebugActiveProcess")
	procDebugActiveProcessStop    = debugKernel32.NewProc("DebugActiveProcessStop")
	procDebugSetProcessKillOnExit = debugKernel32.NewProc("DebugSetProcessKillOnExit")
	procWaitForDebugEvent         = debugKernel32.NewProc("WaitForDebugEvent")
	procContinueDebugEvent        = debugKernel32.NewProc("ContinueDebugEvent")
	procGetThreadContext          = debugKernel32.NewProc("GetThreadContext")
	procSetThreadContext          = debugKernel32.NewProc("SetThreadContext")
	procFlushInstructionCache     = debugKernel32.NewProc("FlushInstructionCache")
)

var tracedUser32APIs = []struct {
	name          string
	argumentCount int
}{
	{name: "keybd_event", argumentCount: 4},
	{name: "SendInput", argumentCount: 3},
	{name: "PostMessageA", argumentCount: 4},
	{name: "PostMessageW", argumentCount: 4},
	{name: "SendMessageA", argumentCount: 4},
	{name: "SendMessageW", argumentCount: 4},
	{name: "PostThreadMessageA", argumentCount: 4},
	{name: "PostThreadMessageW", argumentCount: 4},
}

func startKeybdDebugger(processID int, startedAt time.Time) (*keybdDebugger, error) {
	debugger := &keybdDebugger{
		processID: uint32(processID),
		started:   startedAt,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		ready:     make(chan struct{}),
		stepping:  make(map[uint32]int),
	}
	go debugger.initializeAndRun()
	select {
	case <-debugger.ready:
		if debugger.initError != nil {
			<-debugger.done
			return nil, debugger.initError
		}
		return debugger, nil
	case <-time.After(3 * time.Second):
		debugger.stopOnce.Do(func() { close(debugger.stop) })
		<-debugger.done
		return nil, fmt.Errorf("input API debugger initialization timed out")
	}
}

func (debugger *keybdDebugger) initializeAndRun() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if unsafe.Sizeof(x86Context{}) != 716 || unsafe.Sizeof(debugEvent{}) != 96 {
		debugger.finishInitialization(fmt.Errorf(
			"unexpected debugger structure sizes: CONTEXT=%d DEBUG_EVENT=%d",
			unsafe.Sizeof(x86Context{}),
			unsafe.Sizeof(debugEvent{}),
		))
		return
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_QUERY_INFORMATION|
			windows.PROCESS_VM_OPERATION|
			windows.PROCESS_VM_READ|
			windows.PROCESS_VM_WRITE,
		false,
		debugger.processID,
	)
	if err != nil {
		debugger.finishInitialization(fmt.Errorf("OpenProcess: %w", err))
		return
	}
	debugger.process = process

	seenAddresses := make(map[uintptr]bool, len(tracedUser32APIs))
	for _, api := range tracedUser32APIs {
		address, addressErr := remoteUser32ProcAddress(debugger.processID, api.name)
		if addressErr != nil {
			windows.CloseHandle(process)
			debugger.finishInitialization(addressErr)
			return
		}
		if seenAddresses[address] {
			continue
		}
		seenAddresses[address] = true
		var original byte
		var read uintptr
		if readErr := windows.ReadProcessMemory(process, address, &original, 1, &read); readErr != nil || read != 1 {
			windows.CloseHandle(process)
			debugger.finishInitialization(fmt.Errorf("read %s entry: %w", api.name, readErr))
			return
		}
		debugger.breakpoints = append(debugger.breakpoints, apiBreakpoint{
			name:          api.name,
			address:       address,
			argumentCount: api.argumentCount,
			originalByte:  original,
		})
	}

	if result, _, callErr := procDebugActiveProcess.Call(uintptr(debugger.processID)); result == 0 {
		windows.CloseHandle(process)
		debugger.finishInitialization(fmt.Errorf("DebugActiveProcess: %w", callErr))
		return
	}
	procDebugSetProcessKillOnExit.Call(0)
	for index := range debugger.breakpoints {
		if err := debugger.writeCodeByte(index, 0xcc); err != nil {
			debugger.restoreAllBreakpoints()
			procDebugActiveProcessStop.Call(uintptr(debugger.processID))
			windows.CloseHandle(process)
			debugger.finishInitialization(fmt.Errorf("install %s breakpoint: %w", debugger.breakpoints[index].name, err))
			return
		}
		debugger.breakpoints[index].armed = true
	}
	debugger.finishInitialization(nil)
	debugger.run()
}

func (debugger *keybdDebugger) finishInitialization(err error) {
	debugger.initError = err
	close(debugger.ready)
	if err != nil {
		close(debugger.done)
	}
}

func remoteUser32ProcAddress(processID uint32, procedureName string) (uintptr, error) {
	user32Name, _ := windows.UTF16PtrFromString("user32.dll")
	localBase, _, callErr := procGetModuleHandleW.Call(uintptr(unsafe.Pointer(user32Name)))
	if localBase == 0 {
		return 0, fmt.Errorf("GetModuleHandleW(user32.dll): %w", callErr)
	}
	localProcedure := user32.NewProc(procedureName).Addr()
	if localProcedure < localBase {
		return 0, fmt.Errorf("%s address precedes local user32 base", procedureName)
	}
	procedureOffset := localProcedure - localBase

	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		snapshot, err := windows.CreateToolhelp32Snapshot(
			windows.TH32CS_SNAPMODULE|windows.TH32CS_SNAPMODULE32,
			processID,
		)
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		entry := windows.ModuleEntry32{Size: uint32(windows.SizeofModuleEntry32)}
		err = windows.Module32First(snapshot, &entry)
		for err == nil {
			if strings.EqualFold(windows.UTF16ToString(entry.Module[:]), "user32.dll") {
				windows.CloseHandle(snapshot)
				return entry.ModBaseAddr + procedureOffset, nil
			}
			err = windows.Module32Next(snapshot, &entry)
		}
		windows.CloseHandle(snapshot)
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	return 0, fmt.Errorf("locate remote user32.dll: %w", lastErr)
}

func (debugger *keybdDebugger) Stop() ([]apiCallRecord, error) {
	if debugger == nil {
		return nil, nil
	}
	debugger.stopOnce.Do(func() { close(debugger.stop) })
	<-debugger.done
	debugger.eventsMu.Lock()
	events := append([]apiCallRecord(nil), debugger.events...)
	debugger.eventsMu.Unlock()
	return events, debugger.runError
}

func (debugger *keybdDebugger) run() {
	defer close(debugger.done)
	defer windows.CloseHandle(debugger.process)
	defer procDebugActiveProcessStop.Call(uintptr(debugger.processID))
	defer debugger.restoreAllBreakpoints()

	for {
		select {
		case <-debugger.stop:
			debugger.restoreAllBreakpoints()
			return
		default:
		}

		var event debugEvent
		result, _, callErr := procWaitForDebugEvent.Call(uintptr(unsafe.Pointer(&event)), 50)
		if result == 0 {
			if callErr == errorSemTimeout {
				continue
			}
			debugger.runError = fmt.Errorf("WaitForDebugEvent: %w", callErr)
			return
		}

		continueStatus := uintptr(dbgContinue)
		if event.Code == exceptionDebugEvent {
			exceptionCode := binary.LittleEndian.Uint32(event.Info[0:4])
			exceptionAddress := uintptr(binary.LittleEndian.Uint32(event.Info[12:16]))
			breakpointIndex := debugger.breakpointAt(exceptionAddress)
			switch {
			case exceptionCode == exceptionBreakpoint && breakpointIndex >= 0:
				if err := debugger.captureCall(event.ThreadID, breakpointIndex); err != nil {
					debugger.runError = err
				}
			case exceptionCode == exceptionSingleStep:
				if index, exists := debugger.stepping[event.ThreadID]; exists {
					if err := debugger.finishSingleStep(event.ThreadID, index); err != nil {
						debugger.runError = err
					}
				} else {
					continueStatus = dbgExceptionNotHandled
				}
			case exceptionCode == exceptionBreakpoint:
				// Consume the debugger's initial breakpoint generated on attach.
			default:
				continueStatus = dbgExceptionNotHandled
			}
		}

		procContinueDebugEvent.Call(
			uintptr(event.ProcessID),
			uintptr(event.ThreadID),
			continueStatus,
		)
		if event.Code == exitProcessDebugEvent {
			return
		}
		if debugger.runError != nil {
			return
		}
	}
}

func (debugger *keybdDebugger) breakpointAt(address uintptr) int {
	for index := range debugger.breakpoints {
		if debugger.breakpoints[index].address == address {
			return index
		}
	}
	return -1
}

func (debugger *keybdDebugger) captureCall(threadID uint32, breakpointIndex int) error {
	breakpoint := &debugger.breakpoints[breakpointIndex]
	thread, err := windows.OpenThread(
		windows.THREAD_GET_CONTEXT|windows.THREAD_SET_CONTEXT|threadSuspendResume,
		false,
		threadID,
	)
	if err != nil {
		return fmt.Errorf("OpenThread(%d): %w", threadID, err)
	}
	defer windows.CloseHandle(thread)

	context := x86Context{ContextFlags: contextControl | contextInteger}
	if result, _, callErr := procGetThreadContext.Call(
		uintptr(thread),
		uintptr(unsafe.Pointer(&context)),
	); result == 0 {
		return fmt.Errorf("GetThreadContext(%d): %w", threadID, callErr)
	}

	stackSize := 4 + breakpoint.argumentCount*4
	stack := make([]byte, stackSize)
	var read uintptr
	if err := windows.ReadProcessMemory(
		debugger.process,
		uintptr(context.Esp),
		&stack[0],
		uintptr(len(stack)),
		&read,
	); err != nil || read != uintptr(len(stack)) {
		return fmt.Errorf("read %s stack: %w", breakpoint.name, err)
	}
	arguments := make([]uint32, breakpoint.argumentCount)
	for index := range arguments {
		offset := 4 + index*4
		arguments[index] = binary.LittleEndian.Uint32(stack[offset : offset+4])
	}
	record := apiCallRecord{
		ElapsedMicros: time.Since(debugger.started).Microseconds(),
		ThreadID:      threadID,
		API:           breakpoint.name,
		Arguments:     arguments,
	}
	if breakpoint.name == "SendInput" {
		record.Inputs = debugger.readSendInputs(arguments)
	}
	if interestingAPICall(record) {
		debugger.eventsMu.Lock()
		debugger.events = append(debugger.events, record)
		debugger.eventsMu.Unlock()
	}

	if err := debugger.writeCodeByte(breakpointIndex, breakpoint.originalByte); err != nil {
		return fmt.Errorf("restore %s byte: %w", breakpoint.name, err)
	}
	breakpoint.armed = false
	context.Eip = uint32(breakpoint.address)
	context.EFlags |= 0x100
	if result, _, callErr := procSetThreadContext.Call(
		uintptr(thread),
		uintptr(unsafe.Pointer(&context)),
	); result == 0 {
		return fmt.Errorf("SetThreadContext(%d): %w", threadID, callErr)
	}
	debugger.stepping[threadID] = breakpointIndex
	return nil
}

func interestingAPICall(record apiCallRecord) bool {
	switch record.API {
	case "PostMessageA", "PostMessageW", "SendMessageA", "SendMessageW", "PostThreadMessageA", "PostThreadMessageW":
		if len(record.Arguments) < 2 {
			return false
		}
		switch record.Arguments[1] {
		case wmKeyDown, wmKeyUp, wmSysKeyDown, wmSysKeyUp:
			return true
		default:
			return false
		}
	default:
		return true
	}
}

func (debugger *keybdDebugger) readSendInputs(arguments []uint32) []sendInputRecord {
	if len(arguments) < 3 || arguments[0] == 0 || arguments[1] == 0 {
		return nil
	}
	count := min(arguments[0], 16)
	size := arguments[2]
	if size < 20 || size > 128 {
		return nil
	}
	buffer := make([]byte, int(count*size))
	var read uintptr
	if err := windows.ReadProcessMemory(
		debugger.process,
		uintptr(arguments[1]),
		&buffer[0],
		uintptr(len(buffer)),
		&read,
	); err != nil || read != uintptr(len(buffer)) {
		return nil
	}
	inputs := make([]sendInputRecord, 0, count)
	for index := uint32(0); index < count; index++ {
		item := buffer[index*size : (index+1)*size]
		record := sendInputRecord{Type: binary.LittleEndian.Uint32(item[0:4])}
		if record.Type == 1 && len(item) >= 20 {
			record.VirtualKey = binary.LittleEndian.Uint16(item[4:6])
			record.ScanCode = binary.LittleEndian.Uint16(item[6:8])
			record.Flags = binary.LittleEndian.Uint32(item[8:12])
			record.Time = binary.LittleEndian.Uint32(item[12:16])
			record.ExtraInfo = binary.LittleEndian.Uint32(item[16:20])
		}
		inputs = append(inputs, record)
	}
	return inputs
}

func (debugger *keybdDebugger) finishSingleStep(threadID uint32, breakpointIndex int) error {
	breakpoint := &debugger.breakpoints[breakpointIndex]
	thread, err := windows.OpenThread(
		windows.THREAD_GET_CONTEXT|windows.THREAD_SET_CONTEXT|threadSuspendResume,
		false,
		threadID,
	)
	if err != nil {
		return fmt.Errorf("OpenThread single-step(%d): %w", threadID, err)
	}
	defer windows.CloseHandle(thread)

	context := x86Context{ContextFlags: contextControl}
	if result, _, callErr := procGetThreadContext.Call(
		uintptr(thread),
		uintptr(unsafe.Pointer(&context)),
	); result == 0 {
		return fmt.Errorf("GetThreadContext single-step(%d): %w", threadID, callErr)
	}
	context.EFlags &^= 0x100
	if result, _, callErr := procSetThreadContext.Call(
		uintptr(thread),
		uintptr(unsafe.Pointer(&context)),
	); result == 0 {
		return fmt.Errorf("SetThreadContext single-step(%d): %w", threadID, callErr)
	}
	if err := debugger.writeCodeByte(breakpointIndex, 0xcc); err != nil {
		return fmt.Errorf("rearm %s breakpoint: %w", breakpoint.name, err)
	}
	breakpoint.armed = true
	delete(debugger.stepping, threadID)
	return nil
}

func (debugger *keybdDebugger) restoreAllBreakpoints() {
	for index := range debugger.breakpoints {
		breakpoint := &debugger.breakpoints[index]
		if !breakpoint.armed {
			continue
		}
		_ = debugger.writeCodeByte(index, breakpoint.originalByte)
		breakpoint.armed = false
	}
}

func (debugger *keybdDebugger) writeCodeByte(breakpointIndex int, value byte) error {
	address := debugger.breakpoints[breakpointIndex].address
	var oldProtection uint32
	if err := windows.VirtualProtectEx(
		debugger.process,
		address,
		1,
		windows.PAGE_EXECUTE_READWRITE,
		&oldProtection,
	); err != nil {
		return err
	}
	defer windows.VirtualProtectEx(
		debugger.process,
		address,
		1,
		oldProtection,
		&oldProtection,
	)
	var written uintptr
	if err := windows.WriteProcessMemory(
		debugger.process,
		address,
		&value,
		1,
		&written,
	); err != nil {
		return err
	}
	if written != 1 {
		return fmt.Errorf("short write: %d", written)
	}
	procFlushInstructionCache.Call(
		uintptr(debugger.process),
		address,
		1,
	)
	return nil
}
