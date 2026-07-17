package injection

import "testing"

func TestConfigDefaultsAreSafeAndValid(t *testing.T) {
	config, err := DefaultConfig().Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if config.Enabled || config.RiskAcknowledged || !config.ElevatedHelper {
		t.Fatalf("unsafe defaults: %+v", config)
	}
}

func TestConfigRejectsInvalidTimeoutsAndModuleID(t *testing.T) {
	tests := []Config{
		{HelperTimeoutMS: 2999, RemoteTimeoutMS: 1000},
		{HelperTimeoutMS: 60001, RemoteTimeoutMS: 1000},
		{HelperTimeoutMS: 5000, RemoteTimeoutMS: 5000},
		{HelperTimeoutMS: 5000, RemoteTimeoutMS: 999},
		{ModuleID: `..\module`, HelperTimeoutMS: 5000, RemoteTimeoutMS: 1000},
	}
	for _, config := range tests {
		if _, err := config.Normalized(); err == nil {
			t.Fatalf("invalid config accepted: %+v", config)
		}
	}
}
