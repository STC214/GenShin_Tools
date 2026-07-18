package cpumonitor

import (
	"testing"
	"time"
)

func TestSamplerNormalizesAcrossProcessors(t *testing.T) {
	sampler := NewSampler(4)
	start := time.Unix(100, 0)
	if _, valid := sampler.Sample(start, time.Second); valid {
		t.Fatal("first sample must only establish a baseline")
	}
	percent, valid := sampler.Sample(start.Add(time.Second), 3*time.Second)
	if !valid || percent != 50 {
		t.Fatalf("got percent=%v valid=%v, want 50 true", percent, valid)
	}
}

func TestSustainedWarnsOnceAndRearms(t *testing.T) {
	var monitor Sustained
	start := time.Unix(100, 0)
	if monitor.Observe(start, 30, true, true, 25, 10*time.Second) {
		t.Fatal("warned before duration")
	}
	if !monitor.Observe(start.Add(10*time.Second), 30, true, true, 25, 10*time.Second) {
		t.Fatal("did not warn at duration")
	}
	if monitor.Observe(start.Add(20*time.Second), 30, true, true, 25, 10*time.Second) {
		t.Fatal("warned twice in one episode")
	}
	monitor.Observe(start.Add(21*time.Second), 5, true, true, 25, 10*time.Second)
	monitor.Observe(start.Add(22*time.Second), 30, true, true, 25, 10*time.Second)
	if !monitor.Observe(start.Add(32*time.Second), 30, true, true, 25, 10*time.Second) {
		t.Fatal("did not rearm after recovery")
	}
}

func TestInvalidOrDisabledSampleResetsEpisode(t *testing.T) {
	var monitor Sustained
	now := time.Unix(100, 0)
	monitor.Observe(now, 90, true, true, 25, 3*time.Second)
	monitor.Observe(now.Add(2*time.Second), 90, false, true, 25, 3*time.Second)
	if monitor.Observe(now.Add(4*time.Second), 90, true, true, 25, 3*time.Second) {
		t.Fatal("invalid sample did not reset duration")
	}
}
