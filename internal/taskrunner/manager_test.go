package taskrunner

import (
	"context"
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
