package injection

import (
	"testing"
	"unsafe"
)

func TestShellExecuteInfoABIAMD64(t *testing.T) {
	if size := unsafe.Sizeof(shellExecuteInfo{}); size != 112 {
		t.Fatalf("SHELLEXECUTEINFOW size = %d, want 112", size)
	}
	if offset := unsafe.Offsetof(shellExecuteInfo{}.Process); offset != 104 {
		t.Fatalf("SHELLEXECUTEINFOW hProcess offset = %d, want 104", offset)
	}
}
