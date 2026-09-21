package injection

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestModuleSnapshotRetriesBadLength(t *testing.T) {
	attempts := 0
	handle, err := moduleSnapshotUntil(123, time.Now().Add(time.Second), func(flags, pid uint32) (windows.Handle, error) {
		attempts++
		if flags != th32csSnapModule|th32csSnapModule32 || pid != 123 {
			t.Fatalf("unexpected snapshot arguments: flags=%d pid=%d", flags, pid)
		}
		if attempts < 3 {
			return windows.InvalidHandle, windows.ERROR_BAD_LENGTH
		}
		return windows.Handle(42), nil
	})
	if err != nil || handle != 42 || attempts != 3 {
		t.Fatalf("handle=%v attempts=%d err=%v", handle, attempts, err)
	}
}

func TestModuleSnapshotFailureBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		budget    time.Duration
		wantCalls int
	}{
		{"access denied is not retried", windows.ERROR_ACCESS_DENIED, time.Second, 1},
		{"expired deadline", windows.ERROR_BAD_LENGTH, -time.Second, 1},
		{"persistent transient error", windows.ERROR_BAD_LENGTH, 25 * time.Millisecond, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			start := time.Now()
			handle, err := moduleSnapshotUntil(123, start.Add(tc.budget), func(uint32, uint32) (windows.Handle, error) {
				calls++
				return windows.InvalidHandle, tc.err
			})
			if handle != 0 || !errors.Is(err, tc.err) || !strings.Contains(err.Error(), "CreateToolhelp32Snapshot pid=123 attempts=") {
				t.Fatalf("handle=%v calls=%d err=%v", handle, calls, err)
			}
			if tc.wantCalls > 0 && calls != tc.wantCalls {
				t.Fatalf("calls=%d want=%d", calls, tc.wantCalls)
			}
			if time.Since(start) > time.Second {
				t.Fatal("snapshot retry exceeded bounded wait")
			}
		})
	}
}

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
