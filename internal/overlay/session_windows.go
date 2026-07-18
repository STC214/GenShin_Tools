package overlay

import (
	"context"
	"sync"
	"time"

	"genshintools/internal/gamewindow"
)

type Session struct {
	cancel    context.CancelFunc
	done      chan struct{}
	window    *nativeWindow
	sampler   *NativeSampler
	closeOnce sync.Once
}

func Start(target gamewindow.Target, config Config, publish func(Stats)) (*Session, error) {
	normalized, err := config.Normalized()
	if err != nil {
		return nil, err
	}
	sampler, err := NewNativeSampler(target)
	if err != nil {
		return nil, err
	}
	window, err := startNativeWindow(target, normalized)
	if err != nil {
		sampler.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{cancel: cancel, done: make(chan struct{}), window: window, sampler: sampler}
	go session.run(ctx, publish)
	return session, nil
}

func (session *Session) run(ctx context.Context, publish func(Stats)) {
	defer close(session.done)
	defer session.sampler.Close()
	defer session.window.requestClose()
	defer func() { _ = recover() }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-session.window.done:
			return
		case <-ticker.C:
			stats, err := session.sampler.Sample()
			if err != nil {
				return
			}
			session.window.update(stats)
			if publish != nil {
				publish(stats)
			}
		}
	}
}

func (session *Session) Close(ctx context.Context) error {
	session.closeOnce.Do(session.cancel)
	windowErr := session.window.close(ctx)
	select {
	case <-session.done:
		return windowErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (session *Session) Done() <-chan struct{} { return session.done }
