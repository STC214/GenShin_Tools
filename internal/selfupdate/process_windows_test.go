package selfupdate

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestWaitForProcessExitChecksIdentityAndWaits(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=TestUpdaterWaitChildProcess")
	command.Env = append(os.Environ(), "GENSHINTOOLS_UPDATER_WAIT_CHILD=1")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	}()
	identity, err := processIdentity(uint32(command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	ready := false
	if err := waitForProcessExit(context.Background(), identity, os.Args[0], 3*time.Second, func() error {
		ready = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("process waiter did not signal after validating the parent handle")
	}
}

func TestWaitForProcessExitRejectsWrongCreationTime(t *testing.T) {
	identity, err := CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	identity.CreationTime++
	if err := WaitForProcessExit(context.Background(), identity, os.Args[0], time.Second); err == nil {
		t.Fatal("wrong process creation time was accepted")
	}
}

func TestStopUnconfirmedProcessChecksIdentityAndTerminates(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=TestUpdaterStopChildProcess")
	command.Env = append(os.Environ(), "GENSHINTOOLS_UPDATER_STOP_CHILD=1")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	}()
	identity, err := processIdentity(uint32(command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	if err := stopUnconfirmedProcess(identity, os.Args[0], 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := stopUnconfirmedProcess(ProcessIdentity{PID: identity.PID, CreationTime: identity.CreationTime + 1}, os.Args[0], time.Second); err != nil {
		t.Fatalf("stale process identity was not ignored: %v", err)
	}
}

func TestUpdaterWaitChildProcess(t *testing.T) {
	if os.Getenv("GENSHINTOOLS_UPDATER_WAIT_CHILD") != "1" {
		return
	}
	time.Sleep(150 * time.Millisecond)
	os.Exit(0)
}

func TestUpdaterStopChildProcess(t *testing.T) {
	if os.Getenv("GENSHINTOOLS_UPDATER_STOP_CHILD") != "1" {
		return
	}
	time.Sleep(10 * time.Second)
}
