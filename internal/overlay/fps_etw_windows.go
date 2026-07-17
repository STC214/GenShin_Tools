package overlay

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wnodeFlagTracedGUID            = 0x00020000
	eventTraceRealTimeMode         = 0x00000100
	processTraceRealTime           = 0x00000100
	processTraceEventRecord        = 0x10000000
	eventControlEnable             = 1
	eventControlDisable            = 0
	eventTraceControlStop          = 1
	dxgiPresentStartID             = 0x002A
	dxgiPresentKeyword      uint64 = 0x8000000000000002
)

var dxgiProvider = windows.GUID{Data1: 0xCA11C036, Data2: 0x0102, Data3: 0x4A2D, Data4: [8]byte{0xA6, 0xAD, 0xF0, 0x3C, 0xFE, 0xD5, 0xD3, 0xC9}}

type wnodeHeader struct {
	BufferSize    uint32
	ProviderID    uint32
	History       uint64
	Timestamp     uint64
	GUID          windows.GUID
	ClientContext uint32
	Flags         uint32
}

type eventTraceProperties struct {
	Wnode                                wnodeHeader
	BufferSize, MinimumBuffers           uint32
	MaximumBuffers, MaximumFileSize      uint32
	LogFileMode, FlushTimer, EnableFlags uint32
	AgeLimit                             int32
	NumberOfBuffers, FreeBuffers         uint32
	EventsLost, BuffersWritten           uint32
	LogBuffersLost, RealTimeBuffersLost  uint32
	LoggerThreadID                       uintptr
	LogFileNameOffset, LoggerNameOffset  uint32
}

type traceProperties struct {
	Properties  eventTraceProperties
	SessionName [128]uint16
}

type eventDescriptor struct {
	ID      uint16
	Version uint8
	Channel uint8
	Level   uint8
	Opcode  uint8
	Task    uint16
	Keyword uint64
}

type eventHeader struct {
	Size, HeaderType, Flags, Property uint16
	ThreadID, ProcessID               uint32
	Timestamp                         int64
	ProviderID                        windows.GUID
	Descriptor                        eventDescriptor
	ProcessorTime                     uint64
	ActivityID                        windows.GUID
}

type eventRecord struct {
	Header                              eventHeader
	BufferContext                       uint32
	ExtendedDataCount, UserDataLength   uint16
	ExtendedData, UserData, UserContext uintptr
}

type eventTraceLogfile struct {
	LogFileName, LoggerName        *uint16
	CurrentTime                    int64
	BuffersRead, ProcessTraceMode  uint32
	CurrentEvent                   [88]byte
	LogfileHeader                  [280]byte
	BufferCallback                 uintptr
	BufferSize, Filled, EventsLost uint32
	_                              uint32
	EventRecordCallback            uintptr
	IsKernelTrace                  uint32
	_                              uint32
	Context                        uintptr
}

var (
	advapiETW          = windows.NewLazySystemDLL("advapi32.dll")
	procStartTrace     = advapiETW.NewProc("StartTraceW")
	procEnableTraceEx2 = advapiETW.NewProc("EnableTraceEx2")
	procOpenTrace      = advapiETW.NewProc("OpenTraceW")
	procProcessTrace   = advapiETW.NewProc("ProcessTrace")
	procControlTrace   = advapiETW.NewProc("ControlTraceW")
	procCloseTrace     = advapiETW.NewProc("CloseTrace")
	fpsTraces          sync.Map
	fpsToken           atomic.Uint64
)

var fpsEventCallback = windows.NewCallback(func(recordPointer uintptr) uintptr {
	if recordPointer == 0 {
		return 0
	}
	record := (*eventRecord)(unsafe.Pointer(recordPointer))
	value, exists := fpsTraces.Load(record.UserContext)
	if !exists {
		return 0
	}
	trace := value.(*fpsTrace)
	if record.Header.ProcessID == trace.pid && record.Header.ProviderID == dxgiProvider && record.Header.Descriptor.ID == dxgiPresentStartID {
		trace.frames.Add(1)
	}
	return 0
})

type fpsTrace struct {
	pid           uint32
	token         uintptr
	sessionName   string
	sessionHandle uint64
	traceHandle   uint64
	properties    traceProperties
	frames        atomic.Uint64
	done          chan struct{}
	closeOnce     sync.Once
}

