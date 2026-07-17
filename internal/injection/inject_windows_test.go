package injection

import (
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
	pid, err := launchSuspendedAndInject(executable, filepath.Dir(executable), []string{"-test.run=^TestInjectionFixtureChild$"}, dll, 5*time.Second)
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
