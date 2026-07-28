//go:build windows

package shell

import (
	"context"
	"testing"
	"time"

	"genshintools/internal/input"
)

func TestInputProcessFingerprintTracksExactLifetimes(t *testing.T) {
	first := inputProcessFingerprint([]input.GameProcess{{PID: 10, CreationTime: 100}, {PID: 20, CreationTime: 200}})
	same := inputProcessFingerprint([]input.GameProcess{{PID: 10, CreationTime: 100}, {PID: 20, CreationTime: 200}})
	reused := inputProcessFingerprint([]input.GameProcess{{PID: 10, CreationTime: 101}, {PID: 20, CreationTime: 200}})
	if first == "" || first != same {
		t.Fatalf("equivalent process lifetimes produced unstable fingerprints: %q / %q", first, same)
	}
	if first == reused {
		t.Fatal("PID reuse with a new creation time did not change the fingerprint")
	}
}

func TestInjectionInputBarrierRequiresBothCompletionSignals(t *testing.T) {
	tests := []struct {
		launching, helperDone, gameRunning bool
		want                               bool
	}{
		{true, true, true, true},
		{true, true, false, false},
		{true, false, true, false},
		{false, true, true, false},
	}
	for _, test := range tests {
		if got := injectionInputBarrierReady(test.launching, test.helperDone, test.gameRunning); got != test.want {
			t.Fatalf("barrier(%t,%t,%t) = %t, want %t", test.launching, test.helperDone, test.gameRunning, got, test.want)
		}
	}
}

func TestPendingToolsAfterRestartRetainsExactOwnership(t *testing.T) {
	original := []input.ExternalCompatibilityTool{
		{PID: 10, Path: `D:\Tools\AHK_F.exe`, CreationTime: 100},
		{PID: 20, Path: `D:\Tools\quickinput.exe`, CreationTime: 200},
	}
	results := []input.ExternalToolRestartResult{
		{Path: `D:\Tools\AHK_F.exe`, NewPID: 11, NewCreationTime: 101},
		{Path: `D:\Tools\quickinput.exe`, Error: context.Canceled},
	}
	pending := pendingToolsAfterRestart(original, results)
	if len(pending) != 2 {
		t.Fatalf("pending tools = %v, want two exact lifetimes", pending)
	}
	if pending[0].PID != 11 || pending[0].CreationTime != 101 {
		t.Fatalf("successful replacement ownership = %+v", pending[0])
	}
	if pending[1] != original[1] {
		t.Fatalf("failed replacement did not retain original ownership: %+v", pending[1])
	}
}

func TestPendingToolGenerationRejectsStaleFinalizer(t *testing.T) {
	app := &application{}
	first := []input.ExternalCompatibilityTool{{PID: 10, Path: `D:\Tools\AHK_F.exe`, CreationTime: 100}}
	firstGeneration := app.setPendingInputTools(first)
	second := []input.ExternalCompatibilityTool{{PID: 20, Path: `D:\Tools\quickinput.exe`, CreationTime: 200}}
	app.setPendingInputTools(second)
	if _, replaced := app.replacePendingInputToolsGeneration(firstGeneration, first); replaced {
		t.Fatal("stale finalizer replaced newer pending tool ownership")
	}
	if got := app.pendingInputTools(); len(got) != 1 || got[0] != second[0] {
		t.Fatalf("newer pending ownership changed: %+v", got)
	}
}

func TestPendingToolGenerationRejectsCommitAfterRestartOverlap(t *testing.T) {
	app := &application{}
	originalGeneration := app.setPendingInputTools([]input.ExternalCompatibilityTool{{
		PID: 10, Path: `D:\Tools\AHK_F.exe`, CreationTime: 100,
	}})
	restartedGeneration, replaced := app.replacePendingInputToolsGeneration(originalGeneration, []input.ExternalCompatibilityTool{{
		PID: 11, Path: `D:\Tools\AHK_F.exe`, CreationTime: 101,
	}})
	if !replaced {
		t.Fatal("current finalizer could not take ownership of its replacement")
	}
	newer := []input.ExternalCompatibilityTool{{
		PID: 20, Path: `D:\Tools\quickinput.exe`, CreationTime: 200,
	}}
	app.setPendingInputTools(newer)
	if app.clearPendingInputToolsGeneration(restartedGeneration) {
		t.Fatal("stale post-restart commit cleared a newer launch epoch")
	}
	if got := app.pendingInputTools(); len(got) != 1 || got[0] != newer[0] {
		t.Fatalf("newer launch ownership changed after stale commit: %+v", got)
	}
}

func TestLaunchRemainsBusyDuringPendingToolRestore(t *testing.T) {
	app := &application{}
	app.inputRestoreRunning.Store(true)
	if !app.launchBusy() {
		t.Fatal("new launch was accepted while user input tools were being restored")
	}
}

func TestWaitForReadinessDelayStartsAfterReadinessAndForegroundDoesNotResetIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := time.Now()
	readiness := func() bool {
		return time.Since(started) >= 100*time.Millisecond
	}
	foreground := func() bool {
		// Foreground returns only after the independent 150ms post-readiness
		// delay has elapsed. It must not start a second 150ms interval.
		return time.Since(started) >= 300*time.Millisecond
	}
	if !waitForReadinessDelayAndForeground(ctx, readiness, foreground, 150*time.Millisecond) {
		t.Fatal("post-injection delay and foreground were not detected")
	}
	if elapsed := time.Since(started); elapsed < 300*time.Millisecond || elapsed >= 440*time.Millisecond {
		t.Fatalf("foreground incorrectly reset or shortened the independent delay: %v", elapsed)
	}
}

func TestWaitForReadinessDelayHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitForReadinessDelayAndForeground(ctx, func() bool { return true }, func() bool { return true }, 100*time.Millisecond) {
		t.Fatal("cancelled wait unexpectedly succeeded")
	}
}
