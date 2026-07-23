package injection

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
	"unsafe"
)

const directHelperProbeEnvironment = "GENSHINTOOLS_DIRECT_HELPER_PROBE"

func TestMain(m *testing.M) {
	if os.Getenv(directHelperProbeEnvironment) == "1" {
		if len(os.Args) != 3 || os.Args[1] != "--request" {
			os.Exit(2)
		}
		value := strconv.FormatBool(currentProcessElevated())
		if err := os.WriteFile(os.Args[2], []byte(value), 0o600); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestShellExecuteInfoABIAMD64(t *testing.T) {
	if size := unsafe.Sizeof(shellExecuteInfo{}); size != 112 {
		t.Fatalf("SHELLEXECUTEINFOW size = %d, want 112", size)
	}
	if offset := unsafe.Offsetof(shellExecuteInfo{}.Process); offset != 104 {
		t.Fatalf("SHELLEXECUTEINFOW hProcess offset = %d, want 104", offset)
	}
}

func TestElevatedMainProcessRunsHelperDirectly(t *testing.T) {
	tests := []struct {
		requested       bool
		alreadyElevated bool
		wantRunAs       bool
	}{
		{requested: true, alreadyElevated: false, wantRunAs: true},
		{requested: true, alreadyElevated: true, wantRunAs: false},
		{requested: false, alreadyElevated: false, wantRunAs: false},
		{requested: false, alreadyElevated: true, wantRunAs: false},
	}
	for _, test := range tests {
		if got := shouldUseRunAs(test.requested, test.alreadyElevated); got != test.wantRunAs {
			t.Errorf("shouldUseRunAs(%t, %t) = %t, want %t", test.requested, test.alreadyElevated, got, test.wantRunAs)
		}
	}
}

func TestDirectHelperProcessInheritsParentElevationState(t *testing.T) {
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(t.TempDir(), "GenshinTools-injector.exe")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, data, 0o700); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(t.TempDir(), "helper-result.txt")
	t.Setenv(directHelperProbeEnvironment, "1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var output limitedOutput
	if err := runDirectHelper(ctx, helper, result, &output); err != nil {
		t.Fatalf("run direct helper: %v; output=%s", err, output.String())
	}
	got, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	want := strconv.FormatBool(currentProcessElevated())
	if string(got) != want {
		t.Fatalf("child elevation state = %q, want parent state %q", got, want)
	}
}
