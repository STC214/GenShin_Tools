package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveUpdaterScopeAcceptsOnlyFixedRunnerAndRequest(t *testing.T) {
	root := t.TempDir()
	helper := filepath.Join(root, "data", "updates", "runner", "GenshinTools-updater.exe")
	request := filepath.Join(root, "data", "updates", updaterRequestName)
	for _, path := range []string{helper, request} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	layout, err := ResolveUpdaterScope(helper, request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(layout.InstallRoot, root) {
		t.Fatalf("install root = %q, want %q", layout.InstallRoot, root)
	}
	if _, err := ResolveUpdaterScope(filepath.Join(root, "GenshinTools-updater.exe"), request); err == nil {
		t.Fatal("updater outside runner directory was accepted")
	}
	outside := filepath.Join(t.TempDir(), updaterRequestName)
	if err := os.WriteFile(outside, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveUpdaterScope(helper, outside); err == nil {
		t.Fatal("request outside fixed update path was accepted")
	}
}

func TestLoadUpdaterRequestIsStrict(t *testing.T) {
	valid := validUpdaterRequest("1.1.0", strings.Repeat("a", 64))
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUpdaterRequest(path); err != nil {
		t.Fatal(err)
	}
	for name, invalid := range map[string][]byte{
		"unknown field":  append(data[:len(data)-1], []byte(`,"unexpected":true}`)...),
		"trailing value": append(append([]byte(nil), data...), []byte(` {}`)...),
		"too large":      make([]byte, maxUpdaterRequestBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, invalid, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadUpdaterRequest(path); err == nil {
				t.Fatal("invalid updater request was accepted")
			}
		})
	}
}

func TestPrepareUpdaterCopiesRunnerAndWritesFixedRequest(t *testing.T) {
	root := t.TempDir()
	layout, err := NewUpdateLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "GenshinTools-updater.exe")
	sourceData := []byte("trusted-current-updater")
	if err := os.WriteFile(source, sourceData, 0o644); err != nil {
		t.Fatal(err)
	}
	request := validUpdaterRequest("1.2.3", strings.Repeat("b", 64))
	launch, err := PrepareUpdater(layout, request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(launch.HelperPath, filepath.Join(layout.Runner, "GenshinTools-updater.exe")) || !strings.EqualFold(launch.RequestPath, filepath.Join(layout.Root, updaterRequestName)) {
		t.Fatalf("unexpected updater launch: %+v", launch)
	}
	if got := readFile(t, launch.HelperPath); string(got) != string(sourceData) {
		t.Fatal("prepared updater runner differs from installed updater")
	}
	got, err := LoadUpdaterRequest(launch.RequestPath)
	if err != nil || got != request {
		t.Fatalf("prepared updater request = %+v, %v", got, err)
	}
	if _, err := ResolveUpdaterScope(launch.HelperPath, launch.RequestPath); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteUpdateCommitsAndRestartsFixedMain(t *testing.T) {
	layout, staged := prepareTransaction(t)
	writeInstalledFiles(t, layout.InstallRoot, staged.Manifest, "old-")
	request := validUpdaterRequest(staged.Manifest.Version, staged.ManifestSHA256)
	waited := false
	restarted := false
	err := ExecuteUpdate(context.Background(), layout, request, &UpdaterHooks{
		WaitForParent: func(_ context.Context, got ProcessIdentity, path string, timeout time.Duration) error {
			waited = got == request.Parent && strings.EqualFold(path, filepath.Join(layout.InstallRoot, "GenshinTools.exe")) && timeout == 10*time.Second
			return nil
		},
		RestartMain: func(path, root string) (ProcessIdentity, error) {
			restarted = strings.EqualFold(path, filepath.Join(layout.InstallRoot, "GenshinTools.exe")) && strings.EqualFold(root, layout.InstallRoot)
			return ProcessIdentity{}, nil
		},
		WaitForConfirmation: func(context.Context, UpdateLayout, string, string, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !waited || !restarted {
		t.Fatal("updater did not wait for and restart the fixed launcher")
	}
	journal, err := loadJournal(layout.Journal)
	if err != nil || journal.Phase != "restarting" {
		t.Fatalf("restarting journal = %+v, %v", journal, err)
	}
}

func TestExecuteUpdateRestoresOldVersionWhenConfirmationTimesOut(t *testing.T) {
	layout, staged := prepareTransaction(t)
	old := writeInstalledFiles(t, layout.InstallRoot, staged.Manifest, "unconfirmed-")
	request := validUpdaterRequest(staged.Manifest.Version, staged.ManifestSHA256)
	restarts := 0
	confirmed := false
	err := ExecuteUpdate(context.Background(), layout, request, &UpdaterHooks{
		WaitForParent: func(context.Context, ProcessIdentity, string, time.Duration) error { return nil },
		RestartMain: func(string, string) (ProcessIdentity, error) {
			restarts++
			return ProcessIdentity{}, nil
		},
		WaitForConfirmation: func(_ context.Context, got UpdateLayout, version, digest string, timeout time.Duration) error {
			confirmed = got == layout && version == request.Version && digest == request.ManifestSHA256 && timeout == 5*time.Second
			return errors.New("injected confirmation timeout")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "restored previous version") {
		t.Fatalf("ExecuteUpdate error = %v", err)
	}
	if !confirmed || restarts != 2 {
		t.Fatalf("confirmed=%v restart attempts=%d, want true and 2", confirmed, restarts)
	}
	assertInstalledFiles(t, layout.InstallRoot, old)
	if _, err := os.Lstat(layout.Journal); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("confirmation rollback left the transaction journal behind")
	}
}

func TestExecuteUpdateRestoresOldVersionWhenRestartFails(t *testing.T) {
	layout, staged := prepareTransaction(t)
	old := writeInstalledFiles(t, layout.InstallRoot, staged.Manifest, "restore-")
	request := validUpdaterRequest(staged.Manifest.Version, staged.ManifestSHA256)
	restarts := 0
	err := ExecuteUpdate(context.Background(), layout, request, &UpdaterHooks{
		WaitForParent: func(context.Context, ProcessIdentity, string, time.Duration) error { return nil },
		RestartMain: func(string, string) (ProcessIdentity, error) {
			restarts++
			if restarts == 1 {
				return ProcessIdentity{}, errors.New("new launcher failed to start")
			}
			return ProcessIdentity{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "restored and restarted previous version") {
		t.Fatalf("ExecuteUpdate error = %v", err)
	}
	if restarts != 2 {
		t.Fatalf("restart attempts = %d, want 2", restarts)
	}
	assertInstalledFiles(t, layout.InstallRoot, old)
	if _, err := os.Lstat(layout.Journal); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rollback left the transaction journal behind")
	}
}

func validUpdaterRequest(version, manifestSHA256 string) UpdaterRequest {
	return UpdaterRequest{
		ProtocolVersion: UpdaterProtocolVersion,
		Version:         version, ManifestSHA256: manifestSHA256,
		Parent:        ProcessIdentity{PID: 1234, CreationTime: 5678},
		WaitTimeoutMS: 10_000, ConfirmationTimeoutMS: 5_000, Restart: true,
	}
}