func startFPSTrace(pid uint32) (*fpsTrace, error) {
	generation := fpsToken.Add(1)
	trace := &fpsTrace{pid: pid, token: uintptr(generation), sessionName: fmt.Sprintf("GenshinTools-FPS-%d-%d", windows.GetCurrentProcessId(), generation), done: make(chan struct{})}
	trace.properties.Properties.Wnode.BufferSize = uint32(unsafe.Sizeof(trace.properties))
	trace.properties.Properties.Wnode.ClientContext = 1
	trace.properties.Properties.Wnode.Flags = wnodeFlagTracedGUID
	trace.properties.Properties.BufferSize = 64
	trace.properties.Properties.MinimumBuffers = 4
	trace.properties.Properties.MaximumBuffers = 16
	trace.properties.Properties.LogFileMode = eventTraceRealTimeMode
	trace.properties.Properties.FlushTimer = 1
	trace.properties.Properties.LoggerNameOffset = uint32(unsafe.Offsetof(trace.properties.SessionName))
	name, _ := windows.UTF16PtrFromString(trace.sessionName)
	status, _, _ := procStartTrace.Call(uintptr(unsafe.Pointer(&trace.sessionHandle)), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&trace.properties.Properties)))
	if uint32(status) != 0 {
		return nil, fmt.Errorf("StartTrace DXGI status 0x%08X", uint32(status))
	}
	cleanup := true
	defer func() {
		if cleanup {
			trace.stopController()
		}
	}()
	status, _, _ = procEnableTraceEx2.Call(uintptr(trace.sessionHandle), uintptr(unsafe.Pointer(&dxgiProvider)), eventControlEnable, 5, uintptr(dxgiPresentKeyword), 0, 0, 0)
	if uint32(status) != 0 {
		return nil, fmt.Errorf("EnableTraceEx2 DXGI status 0x%08X", uint32(status))
	}
	fpsTraces.Store(trace.token, trace)
	logfile := eventTraceLogfile{LoggerName: name, ProcessTraceMode: processTraceRealTime | processTraceEventRecord, EventRecordCallback: fpsEventCallback, Context: trace.token}
	handle, _, openErr := procOpenTrace.Call(uintptr(unsafe.Pointer(&logfile)))
	trace.traceHandle = uint64(handle)
	if trace.traceHandle == ^uint64(0) {
		fpsTraces.Delete(trace.token)
		return nil, fmt.Errorf("OpenTrace DXGI: %w", openErr)
	}
	cleanup = false
	go func() {
		defer close(trace.done)
		procProcessTrace.Call(uintptr(unsafe.Pointer(&trace.traceHandle)), 1, 0, 0)
	}()
	return trace, nil
}

func (trace *fpsTrace) sample() float64 {
	return float64(trace.frames.Swap(0))
}

func (trace *fpsTrace) close() {
	trace.closeOnce.Do(func() {
		fpsTraces.Delete(trace.token)
		trace.stopController()
		if trace.traceHandle != 0 && trace.traceHandle != ^uint64(0) {
			procCloseTrace.Call(uintptr(trace.traceHandle))
		}
		select {
		case <-trace.done:
		case <-time.After(2 * time.Second):
		}
	})
}

func (trace *fpsTrace) stopController() {
	if trace.sessionHandle == 0 {
		return
	}
	procEnableTraceEx2.Call(uintptr(trace.sessionHandle), uintptr(unsafe.Pointer(&dxgiProvider)), eventControlDisable, 0, 0, 0, 0, 0, 0)
	properties := traceProperties{}
	properties.Properties.Wnode.BufferSize = uint32(unsafe.Sizeof(properties))
	properties.Properties.LoggerNameOffset = uint32(unsafe.Offsetof(properties.SessionName))
	procControlTrace.Call(uintptr(trace.sessionHandle), 0, uintptr(unsafe.Pointer(&properties.Properties)), eventTraceControlStop)
	trace.sessionHandle = 0
}

func validateETWLayouts() error {
	checks := []struct {
		got, want uintptr
		name      string
	}{
		{unsafe.Sizeof(wnodeHeader{}), 48, "WNODE_HEADER"},
		{unsafe.Sizeof(eventTraceProperties{}), 120, "EVENT_TRACE_PROPERTIES"},
		{unsafe.Sizeof(eventHeader{}), 80, "EVENT_HEADER"},
		{unsafe.Sizeof(eventRecord{}), 112, "EVENT_RECORD"},
		{unsafe.Sizeof(eventTraceLogfile{}), 448, "EVENT_TRACE_LOGFILEW"},
		{unsafe.Offsetof(eventTraceLogfile{}.EventRecordCallback), 424, "EVENT_TRACE_LOGFILEW.EventRecordCallback"},
		{unsafe.Offsetof(eventTraceLogfile{}.Context), 440, "EVENT_TRACE_LOGFILEW.Context"},
	}
	for _, check := range checks {
		if check.got != check.want {
			return fmt.Errorf("%s ABI size/offset %d, want %d", check.name, check.got, check.want)
		}
	}
	return nil
}

var errETWLayout = validateETWLayouts()
