package launch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateShortcutUnicodePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Unicode 空格")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	shortcut := filepath.Join(root, "原神 启动.lnk")
	target, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	arguments := []string{"--name", "A B", `C:\日志 路径\x.txt`}
	if err := CreateShortcut(shortcut, target, root, arguments, target); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(shortcut)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < 100 {
		t.Fatalf("shortcut size = %d", info.Size())
	}
}
