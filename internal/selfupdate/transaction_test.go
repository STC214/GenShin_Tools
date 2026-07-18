package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCommitStagedAndConfirm(t *testing.T) {
	layout, staged := prepareTransaction(t)
	old := writeInstalledFiles(t, layout.InstallRoot, staged.Manifest, "old-")

	if err := CommitStaged(context.Background(), layout, staged.Manifest.Version, staged.ManifestSHA256, nil); err != nil {
		t.Fatal(err)
	}
	journal, err := loadJournal(layout.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if journal.Phase != "committed" {
		t.Fatalf("transaction phase = %q, want committed", journal.Phase)
	}
	for _, file := range staged.Manifest.Files {
		if err := verifyPackageFile(filepath.Join(layout.InstallRoot, filepath.FromSlash(file.Path)), file); err != nil {
			t.Fatalf("installed %s: %v", file.Path, err)
		}
		if string(readFile(t, filepath.Join(layout.InstallRoot, filepath.FromSlash(file.Path)))) == string(old[file.Path]) {
			t.Fatalf("installed %s was not replaced", file.Path)
		}
	}
	if err := MarkTransactionRestarting(layout, staged.Manifest.Version, staged.ManifestSHA256); err != nil {
		t.Fatal(err)
	}
	if journal, err = loadJournal(layout.Journal); err != nil || journal.Phase != "restarting" {
		t.Fatalf("restart phase = %+v, %v", journal, err)
	}
	if err := ConfirmTransaction(layout, staged.Manifest.Version, staged.ManifestSHA256); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{layout.Journal, filepath.Join(layout.Backups, staged.Manifest.Version), staged.Directory} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("confirmed transaction path still exists: %s", path)
		}
	}
}

func TestCommitStagedRollsBackInjectedReplacementFailure(t *testing.T) {
	layout, staged := prepareTransaction(t)
	old := writeInstalledFiles(t, layout.InstallRoot, staged.Manifest, "rollback-")
	newFile := staged.Manifest.Files[0].Path
	if err := os.Remove(filepath.Join(layout.InstallRoot, filepath.FromSlash(newFile))); err != nil {
		t.Fatal(err)
	}
	delete(old, newFile)
	injected := errors.New("injected replacement failure")
	err := CommitStaged(context.Background(), layout, staged.Manifest.Version, staged.ManifestSHA256, &CommitHooks{
		BeforeReplace: func(index int, _ string) error {
			if index == 2 {
				return injected
			}
			return nil
		},
	})
	if !errors.Is(err, injected) {
		t.Fatalf("CommitStaged error = %v, want injected failure", err)
	}
	assertInstalledFiles(t, layout.InstallRoot, old)
	if _, err := os.Lstat(filepath.Join(layout.InstallRoot, filepath.FromSlash(newFile))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new file %s was not removed during rollback", newFile)
	}
	if _, err := os.Lstat(layout.Journal); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rolled-back transaction journal still exists")
	}
}

func TestRecoverTransactionRollsBackInterruptedCommit(t *testing.T) {
	layout, staged := prepareTransaction(t)
	old := writeInstalledFiles(t, layout.InstallRoot, staged.Manifest, "recover-")
	backupDirectory := filepath.Join(layout.Backups, staged.Manifest.Version)
	if err := os.Mkdir(backupDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	journal := transactionJournal{
		SchemaVersion: transactionSchemaVersion,
		Version:       staged.Manifest.Version, ManifestSHA256: staged.ManifestSHA256,
		Phase: "committing", Files: make([]transactionFile, len(staged.Manifest.Files)),
	}
	for index, file := range staged.Manifest.Files {
		target := filepath.Join(layout.InstallRoot, filepath.FromSlash(file.Path))
		backup := filepath.Join(backupDirectory, filepath.FromSlash(file.Path))
		size, digest, err := copyVerified(target, backup, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		journal.Files[index] = transactionFile{Path: file.Path, HadOriginal: true, OldSize: size, OldSHA256: digest}
	}
	first := staged.Manifest.Files[0]
	if err := replaceFromSource(
		filepath.Join(staged.Directory, filepath.FromSlash(first.Path)),
		filepath.Join(layout.InstallRoot, filepath.FromSlash(first.Path)),
		first.Size, first.SHA256,
	); err != nil {
		t.Fatal(err)
	}
	if err := saveJournal(layout.Journal, journal); err != nil {
		t.Fatal(err)
	}

	if err := RecoverTransaction(context.Background(), layout); err != nil {
		t.Fatal(err)
	}
	assertInstalledFiles(t, layout.InstallRoot, old)
}

func TestRecoverTransactionRejectsTamperedJournalPath(t *testing.T) {
	layout, staged := prepareTransaction(t)
	journal := transactionJournal{
		SchemaVersion: transactionSchemaVersion,
		Version:       staged.Manifest.Version, ManifestSHA256: staged.ManifestSHA256,
		Phase: "committing", Files: make([]transactionFile, len(staged.Manifest.Files)),
	}
	for index, file := range staged.Manifest.Files {
		journal.Files[index] = transactionFile{Path: file.Path}
	}
	journal.Files[0].Path = "../../outside.exe"
	data, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.Journal, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverTransaction(context.Background(), layout); err == nil {
		t.Fatal("tampered transaction journal was accepted")
	}
}

func prepareTransaction(t *testing.T) (UpdateLayout, StagedRelease) {
	t.Helper()
	installRoot := t.TempDir()
	layout, err := NewUpdateLayout(installRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(t.TempDir(), "release.zip")
	writeReleaseZIP(t, packagePath, nil, false, false)
	staged, err := StagePackage(context.Background(), packagePath, layout.Versions, "1.1.0", artifactForFile(t, packagePath))
	if err != nil {
		t.Fatal(err)
	}
	return layout, staged
}

func writeInstalledFiles(t *testing.T, root string, manifest PackageManifest, prefix string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte, len(manifest.Files))
	for _, file := range manifest.Files {
		data := []byte(prefix + file.Path)
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		files[file.Path] = data
	}
	return files
}

func assertInstalledFiles(t *testing.T, root string, want map[string][]byte) {
	t.Helper()
	for name, data := range want {
		if got := readFile(t, filepath.Join(root, filepath.FromSlash(name))); string(got) != string(data) {
			t.Fatalf("installed %s was not restored", name)
		}
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
