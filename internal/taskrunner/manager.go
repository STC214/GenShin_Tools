// Package taskrunner owns cancellable background work and stale-result IDs.
package taskrunner

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type Manager struct {
	root       context.Context
	cancelRoot context.CancelFunc
	nextID     atomic.Uint64
	mu         sync.Mutex
	tasks      map[uint64]context.CancelFunc
	wg         sync.WaitGroup
}

func New() *Manager {
	root, cancel := context.WithCancel(context.Background())
	return &Manager{root: root, cancelRoot: cancel, tasks: make(map[uint64]context.CancelFunc)}
}

func (m *Manager) Run(work func(context.Context, uint64)) uint64 {
	id := m.nextID.Add(1)
	ctx, cancel := context.WithCancel(m.root)
	m.mu.Lock()
	m.tasks[id] = cancel
	m.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			m.mu.Lock()
			delete(m.tasks, id)
			m.mu.Unlock()
			cancel()
		}()
		work(ctx, id)
	}()
	return id
}

func (m *Manager) Cancel(id uint64) {
	m.mu.Lock()
	cancel := m.tasks[id]
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *Manager) Shutdown(timeout time.Duration) bool {
	m.cancelRoot()
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (m *Manager) Active() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.tasks)
}
