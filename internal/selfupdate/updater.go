package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	UpdaterProtocolVersion = 1
	updaterRequestName     = "update-request.json"
	updaterReadyName       = "updater-ready"
	maxUpdaterRequestBytes = 16 << 10
)

type ProcessIdentity struct {
	PID          uint32 `json:"pid"`
	CreationTime int64  `json:"creationTime"`
}

type UpdaterRequest struct {
	ProtocolVersion int             `json:"protocolVersion"`
	Version         string          `json:"version"`
	ManifestSHA256  string          `json:"manifestSha256"`
	Parent          ProcessIdentity `json:"parent"`
	WaitTimeoutMS   int             `json:"waitTimeoutMs"`
	Restart         bool            `json:"restart"`
}

type UpdaterHooks struct {
	WaitForParent func(context.Context, ProcessIdentity, string, time.Duration) error
	RestartMain   func(string, string) error
	Commit        *CommitHooks
}

type UpdaterLaunch struct {
	HelperPath  string
	RequestPath string
	InstallRoot string
}

func PrepareUpdater(layout UpdateLayout, request UpdaterRequest) (UpdaterLaunch, error) {
	if err := layout.Ensure(); err != nil {
		return UpdaterLaunch{}, err
	}
	if err := ValidateUpdaterRequest(request); err != nil {
		return UpdaterLaunch{}, err
	}
	if _, err := os.Lstat(layout.Journal); err == nil {
		return UpdaterLaunch{}, errors.New("cannot prepare updater while a transaction is pending")
	} else if !errors.Is(err, os.ErrNotExist) {
		return UpdaterLaunch{}, err
	}
	source := filepath.Join(layout.InstallRoot, "GenshinTools-updater.exe")
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return UpdaterLaunch{}, errors.New("installed updater is not a regular non-empty file")
	}
	if err := rejectReparseWithin(layout.InstallRoot, source); err != nil {
		return UpdaterLaunch{}, err
	}
	digest, err := fileSHA256(source)
	if err != nil {
		return UpdaterLaunch{}, err
	}
	helper := filepath.Join(layout.Runner, "GenshinTools-updater.exe")
	if err := rejectReparseWithin(layout.InstallRoot, filepath.Dir(helper)); err != nil {
		return UpdaterLaunch{}, err
	}
	if err := replaceFromSource(source, helper, info.Size(), digest); err != nil {
		return UpdaterLaunch{}, fmt.Errorf("prepare updater runner: %w", err)
	}
	requestData, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return UpdaterLaunch{}, err
	}
	requestPath := filepath.Join(layout.Root, updaterRequestName)
	if err := atomicWriteJournal(requestPath, append(requestData, '\n')); err != nil {
		return UpdaterLaunch{}, fmt.Errorf("write updater request: %w", err)
	}
	return UpdaterLaunch{HelperPath: helper, RequestPath: requestPath, InstallRoot: layout.InstallRoot}, nil
}

