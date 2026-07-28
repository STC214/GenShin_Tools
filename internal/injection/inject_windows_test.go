package injection

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestInjectionFixtureChild(t *testing.T) {
	if os.Getenv("GENSHINTOOLS_S09_CHILD") != "1" {
		return
	}
}

func TestLaunchSuspendedAndInjectOnOwnedFixture(t *testing.T) {
	if os.Getenv("GENSHINTOOLS_S09_CHILD") == "1" {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GENSHINTOOLS_S09_CHILD", "1")
	dll := filepath.Join(os.Getenv("SystemRoot"), "System32", "version.dll")
	pid, err := launchSuspendedAndInject(executable, filepath.Dir(executable), []string{"-test.run=^TestInjectionFixtureChild$"}, []string{dll}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	status, err := windows.WaitForSingleObject(process, 10_000)
	if err != nil || status != waitObject0 {
		t.Fatalf("fixture child wait status=0x%X err=%v", status, err)
	}
}

func TestRemoteModuleLoadedRequiresExactResidentModule(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := remoteModuleLoaded(windows.GetCurrentProcessId(), executable)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded {
		t.Fatal("current executable was not found in its own module snapshot")
	}
	if _, err := remoteModuleLoaded(0, executable); err == nil {
		t.Fatal("zero-PID module query was accepted")
	}
}

func TestReadyEventSignaledObservesWithoutMutating(t *testing.T) {
	name := fmt.Sprintf(`Local\GenshinTools.PluginReady.test.%d`, windows.GetCurrentProcessId())
	nameUTF16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	event, err := windows.CreateEvent(nil, 1, 0, nameUTF16)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(event)
	signaled, err := ReadyEventSignaled(name)
	if err != nil || signaled {
		t.Fatalf("unsignaled event result=%t err=%v", signaled, err)
	}
	if err := windows.SetEvent(event); err != nil {
		t.Fatal(err)
	}
	signaled, err = ReadyEventSignaled(name)
	if err != nil || !signaled {
		t.Fatalf("signaled event result=%t err=%v", signaled, err)
	}
}
