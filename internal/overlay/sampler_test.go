package overlay

import (
	"math"
	"testing"
	"time"

	"genshintools/internal/gamewindow"

	"golang.org/x/sys/windows"
)

func TestCalculateCPUIsNormalizedAndBounded(t *testing.T) {
	start := time.Unix(1, 0)
	previous := processSample{At: start, CPUTime: 100}
	current := processSample{At: start.Add(time.Second), CPUTime: 2_000_000_100}
	value, valid := calculateCPU(previous, current, 4)
	if !valid || math.Abs(value-50) > 0.001 {
		t.Fatalf("CPU = %f valid=%v, want 50", value, valid)
	}
	current.CPUTime = 10_000_000_100
	value, valid = calculateCPU(previous, current, 1)
	if !valid || value != 100 {
		t.Fatalf("bounded CPU = %f valid=%v", value, valid)
	}
}

func TestConfigRejectsEmptyAndUnsafeOffsets(t *testing.T) {
	config := DefaultConfig()
	config.ShowFPS, config.ShowCPU, config.ShowGPU = false, false, false
	if _, err := config.Normalized(); err == nil {
		t.Fatal("empty overlay unexpectedly accepted")
	}
	config = DefaultConfig()
	config.OffsetX = 5000
	if _, err := config.Normalized(); err == nil {
		t.Fatal("unsafe offset unexpectedly accepted")
	}
}

func TestETWABIStructLayouts(t *testing.T) {
	if err := validateETWLayouts(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeSamplerSamplesCPUAndReleasesNativeSources(t *testing.T) {
	process, err := windows.GetCurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &creation, &exit, &kernel, &user); err != nil {
		t.Fatal(err)
	}
	sampler, err := NewNativeSampler(gamewindow.Target{PID: windows.GetCurrentProcessId(), CreationTime: creation.Nanoseconds()})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	stats, err := sampler.Sample()
	if err != nil {
		sampler.Close()
		t.Fatal(err)
	}
	if !stats.CPUValid {
		sampler.Close()
		t.Fatal("process CPU sample is not valid")
	}
	if sampler.fps == nil && stats.FPSReason == "" {
		sampler.Close()
		t.Fatal("FPS source neither started nor reported a degradation reason")
	}
	sampler.Close()
	if sampler.process != 0 || sampler.gpu != nil || sampler.fps != nil {
		t.Fatal("native sampler did not release all owned sources")
	}
}
