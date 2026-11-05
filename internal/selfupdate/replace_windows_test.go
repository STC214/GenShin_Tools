package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenameNewDirectoryNeverOverwritesStagedRelease(t *testing.T) {
	root := t.TempDir()
	source, destination := filepath.Join(root, "candidate"), filepath.Join(root, "1.7.0")
	for _, path := range []string{source, destination} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(destination, "keep.txt")
	if err := os.WriteFile(marker, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := renameNewDirectory(source, destination); err == nil {
		t.Fatal("existing staged release was overwritten")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "original" {
		t.Fatalf("existing staged release changed: data=%q err=%v", data, err)
	}
	if err := renameNewDirectory(source, filepath.Join(root, "new-version")); err != nil {
		t.Fatal(err)
	}
}
