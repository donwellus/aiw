package engine

import (
	"context"
	"sync"
)

type Limiter struct {
	mu    sync.Mutex
	semis map[string]chan struct{}
}

func NewLimiter() *Limiter {
	return &Limiter{semis: map[string]chan struct{}{}}
}

func (l *Limiter) Acquire(ctx context.Context, key string, limit int) (func(), error) {
	if limit <= 0 {
		return func() {}, nil
	}
	l.mu.Lock()
	sem, ok := l.semis[key]
	if !ok {
		sem = make(chan struct{}, limit)
		l.semis[key] = sem
	}
	l.mu.Unlock()

	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
