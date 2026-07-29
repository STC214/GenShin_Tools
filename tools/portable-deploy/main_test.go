package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"genshintools/internal/platform/win32"
)

func TestMirrorReleasePreservesDataAndRemovesRetiredFiles(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "staged")
	target := filepath.Join(root, "installed")
	mustWrite(t, filepath.Join(source, "GenshinTools.exe"), "new")
	mustWrite(t, filepath.Join(source, "LICENSES", "current.txt"), "current")
	mustWrite(t, filepath.Join(target, "GenshinTools.exe"), "old")
	mustWrite(t, filepath.Join(target, "retired.exe"), "retired")
	mustWrite(t, filepath.Join(target, "LICENSES", "retired.txt"), "retired")
	mustWrite(t, filepath.Join(target, "data", "config.json"), "user")

	if err := mirrorRelease(source, target); err != nil {
		t.Fatal(err)
	}
	assertContent(t, filepath.Join(target, "GenshinTools.exe"), "new")
	assertContent(t, filepath.Join(target, "LICENSES", "current.txt"), "current")
	assertContent(t, filepath.Join(target, "data", "config.json"), "user")
	for _, path := range []string{
		filepath.Join(target, "retired.exe"),
		filepath.Join(target, "LICENSES", "retired.txt"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("retired path still exists: %s", path)
		}
	}
}

func TestMirrorReleaseRejectsPackagedData(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "staged")
	target := filepath.Join(root, "installed")
	mustWrite(t, filepath.Join(source, "data", "config.json"), "packaged")
	mustWrite(t, filepath.Join(target, "data", "config.json"), "user")
	if err := mirrorRelease(source, target); err == nil {
		t.Fatal("expected packaged data rejection")
	}
	assertContent(t, filepath.Join(target, "data", "config.json"), "user")
}

func TestMirrorReleaseRollsBackPartialInstall(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "staged")
	target := filepath.Join(root, "installed")
	mustWrite(t, filepath.Join(source, "a.exe"), "new-a")
	mustWrite(t, filepath.Join(source, "b.exe"), "new-b")
	mustWrite(t, filepath.Join(target, "a.exe"), "old-a")
	mustWrite(t, filepath.Join(target, "retired.exe"), "old-retired")
	mustWrite(t, filepath.Join(target, "data", "config.json"), "user")

	originalMove := movePath
	t.Cleanup(func() { movePath = originalMove })
	movePath = func(oldPath, newPath string) error {
		if filepath.Base(oldPath) == "b.exe" && filepath.Dir(oldPath) == source {
			return errors.New("injected install failure")
		}
		return originalMove(oldPath, newPath)
	}
	if err := mirrorRelease(source, target); err == nil {
		t.Fatal("expected injected install failure")
	}
	assertContent(t, filepath.Join(target, "a.exe"), "old-a")
	assertContent(t, filepath.Join(target, "retired.exe"), "old-retired")
	assertContent(t, filepath.Join(target, "data", "config.json"), "user")
}

func TestDeploymentMutexRejectsConcurrentOwnerAndReleases(t *testing.T) {
	target := filepath.Join(t.TempDir(), "installed")
	first, err := acquireDeploymentMutex(target)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := acquireDeploymentMutex(target); err == nil {
		win32.CloseHandle(second)
		t.Fatal("concurrent deployment mutex was accepted")
	}
	win32.CloseHandle(first)
	third, err := acquireDeploymentMutex(target)
	if err != nil {
		t.Fatalf("deployment mutex remained owned after close: %v", err)
	}
	win32.CloseHandle(third)
}

func TestRecoverInterruptedInstallIsIdempotentAfterRestoreFailure(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "installed")
	source := filepath.Join(root, ".genshintools-deploy-fixture", "1.5.3")
	backup := filepath.Join(root, ".genshintools-backup-fixture")
	mustWrite(t, filepath.Join(target, "new.exe"), "new")
	mustWrite(t, filepath.Join(target, "data", "config.json"), "user")
	mustWrite(t, filepath.Join(backup, "old.exe"), "old")
	mustWrite(t, filepath.Join(backup, "retired.exe"), "retired")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	journal := deployJournal{
		SchemaVersion: deployJournalSchema,
		Target:        target,
		Source:        source,
		Backup:        backup,
		Phase:         "installing",
		Incoming:      []string{"new.exe"},
	}
	if err := createDeploymentJournal(deploymentJournalPath(target), journal); err != nil {
		t.Fatal(err)
	}

	originalMove := movePath
	t.Cleanup(func() { movePath = originalMove })
	failed := false
	movePath = func(oldPath, newPath string) error {
		if !failed && filepath.Base(oldPath) == "retired.exe" && filepath.Dir(oldPath) == backup {
			failed = true
			return errors.New("injected restore failure")
		}
		return originalMove(oldPath, newPath)
	}
	if err := recoverDeployment(target); err == nil {
		t.Fatal("expected injected recovery failure")
	}
	movePath = originalMove

	if err := recoverDeployment(target); err != nil {
		t.Fatal(err)
	}
	assertContent(t, filepath.Join(target, "old.exe"), "old")
	assertContent(t, filepath.Join(target, "retired.exe"), "retired")
	assertContent(t, filepath.Join(target, "data", "config.json"), "user")
	if _, err := os.Stat(filepath.Join(target, "new.exe")); !os.IsNotExist(err) {
		t.Fatalf("partially installed file survived recovery: %v", err)
	}
	if _, err := os.Stat(deploymentJournalPath(target)); !os.IsNotExist(err) {
		t.Fatalf("completed recovery journal survived: %v", err)
	}
}

