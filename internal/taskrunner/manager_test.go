package taskrunner

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManagerCancelsAndWaits(t *testing.T) {
	manager := New()
	started := make(chan struct{})
	id := manager.Run(func(ctx context.Context, _ uint64) {
		close(started)
		<-ctx.Done()
	})
	<-started
	if id == 0 || manager.Active() != 1 {
		t.Fatalf("id=%d active=%d", id, manager.Active())
	}
	manager.Cancel(id)
	if !manager.Shutdown(time.Second) {
		t.Fatal("Shutdown timed out")
	}
	if manager.Active() != 0 {
		t.Fatalf("active=%d", manager.Active())
	}
}

func TestManagerContainsTaskPanic(t *testing.T) {
	panics := make(chan any, 1)
	manager := New(func(value any) { panics <- value })
	manager.Run(func(context.Context, uint64) { panic(errors.New("fixture panic")) })
	if !manager.Shutdown(time.Second) {
		t.Fatal("Shutdown timed out after recovered panic")
	}
	select {
	case value := <-panics:
		if value == nil {
			t.Fatal("panic handler received nil")
		}
	default:
		t.Fatal("panic handler was not called")
	}
}

func TestManagerShutdownTimeout(t *testing.T) {
	manager := New()
	release := make(chan struct{})
	manager.Run(func(context.Context, uint64) { <-release })
	if manager.Shutdown(time.Millisecond) {
		t.Fatal("Shutdown unexpectedly completed")
	}
	close(release)
	if !manager.Shutdown(time.Second) {
		t.Fatal("second Shutdown timed out")
	}
}

func TestManagerRejectsRunAfterShutdownStarts(t *testing.T) {
	manager := New()
	if !manager.Shutdown(time.Second) {
		t.Fatal("empty manager shutdown timed out")
	}
	called := false
	if id := manager.Run(func(context.Context, uint64) { called = true }); id != 0 {
		t.Fatalf("post-shutdown task id = %d, want 0", id)
	}
	if called || manager.Active() != 0 {
		t.Fatal("post-shutdown task ran")
	}
}
