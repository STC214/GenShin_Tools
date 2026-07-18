package selfupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoverAtStartupConfirmsMatchingCommittedVersion(t *testing.T) {
	layout, staged := prepareTransaction(t)
	writeInstalledFiles(t, layout.InstallRoot, staged.Manifest, "startup-")
	if err := CommitStaged(context.Background(), layout, staged.Manifest.Version, staged.ManifestSHA256, nil); err != nil {
		t.Fatal(err)
	}
	status, err := RecoverAtStartup(layout.InstallRoot, staged.Manifest.Version)
	if err != nil || status != StartupUpdateConfirmed {
		t.Fatalf("startup status=%q err=%v", status, err)
	}
	if _, err := os.Lstat(layout.Journal); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("startup confirmation left journal behind")
	}
}

func TestRecoverAtStartupLeavesCommittedOtherVersionPending(t *testing.T) {
	layout, staged := prepareTransaction(t)
	writeInstalledFiles(t, layout.InstallRoot, staged.Manifest, "startup-")
	if err := CommitStaged(context.Background(), layout, staged.Manifest.Version, staged.ManifestSHA256, nil); err != nil {
		t.Fatal(err)
	}
	status, err := RecoverAtStartup(layout.InstallRoot, "9.9.9")
	if err != nil || status != StartupUpdatePending {
		t.Fatalf("startup status=%q err=%v", status, err)
	}
	if _, err := os.Stat(filepath.Join(layout.Versions, staged.Manifest.Version, "release.json")); err != nil {
		t.Fatal("pending staged version was removed")
	}
}
