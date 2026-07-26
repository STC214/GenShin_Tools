//go:build windows

package shell

import (
	"context"
	"testing"
	"time"

	"genshintools/internal/input"
)

func TestCapturedAHKToolUsesExactExecutableName(t *testing.T) {
	tests := []struct {
		name  string
		tools []input.ExternalCompatibilityTool
		want  bool
	}{
		{
			name:  "AHK_F",
			tools: []input.ExternalCompatibilityTool{{Path: `D:\Tools\AHK_F.exe`}},
			want:  true,
		},
		{
			name:  "case insensitive",
			tools: []input.ExternalCompatibilityTool{{Path: `D:\Tools\ahk_f.EXE`}},
			want:  true,
		},
		{
			name:  "similar name is rejected",
			tools: []input.ExternalCompatibilityTool{{Path: `D:\Tools\my_AHK_F.exe`}},
			want:  false,
		},
		{
			name:  "quick input only",
			tools: []input.ExternalCompatibilityTool{{Path: `D:\Tools\quickinput.exe`}},
			want:  false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := capturedAHKTool(test.tools); got != test.want {
				t.Fatalf("capturedAHKTool() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestWaitForStableGameForegroundRequiresContinuousForeground(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	calls := 0
	foreground := func() bool {
		calls++
		// The first foreground period is interrupted and must not count toward
		// the stable period. The later period remains continuously foreground.
		return calls != 2
	}
	started := time.Now()
	if !waitForStableGameForeground(ctx, foreground, 150*time.Millisecond) {
		t.Fatal("stable foreground was not detected")
	}
	if elapsed := time.Since(started); elapsed < 250*time.Millisecond {
		t.Fatalf("returned before a continuous stable period elapsed: %v", elapsed)
	}
}

func TestWaitForStableGameForegroundHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitForStableGameForeground(ctx, func() bool { return false }, 100*time.Millisecond) {
		t.Fatal("cancelled wait unexpectedly succeeded")
	}
}