func TestRecoverCommittedDeploymentOnlyCleansBackup(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "installed")
	source := filepath.Join(root, ".genshintools-deploy-fixture", "1.5.3")
	backup := filepath.Join(root, ".genshintools-backup-fixture")
	mustWrite(t, filepath.Join(target, "current.exe"), "current")
	mustWrite(t, filepath.Join(target, "data", "config.json"), "user")
	mustWrite(t, filepath.Join(backup, "old.exe"), "old")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	journal := deployJournal{
		SchemaVersion: deployJournalSchema,
		Target:        target,
		Source:        source,
		Backup:        backup,
		Phase:         "committed",
		Incoming:      []string{"current.exe"},
	}
	if err := createDeploymentJournal(deploymentJournalPath(target), journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverDeployment(target); err != nil {
		t.Fatal(err)
	}
	assertContent(t, filepath.Join(target, "current.exe"), "current")
	assertContent(t, filepath.Join(target, "data", "config.json"), "user")
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("committed backup survived recovery: %v", err)
	}
}

func TestRecoverCommittedDeploymentRetainsBackupWhenTargetIsMissing(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "installed")
	source := filepath.Join(root, ".genshintools-deploy-fixture", "1.5.4")
	backup := filepath.Join(root, ".genshintools-backup-fixture")
	mustWrite(t, filepath.Join(backup, "old.exe"), "old")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	journal := deployJournal{
		SchemaVersion: deployJournalSchema,
		Target:        target,
		Source:        source,
		Backup:        backup,
		Phase:         "committed",
		Incoming:      []string{"current.exe"},
	}
	journalPath := deploymentJournalPath(target)
	if err := createDeploymentJournal(journalPath, journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverDeployment(target); err == nil {
		t.Fatal("missing committed target was accepted")
	}
	assertContent(t, filepath.Join(backup, "old.exe"), "old")
	if _, err := os.Stat(journalPath); err != nil {
		t.Fatalf("recovery evidence was removed after unsafe cleanup refusal: %v", err)
	}
}

func TestRecoverCommittedDeploymentRetainsBackupOnTargetMismatch(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "installed")
	source := filepath.Join(root, ".genshintools-deploy-fixture", "1.5.4")
	backup := filepath.Join(root, ".genshintools-backup-fixture")
	mustWrite(t, filepath.Join(target, "current.exe"), "current")
	mustWrite(t, filepath.Join(target, "unexpected.exe"), "unexpected")
	mustWrite(t, filepath.Join(backup, "old.exe"), "old")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	journal := deployJournal{
		SchemaVersion: deployJournalSchema,
		Target:        target,
		Source:        source,
		Backup:        backup,
		Phase:         "committed",
		Incoming:      []string{"current.exe"},
	}
	if err := createDeploymentJournal(deploymentJournalPath(target), journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverDeployment(target); err == nil {
		t.Fatal("mismatched committed target was accepted")
	}
	assertContent(t, filepath.Join(backup, "old.exe"), "old")
}

func TestRecoverLegacyCommittedJournal(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "installed")
	source := filepath.Join(root, ".genshintools-deploy-fixture", "1.5.3")
	backup := filepath.Join(root, ".genshintools-backup-fixture")
	mustWrite(t, filepath.Join(target, "GenshinTools.exe"), "current")
	mustWrite(t, filepath.Join(backup, "GenshinTools.exe"), "old")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	journal := deployJournal{
		SchemaVersion: 1,
		Target:        target,
		Source:        source,
		Backup:        backup,
		Phase:         "committed",
	}
	if err := createDeploymentJournal(deploymentJournalPath(target), journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverDeployment(target); err != nil {
		t.Fatal(err)
	}
	assertContent(t, filepath.Join(target, "GenshinTools.exe"), "current")
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("legacy committed backup survived recovery: %v", err)
	}
}

func mustWrite(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}
