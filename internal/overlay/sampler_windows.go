package overlay

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unsafe"

	"genshintools/internal/gamewindow"

	"golang.org/x/sys/windows"
)

const (
	allProcessorGroups = 0xFFFF
	pdhMoreData        = 0x800007D2
	pdhValidData       = 0x00000000
	pdhNewData         = 0x00000001
	pdhFormatDouble    = 0x00000200
)

var (
	pdhDLL                          = windows.NewLazySystemDLL("pdh.dll")
	procPdhOpenQuery                = pdhDLL.NewProc("PdhOpenQueryW")
	procPdhAddEnglishCounter        = pdhDLL.NewProc("PdhAddEnglishCounterW")
	procPdhCollectQueryData         = pdhDLL.NewProc("PdhCollectQueryData")
	procPdhGetFormattedCounterArray = pdhDLL.NewProc("PdhGetFormattedCounterArrayW")
	procPdhCloseQuery               = pdhDLL.NewProc("PdhCloseQuery")
)

type processSample struct {
	At      time.Time
	CPUTime int64
}

type NativeSampler struct {
	target     gamewindow.Target
	process    windows.Handle
	processors uint32
	previous   processSample
	gpu        *gpuQuery
	fps        *fpsTrace
	fpsReason  string
}

func NewNativeSampler(target gamewindow.Target) (*NativeSampler, error) {
	if err := gamewindow.Validate(target); err != nil {
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, target.PID)
	if err != nil {
		return nil, err
	}
	sampler := &NativeSampler{target: target, process: process, processors: windows.GetActiveProcessorCount(allProcessorGroups)}
	if sampler.processors == 0 {
		sampler.processors = 1
	}
	sampler.previous, err = sampler.readProcessSample()
	if err != nil {
		windows.CloseHandle(process)
		return nil, err
	}
	sampler.gpu, _ = newGPUQuery(target.PID)
	if errETWLayout != nil {
		sampler.fpsReason = errETWLayout.Error()
	} else if sampler.fps, err = startFPSTrace(target.PID); err != nil {
		sampler.fpsReason = err.Error()
	}
	return sampler, nil
}

func (sampler *NativeSampler) Sample() (Stats, error) {
	if err := gamewindow.Validate(sampler.target); err != nil {
		return Stats{}, err
	}
	current, err := sampler.readProcessSample()
	if err != nil {
		return Stats{}, err
	}
	stats := Stats{FPSReason: sampler.fpsReason}
	if sampler.fps != nil {
		stats.FPS, stats.FPSValid = sampler.fps.sample(), true
	}
	stats.CPU, stats.CPUValid = calculateCPU(sampler.previous, current, sampler.processors)
	sampler.previous = current
	if sampler.gpu == nil {
		stats.GPUReason = "GPU Engine PDH counter unavailable"
	} else if value, err := sampler.gpu.sample(); err != nil {
		stats.GPUReason = err.Error()
	} else {
		stats.GPU, stats.GPUValid = value, true
	}
	return stats, nil
}

func (sampler *NativeSampler) Close() {
	if sampler.gpu != nil {
		sampler.gpu.close()
		sampler.gpu = nil
	}
	if sampler.fps != nil {
		sampler.fps.close()
		sampler.fps = nil
	}
	if sampler.process != 0 {
		windows.CloseHandle(sampler.process)
		sampler.process = 0
	}
}

func (sampler *NativeSampler) readProcessSample() (processSample, error) {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(sampler.process, &creation, &exit, &kernel, &user); err != nil {
		return processSample{}, err
	}
	return processSample{At: time.Now(), CPUTime: kernel.Nanoseconds() + user.Nanoseconds()}, nil
}

func calculateCPU(previous, current processSample, processors uint32) (float64, bool) {
	wall := current.At.Sub(previous.At).Nanoseconds()
	cpu := current.CPUTime - previous.CPUTime
	if wall <= 0 || cpu < 0 || processors == 0 {
		return 0, false
	}
	value := float64(cpu) / float64(wall*int64(processors)) * 100
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, false
	}
	return min(value, 100), true
}

type pdhCounterValueItemDouble struct {
	Name   *uint16
	Status uint32
	_      uint32
	Value  float64
}

type gpuQuery struct {
	query   uintptr
	counter uintptr
	pidMark string
}

func newGPUQuery(pid uint32) (*gpuQuery, error) {
	var query uintptr
	if status, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&query))); uint32(status) != 0 {
		return nil, fmt.Errorf("PdhOpenQuery status 0x%08X", uint32(status))
	}
	result := &gpuQuery{query: query, pidMark: fmt.Sprintf("pid_%d_", pid)}
	path, _ := windows.UTF16PtrFromString(fmt.Sprintf(`\GPU Engine(*pid_%d_*engtype_3D)\Utilization Percentage`, pid))
	if status, _, _ := procPdhAddEnglishCounter.Call(query, uintptr(unsafe.Pointer(path)), 0, uintptr(unsafe.Pointer(&result.counter))); uint32(status) != 0 {
		result.close()
		return nil, fmt.Errorf("PdhAddEnglishCounter status 0x%08X", uint32(status))
	}
	procPdhCollectQueryData.Call(query)
	return result, nil
}

func (query *gpuQuery) sample() (float64, error) {
	if status, _, _ := procPdhCollectQueryData.Call(query.query); uint32(status) != 0 {
		return 0, fmt.Errorf("PdhCollectQueryData status 0x%08X", uint32(status))
	}
	var size, count uint32
	status, _, _ := procPdhGetFormattedCounterArray.Call(query.counter, pdhFormatDouble, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	if uint32(status) != pdhMoreData || size == 0 || count == 0 || size > 16<<20 || count > 100_000 {
		return 0, errors.New("GPU counter returned no bounded target instances")
	}
	buffer := make([]byte, size)
	status, _, _ = procPdhGetFormattedCounterArray.Call(query.counter, pdhFormatDouble, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buffer[0])))
	if uint32(status) != 0 {
		return 0, fmt.Errorf("PdhGetFormattedCounterArray status 0x%08X", uint32(status))
	}
	items := unsafe.Slice((*pdhCounterValueItemDouble)(unsafe.Pointer(&buffer[0])), int(count))
	var total float64
	valid := false
	for _, item := range items {
		name := strings.ToLower(windows.UTF16PtrToString(item.Name))
		if !strings.Contains(name, query.pidMark) || (item.Status != pdhValidData && item.Status != pdhNewData) || math.IsNaN(item.Value) || math.IsInf(item.Value, 0) {
			continue
		}
		total += max(0, item.Value)
		valid = true
	}
	if !valid {
		return 0, errors.New("GPU counter contains no valid target values")
	}
	return min(total, 100), nil
}

func (query *gpuQuery) close() {
	if query.query != 0 {
		procPdhCloseQuery.Call(query.query)
		query.query, query.counter = 0, 0
	}
}
