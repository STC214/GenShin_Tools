package shell

import (
	"os"
	"path/filepath"
	"testing"

	"genshintools/internal/game"
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

func TestRefreshInjectionCandidateDetectsGameUpdate(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "YuanShen.exe")
	configPath := filepath.Join(root, "config.ini")
	if err := os.WriteFile(executable, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig := func(version string) {
		t.Helper()
		data := []byte("[General]\r\ngame_version=" + version + "\r\nchannel=1\r\n")
		if err := os.WriteFile(configPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig("7.0.0")
	previous, err := game.InspectRoot(root, "YuanShen.exe")
	if err != nil {
		t.Fatal(err)
	}
	writeConfig("7.1.0")
	current, changed, err := refreshInjectionCandidate(previous)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || current.Version != "7.1.0" || current.Server != game.ServerCNOfficial {
		t.Fatalf("refreshed candidate = %+v, changed=%v", current, changed)
	}
	stable, changed, err := refreshInjectionCandidate(current)
	if err != nil {
		t.Fatal(err)
	}
	if changed || stable.Version != current.Version {
		t.Fatalf("stable refresh = %+v, changed=%v", stable, changed)
	}
}
