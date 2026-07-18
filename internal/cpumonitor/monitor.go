// Package cpumonitor samples and evaluates current-process CPU usage.
package cpumonitor

import "time"

type Sampler struct {
	processors int
	wall       time.Time
	cpu        time.Duration
	ready      bool
}

func NewSampler(processors int) *Sampler {
	if processors < 1 {
		processors = 1
	}
	return &Sampler{processors: processors}
}

// Sample returns process CPU percent normalized so that full use of every
// logical processor is 100 percent. The first sample establishes a baseline.
func (sampler *Sampler) Sample(now time.Time, cpuTime time.Duration) (float64, bool) {
	if !sampler.ready {
		sampler.wall, sampler.cpu, sampler.ready = now, cpuTime, true
		return 0, false
	}
	wallDelta, cpuDelta := now.Sub(sampler.wall), cpuTime-sampler.cpu
	sampler.wall, sampler.cpu = now, cpuTime
	if wallDelta <= 0 || cpuDelta < 0 {
		return 0, false
	}
	percent := float64(cpuDelta) / float64(wallDelta) * 100 / float64(sampler.processors)
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return percent, true
}

type Sustained struct {
	highSince time.Time
	warned    bool
}

// Observe emits true once for each continuous high-CPU episode. Falling below
// the threshold, disabling the feature, or losing a valid sample rearms it.
func (monitor *Sustained) Observe(now time.Time, percent float64, valid, enabled bool, threshold float64, duration time.Duration) bool {
	if !enabled || !valid || percent < threshold {
		monitor.highSince = time.Time{}
		monitor.warned = false
		return false
	}
	if monitor.highSince.IsZero() {
		monitor.highSince = now
		return false
	}
	if monitor.warned || now.Sub(monitor.highSince) < duration {
		return false
	}
	monitor.warned = true
	return true
}
