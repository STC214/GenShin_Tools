package injection

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestReadOnlyLockBlocksAuditedFileMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "module.dll")
	if err := os.WriteFile(path, []byte("audited"), 0o644); err != nil {
		t.Fatal(err)
	}
	handle, err := lockFileReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if err := os.WriteFile(path, []byte("changed"), 0o644); err == nil {
		t.Fatal("locked audited file was writable")
	}
	if err := os.Rename(path, path+".replaced"); err == nil {
		t.Fatal("locked audited file was replaceable")
	}
}
