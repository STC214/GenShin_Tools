package capture

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type blockingCapturer struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (capturer *blockingCapturer) Capture(_ Target, path string) error {
	capturer.calls.Add(1)
	capturer.started <- struct{}{}
	<-capturer.release
	return os.WriteFile(path, []byte("fixture"), 0o644)
}

func TestManagerBoundsBurstToRunningPlusOnePending(t *testing.T) {
	directory := t.TempDir()
	capturer := &blockingCapturer{started: make(chan struct{}, 2), release: make(chan struct{}, 2)}
	results := make(chan Result, 2)
	manager := NewManager(capturer, func(result Result) { results <- result })
	config := DefaultConfig()
	config.Enabled, config.SaveDir = true, directory
	if err := manager.Configure(config); err != nil {
		t.Fatal(err)
	}
	manager.SetTarget(&Target{PID: 1, CreationTime: 2})
	if !manager.Request() {
		t.Fatal("first request was rejected")
	}
	select {
	case <-capturer.started:
	case <-time.After(time.Second):
		t.Fatal("capture did not start")
	}
	if !manager.Request() {
		t.Fatal("one pending request should be accepted")
	}
	if manager.Request() {
		t.Fatal("third burst request should be dropped")
	}
	capturer.release <- struct{}{}
	select {
	case <-capturer.started:
	case <-time.After(time.Second):
		t.Fatal("pending capture did not start")
	}
	capturer.release <- struct{}{}
	for range 2 {
		select {
		case result := <-results:
			if result.Error != "" || result.Path == "" {
				t.Fatalf("result = %+v", result)
			}
		case <-time.After(time.Second):
			t.Fatal("capture result timed out")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if capturer.calls.Load() != 2 {
		t.Fatalf("captures = %d, want 2", capturer.calls.Load())
	}
}

func TestConfigRejectsInputPhysicalKeyConflict(t *testing.T) {
	config := DefaultConfig()
	if config.ConflictsWith(0x77, 'F', 0x7B) {
		t.Fatal("default screenshot hotkey conflicts with S03 defaults")
	}
	config.VirtualKey = 0x7B
	if !config.ConflictsWith(0x77, 'F', 0x7B) {
		t.Fatal("F12 conflict was not detected")
	}
	config.VirtualKey = 0x21
	if !config.ConflictsWith(0x200 | 0x100 | 0x21) {
		t.Fatal("encoded Page Up conflict was not detected")
	}
	if !config.ConflictsWith(0x200 | 0x69) {
		t.Fatal("Num 9/Page Up alias conflict was not detected")
	}
	config.VirtualKey = 0x69
	if !config.ConflictsWith(0x200 | 0x100 | 0x21) {
		t.Fatal("Page Up/Num 9 reverse alias conflict was not detected")
	}
}

func TestHotkeyStringUsesKeyboardLabel(t *testing.T) {
	config := DefaultConfig()
	name := config.HotkeyString()
	if strings.Contains(strings.ToUpper(name), "VK") || strings.Contains(strings.ToLower(name), "0x") {
		t.Fatalf("hotkey string exposed an internal key code: %q", name)
	}
}

func TestConfigPreservesPortableRelativeSaveDirectory(t *testing.T) {
	config := DefaultConfig()
	config.SaveDir = filepath.Join("data", "screenshots", "..", "screenshots")
	normalized, err := config.Normalized()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("data", "screenshots")
	if normalized.SaveDir != want {
		t.Fatalf("save directory = %q, want portable %q", normalized.SaveDir, want)
	}
}

func TestWritePNGAtomic(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	frame.Pix[0] = 0xFF
	path := filepath.Join(t.TempDir(), "capture.png")
	if err := writePNGAtomic(path, frame); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("PNG not committed: %v", err)
	}
}
