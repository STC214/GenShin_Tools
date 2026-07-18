package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func CurrentProcessIdentity() (ProcessIdentity, error) {
	return processIdentity(uint32(os.Getpid()))
}

func processIdentity(pid uint32) (ProcessIdentity, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ProcessIdentity{}, err
	}
	defer windows.CloseHandle(process)
	creationTime, err := queryProcessCreationTime(process)
	if err != nil {
		return ProcessIdentity{}, err
	}
	return ProcessIdentity{PID: pid, CreationTime: creationTime}, nil
}

func WaitForProcessExit(ctx context.Context, identity ProcessIdentity, expectedPath string, timeout time.Duration) error {
	return waitForProcessExit(ctx, identity, expectedPath, timeout, nil)
}

func waitForProcessExit(ctx context.Context, identity ProcessIdentity, expectedPath string, timeout time.Duration, ready func() error) error {
	if identity.PID == 0 || identity.CreationTime <= 0 || timeout < time.Second || timeout > 5*time.Minute {
		return errors.New("invalid process identity or wait timeout")
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, identity.PID)
	if err != nil {
		return fmt.Errorf("open parent process: %w", err)
	}
	defer windows.CloseHandle(process)
	creationTime, err := queryProcessCreationTime(process)
	if err != nil || creationTime != identity.CreationTime {
		return errors.New("parent PID creation time does not match")
	}
	actualPath, err := queryProcessPath(process)
	if err != nil {
		return fmt.Errorf("query parent process path: %w", err)
	}
	wantPath, err := filepath.Abs(expectedPath)
	if err != nil || !strings.EqualFold(filepath.Clean(actualPath), filepath.Clean(wantPath)) {
		return fmt.Errorf("parent process path is %q, want fixed launcher path", actualPath)
	}
	if ready != nil {
		if err := ready(); err != nil {
			return fmt.Errorf("signal updater readiness: %w", err)
		}
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errors.New("timed out waiting for parent process exit")
		}
		wait := 100 * time.Millisecond
		if remaining < wait {
			wait = remaining
		}
		status, err := windows.WaitForSingleObject(process, uint32(max(wait.Milliseconds(), 1)))
		if err != nil {
			return err
		}
		switch status {
		case windows.WAIT_OBJECT_0:
			return nil
		case uint32(windows.WAIT_TIMEOUT):
			continue
		default:
			return fmt.Errorf("unexpected parent wait status 0x%08X", status)
		}
	}
}

func RestartMain(mainPath, installRoot string) error {
	main, err := filepath.Abs(mainPath)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(installRoot)
	if err != nil {
		return err
	}
	if !strings.EqualFold(main, filepath.Join(root, "GenshinTools.exe")) {
		return errors.New("refusing to restart a non-launcher executable")
	}
	info, err := os.Lstat(main)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("launcher executable is not a regular file")
	}
	if err := rejectReparseWithin(root, main); err != nil {
		return err
	}
	command := exec.Command(main)
	command.Dir = root
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func queryProcessCreationTime(process windows.Handle) (int64, error) {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &creation, &exit, &kernel, &user); err != nil {
		return 0, err
	}
	return creation.Nanoseconds(), nil
}

func queryProcessPath(process windows.Handle) (string, error) {
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:size]), nil
}
