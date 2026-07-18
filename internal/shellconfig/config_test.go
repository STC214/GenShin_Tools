package shellconfig

import "testing"

func TestDefaultConfigIsSafeAndValid(t *testing.T) {
	config, err := DefaultConfig().Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if config.Theme != ThemeDark || !config.MinimizeToTray || config.ProcessPriority != PriorityNormal {
		t.Fatalf("defaults = %+v", config)
	}
}

func TestConfigRejectsUnsafeOrUnknownValues(t *testing.T) {
	tests := []Config{
		{Language: "unknown", Theme: ThemeDark, ProcessPriority: PriorityNormal, CPUWarningThreshold: 25, CPUWarningDurationMS: 10_000},
		{Language: LanguageEN, Theme: "video", ProcessPriority: PriorityNormal, CPUWarningThreshold: 25, CPUWarningDurationMS: 10_000},
		{Language: LanguageEN, Theme: ThemeDark, ProcessPriority: "realtime", CPUWarningThreshold: 25, CPUWarningDurationMS: 10_000},
		{Language: LanguageEN, Theme: ThemeDark, ProcessPriority: PriorityNormal, CPUWarningThreshold: 0, CPUWarningDurationMS: 10_000},
	}
	for _, config := range tests {
		if _, err := config.Normalized(); err == nil {
			t.Fatalf("invalid config accepted: %+v", config)
		}
	}
}