func StartUpdater(launch UpdaterLaunch) error {
	layout, err := ResolveUpdaterScope(launch.HelperPath, launch.RequestPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Clean(layout.InstallRoot), filepath.Clean(launch.InstallRoot)) {
		return errors.New("updater launch install root does not match runner scope")
	}
	request, err := LoadUpdaterRequest(launch.RequestPath)
	if err != nil {
		return err
	}
	readyPath := filepath.Join(layout.Root, updaterReadyName)
	if info, err := os.Lstat(readyPath); err == nil {
		if !info.Mode().IsRegular() || rejectReparseWithin(layout.InstallRoot, readyPath) != nil {
			return errors.New("updater ready path is not a regular portable file")
		}
		if err := os.Remove(readyPath); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	command := exec.Command(launch.HelperPath, "--request", launch.RequestPath)
	command.Dir = layout.InstallRoot
	if err := command.Start(); err != nil {
		return err
	}
	wantReady := updaterReadyToken(request)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		value, err := readUpdaterReady(layout, readyPath)
		if err == nil && value == wantReady {
			_ = os.Remove(readyPath)
			return command.Process.Release()
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = command.Process.Kill()
	_, _ = command.Process.Wait()
	return errors.New("updater did not validate the parent process within 5 seconds")
}

func readUpdaterReady(layout UpdateLayout, readyPath string) (string, error) {
	info, err := os.Lstat(readyPath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 256 {
		return "", errors.New("updater ready file is invalid")
	}
	if err := rejectReparseWithin(layout.InstallRoot, readyPath); err != nil {
		return "", err
	}
	input, err := os.Open(readyPath)
	if err != nil {
		return "", err
	}
	defer input.Close()
	data, err := io.ReadAll(io.LimitReader(input, 257))
	if err != nil || len(data) > 256 {
		return "", errors.New("updater ready file changed while reading")
	}
	return string(data), nil
}

func ResolveUpdaterScope(helperPath, requestPath string) (UpdateLayout, error) {
	helper, err := filepath.Abs(helperPath)
	if err != nil {
		return UpdateLayout{}, err
	}
	if !strings.EqualFold(filepath.Base(helper), "GenshinTools-updater.exe") || !strings.EqualFold(filepath.Base(filepath.Dir(helper)), "runner") {
		return UpdateLayout{}, errors.New("updater must run from the fixed portable runner path")
	}
	updatesDirectory := filepath.Dir(filepath.Dir(helper))
	if !strings.EqualFold(filepath.Base(updatesDirectory), "updates") || !strings.EqualFold(filepath.Base(filepath.Dir(updatesDirectory)), "data") {
		return UpdateLayout{}, errors.New("updater runner is outside the portable update layout")
	}
	installRoot := filepath.Dir(filepath.Dir(updatesDirectory))
	layout, err := NewUpdateLayout(installRoot)
	if err != nil {
		return UpdateLayout{}, err
	}
	request, err := filepath.Abs(requestPath)
	if err != nil {
		return UpdateLayout{}, err
	}
	wantRequest := filepath.Join(layout.Root, updaterRequestName)
	if !strings.EqualFold(filepath.Clean(request), filepath.Clean(wantRequest)) {
		return UpdateLayout{}, errors.New("updater request is outside the fixed portable update path")
	}
	if err := validateUpdateLayout(layout); err != nil {
		return UpdateLayout{}, err
	}
	for _, path := range []string{helper, request} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return UpdateLayout{}, errors.New("updater executable or request is not a regular file")
		}
		if err := rejectReparseWithin(layout.InstallRoot, path); err != nil {
			return UpdateLayout{}, err
		}
	}
	return layout, nil
}

func LoadUpdaterRequest(path string) (UpdaterRequest, error) {
	input, err := os.Open(path)
	if err != nil {
		return UpdaterRequest{}, err
	}
	defer input.Close()
	data, err := io.ReadAll(io.LimitReader(input, maxUpdaterRequestBytes+1))
	if err != nil {
		return UpdaterRequest{}, err
	}
	if len(data) > maxUpdaterRequestBytes {
		return UpdaterRequest{}, errors.New("updater request exceeds 16 KiB")
	}
	var request UpdaterRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return UpdaterRequest{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return UpdaterRequest{}, errors.New("updater request contains trailing JSON")
	}
	if err := ValidateUpdaterRequest(request); err != nil {
		return UpdaterRequest{}, err
	}
	return request, nil
}

func ValidateUpdaterRequest(request UpdaterRequest) error {
	if request.ProtocolVersion != UpdaterProtocolVersion {
		return errors.New("unsupported updater protocol version")
	}
	if _, ok := parseVersion(request.Version); !ok || !shaPattern.MatchString(request.ManifestSHA256) {
		return errors.New("updater version or manifest hash is invalid")
	}
	if request.Parent.PID == 0 || request.Parent.CreationTime <= 0 {
		return errors.New("updater parent process identity is invalid")
	}
	if request.WaitTimeoutMS < 1_000 || request.WaitTimeoutMS > 300_000 {
		return errors.New("updater wait timeout must be within 1000..300000 ms")
	}
	return nil
}

func ExecuteUpdate(ctx context.Context, layout UpdateLayout, request UpdaterRequest, hooks *UpdaterHooks) error {
	if err := validateUpdateLayout(layout); err != nil {
		return err
	}
	if err := ValidateUpdaterRequest(request); err != nil {
		return err
	}
	wait := func(ctx context.Context, identity ProcessIdentity, expectedPath string, timeout time.Duration) error {
		readyPath := filepath.Join(layout.Root, updaterReadyName)
		return waitForProcessExit(ctx, identity, expectedPath, timeout, func() error {
			return atomicWriteJournal(readyPath, []byte(updaterReadyToken(request)))
		})
	}
	restart := RestartMain
	var commitHooks *CommitHooks
	if hooks != nil {
		if hooks.WaitForParent != nil {
			wait = hooks.WaitForParent
		}
		if hooks.RestartMain != nil {
			restart = hooks.RestartMain
		}
		commitHooks = hooks.Commit
	}
	mainPath := filepath.Join(layout.InstallRoot, "GenshinTools.exe")
	if err := wait(ctx, request.Parent, mainPath, time.Duration(request.WaitTimeoutMS)*time.Millisecond); err != nil {
		return fmt.Errorf("wait for launcher exit: %w", err)
	}
	if err := RecoverTransaction(ctx, layout); err != nil {
		return fmt.Errorf("recover previous update transaction: %w", err)
	}
	if err := CommitStaged(ctx, layout, request.Version, request.ManifestSHA256, commitHooks); err != nil {
		return fmt.Errorf("commit prepared update: %w", err)
	}
	if !request.Restart {
		return nil
	}
	if err := restart(mainPath, layout.InstallRoot); err == nil {
		return nil
	} else {
		restartErr := err
		if rollbackErr := RollbackCommitted(context.Background(), layout, request.Version, request.ManifestSHA256); rollbackErr != nil {
			return errors.Join(fmt.Errorf("restart updated launcher: %w", restartErr), fmt.Errorf("rollback committed update: %w", rollbackErr))
		}
		if oldRestartErr := restart(mainPath, layout.InstallRoot); oldRestartErr != nil {
			return errors.Join(fmt.Errorf("restart updated launcher: %w", restartErr), fmt.Errorf("restart restored launcher: %w", oldRestartErr))
		}
		return fmt.Errorf("updated launcher could not start; restored and restarted previous version: %w", restartErr)
	}
}

func updaterReadyToken(request UpdaterRequest) string {
	return fmt.Sprintf("%d:%d:%s", request.Parent.PID, request.Parent.CreationTime, request.ManifestSHA256)
}
