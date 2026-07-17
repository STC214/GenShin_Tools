package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestForExecutable(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "portable", "GenshinTools.exe")

	layout, err := ForExecutable(executable)
	if err != nil {
		t.Fatalf("ForExecutable: %v", err)
	}
	if layout.Root != filepath.Dir(executable) {
		t.Fatalf("Root = %q, want %q", layout.Root, filepath.Dir(executable))
	}
	if layout.Config != filepath.Join(filepath.Dir(executable), "data", "config.json") {
		t.Fatalf("Config = %q", layout.Config)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, directory := range layout.Directories() {
		info, err := os.Stat(directory)
		if err != nil {
			t.Fatalf("stat %q: %v", directory, err)
		}
		if !info.IsDir() {
			t.Fatalf("%q is not a directory", directory)
		}
	}
	if _, err := os.Stat(layout.Config); !os.IsNotExist(err) {
		t.Fatalf("Ensure unexpectedly created config file; stat error=%v", err)
	}
}

func TestForExecutableRejectsEmptyPath(t *testing.T) {
	if _, err := ForExecutable(""); err == nil {
		t.Fatal("ForExecutable accepted an empty path")
	}
}
