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
	onPanic    func(any)
	closing    bool
}

func New(panicHandler ...func(any)) *Manager {
	root, cancel := context.WithCancel(context.Background())
	var handler func(any)
	if len(panicHandler) > 0 {
		handler = panicHandler[0]
	}
	return &Manager{root: root, cancelRoot: cancel, tasks: make(map[uint64]context.CancelFunc), onPanic: handler}
}

func (m *Manager) Run(work func(context.Context, uint64)) uint64 {
	id := m.nextID.Add(1)
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return 0
	}
	ctx, cancel := context.WithCancel(m.root)
	m.tasks[id] = cancel
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		defer func() {
			m.mu.Lock()
			delete(m.tasks, id)
			m.mu.Unlock()
			cancel()
		}()
		defer func() {
			if value := recover(); value != nil && m.onPanic != nil {
				m.onPanic(value)
			}
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
	m.mu.Lock()
	m.closing = true
	m.mu.Unlock()
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
