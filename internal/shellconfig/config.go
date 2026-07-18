package shellconfig

import (
	"errors"
	"strings"
)

const (
	LanguageSystem = "system"
	LanguageZH     = "zh-CN"
	LanguageEN     = "en-US"

	ThemeDark   = "dark"
	ThemeSystem = "system"

	PriorityBelowNormal = "below-normal"
	PriorityNormal      = "normal"
	PriorityAboveNormal = "above-normal"
)

type Config struct {
	Language             string `json:"language"`
	Theme                string `json:"theme"`
	MinimizeToTray       bool   `json:"minimizeToTray"`
	RememberWindowSize   bool   `json:"rememberWindowSize"`
	EnforceMinimumSize   bool   `json:"enforceMinimumSize"`
	ProcessPriority      string `json:"processPriority"`
	CPUWarningEnabled    bool   `json:"cpuWarningEnabled"`
	CPUWarningThreshold  int    `json:"cpuWarningThreshold"`
	CPUWarningDurationMS int    `json:"cpuWarningDurationMs"`
}

func DefaultConfig() Config {
	return Config{Language: LanguageSystem, Theme: ThemeDark, MinimizeToTray: true, RememberWindowSize: true, EnforceMinimumSize: true, ProcessPriority: PriorityNormal, CPUWarningEnabled: true, CPUWarningThreshold: 25, CPUWarningDurationMS: 10_000}
}

func (config Config) Normalized() (Config, error) {
	config.Language = strings.TrimSpace(config.Language)
	config.Theme = strings.TrimSpace(config.Theme)
	config.ProcessPriority = strings.TrimSpace(config.ProcessPriority)
	if !oneOf(config.Language, LanguageSystem, LanguageZH, LanguageEN) {
		return Config{}, errors.New("shell language is invalid")
	}
	if !oneOf(config.Theme, ThemeDark, ThemeSystem) {
		return Config{}, errors.New("shell theme is invalid")
	}
	if !oneOf(config.ProcessPriority, PriorityBelowNormal, PriorityNormal, PriorityAboveNormal) {
		return Config{}, errors.New("shell process priority is invalid")
	}
	if config.CPUWarningThreshold < 5 || config.CPUWarningThreshold > 100 {
		return Config{}, errors.New("shell CPU warning threshold must be within 5..100 percent")
	}
	if config.CPUWarningDurationMS < 3_000 || config.CPUWarningDurationMS > 300_000 {
		return Config{}, errors.New("shell CPU warning duration must be within 3000..300000 ms")
	}
	return config, nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
