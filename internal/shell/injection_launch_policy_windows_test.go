package shell

import (
	"testing"

	"genshintools/internal/launch"
)

func TestSafeModeBlocksEnabledAndFallbackInjectionModules(t *testing.T) {
	for _, test := range []struct {
		name     string
		safeMode bool
		modules  []string
		fallback string
		want     bool
	}{
		{name: "enabled modules", safeMode: true, modules: []string{"plugin"}, want: true},
		{name: "fallback module", safeMode: true, fallback: "legacy", want: true},
		{name: "nothing selected", safeMode: true, want: false},
		{name: "safe mode disabled", modules: []string{"plugin"}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := injectionBlockedBySafeMode(test.safeMode, test.modules, test.fallback); got != test.want {
				t.Fatalf("injectionBlockedBySafeMode()=%v, want %v", got, test.want)
			}
		})
	}
}

func TestLaunchBusyCoversHelperStartingAndRunningStates(t *testing.T) {
	for _, app := range []*application{
		{injectionLaunching: true},
		{launchSnap: launch.Snapshot{State: launch.StateStarting}},
		{launchSnap: launch.Snapshot{State: launch.StateRunning}},
	} {
		if !app.launchBusy() {
			t.Fatalf("launch state was not treated as busy: %+v", app.launchSnap)
		}
	}
	if (&application{}).launchBusy() {
		t.Fatal("idle application was treated as launch-busy")
	}
}
