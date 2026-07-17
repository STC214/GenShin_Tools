// Package overlay implements the S08 click-through game performance overlay.
package overlay

import "errors"

type Config struct {
	Enabled bool `json:"enabled"`
	ShowFPS bool `json:"showFps"`
	ShowCPU bool `json:"showCpu"`
	ShowGPU bool `json:"showGpu"`
	OffsetX int  `json:"offsetX"`
	OffsetY int  `json:"offsetY"`
}

func DefaultConfig() Config {
	return Config{ShowFPS: true, ShowCPU: true, ShowGPU: true, OffsetX: 16, OffsetY: 16}
}

func (config Config) Normalized() (Config, error) {
	if config.OffsetX < -4000 || config.OffsetX > 4000 || config.OffsetY < -4000 || config.OffsetY > 4000 {
		return Config{}, errors.New("overlay offsets must be within -4000..4000")
	}
	if !config.ShowFPS && !config.ShowCPU && !config.ShowGPU {
		return Config{}, errors.New("overlay must show at least one metric")
	}
	return config, nil
}

type Stats struct {
	FPS, CPU, GPU                float64
	FPSValid, CPUValid, GPUValid bool
	FPSReason, GPUReason         string
}
