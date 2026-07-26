package input

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestSupportedCompatibilityToolIsExact(t *testing.T) {
	for _, name := range []string{"AHK_F.exe", "ahk_f.EXE", "quickinput.exe"} {
		if !supportedCompatibilityTool(name) {
			t.Fatalf("expected %q to be supported", name)
		}
	}
	for _, name := range []string{"AutoHotkey.exe", "AHK_Space.exe", "quickinput-helper.exe", "GenshinTools.exe", ""} {
		if supportedCompatibilityTool(name) {
			t.Fatalf("unexpected broad compatibility match for %q", name)
		}
	}
}

func TestCompatibilityShellExecuteInfoABIAMD64(t *testing.T) {
	if size := unsafe.Sizeof(shellExecuteInfo{}); size != 112 {
		t.Fatalf("SHELLEXECUTEINFOW size = %d, want 112", size)
	}
	if offset := unsafe.Offsetof(shellExecuteInfo{}.Process); offset != 104 {
		t.Fatalf("SHELLEXECUTEINFOW hProcess offset = %d, want 104", offset)
	}
}

func TestRequiresElevatedCompatibilityStartUnwrapsError(t *testing.T) {
	if !requiresElevatedCompatibilityStart(fmt.Errorf("launch: %w", windows.ERROR_ELEVATION_REQUIRED)) {
		t.Fatal("wrapped elevation-required error was not recognized")
	}
	if requiresElevatedCompatibilityStart(errors.New("ordinary start failure")) {
		t.Fatal("ordinary start failure requested elevation")
	}
}

func TestAHKRestartPreservesOriginalUntilReplacementIsHealthy(t *testing.T) {
	old := ExternalCompatibilityTool{PID: 42, Path: `D:\tools\AHK_F.exe`, CreationTime: 100}
	startFailure := errors.New("UAC cancelled")
	result := ExternalToolRestartResult{Path: old.Path}
	stops := 0
	restartAHKCompatibilityToolWith(
		[]ExternalCompatibilityTool{old},
		&result,
		func(string) (uint32, error) { return 0, startFailure },
		func(string, uint32, time.Duration) error {
			t.Fatal("unstarted replacement was verified")
			return nil
		},
		func(ExternalCompatibilityTool) error {
			stops++
			return nil
		},
	)
	if !errors.Is(result.Error, startFailure) {
		t.Fatalf("restart error = %v, want %v", result.Error, startFailure)
	}
	if stops != 0 {
		t.Fatalf("original AHK was stopped %d time(s) after replacement start failure", stops)
	}

	result = ExternalToolRestartResult{Path: old.Path}
	verifyFailure := errors.New("replacement exited")
	restartAHKCompatibilityToolWith(
		[]ExternalCompatibilityTool{old},
		&result,
		func(string) (uint32, error) { return 84, nil },
		func(string, uint32, time.Duration) error { return verifyFailure },
		func(ExternalCompatibilityTool) error {
			stops++
			return nil
		},
	)
	if !errors.Is(result.Error, verifyFailure) {
		t.Fatalf("verification error = %v, want %v", result.Error, verifyFailure)
	}
	if stops != 0 {
		t.Fatalf("original AHK was stopped %d time(s) before replacement became healthy", stops)
	}

	result = ExternalToolRestartResult{Path: old.Path}
	restartAHKCompatibilityToolWith(
		[]ExternalCompatibilityTool{old},
		&result,
		func(string) (uint32, error) { return 84, nil },
		func(string, uint32, time.Duration) error { return nil },
		func(tool ExternalCompatibilityTool) error {
			stops++
			if tool.PID != old.PID {
				t.Fatalf("stopped PID %d, want %d", tool.PID, old.PID)
			}
			return nil
		},
	)
	if result.Error != nil || result.NewPID != 84 {
		t.Fatalf("healthy restart result = %+v", result)
	}
	if stops != 1 {
		t.Fatalf("healthy replacement stopped original %d time(s), want one", stops)
	}
}

func TestCompatibilityToolArguments(t *testing.T) {
	if got := compatibilityToolArguments(`D:\tools\AHK_F.exe`); !reflect.DeepEqual(got, []string{"/restart"}) {
		t.Fatalf("AHK restart arguments = %v", got)
	}
	if got := compatibilityToolArguments(`D:\tools\quickinput.exe`); got != nil {
		t.Fatalf("QuickInput restart arguments = %v, want none", got)
	}
}

func TestStartBundledAHKRejectsTamperedBinaryBeforeLaunch(t *testing.T) {
	root := t.TempDir()
	runtimePath := filepath.Join(root, "AHK_F.exe")
	if err := os.WriteFile(runtimePath, []byte("not the audited runtime"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := StartBundledAHK(runtimePath, []uint32{42})
	if result.Error == nil || result.NewPID != 0 {
		t.Fatalf("tampered bundled AHK was accepted: %+v", result)
	}
}
