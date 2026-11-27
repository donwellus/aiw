package runner

import (
	"context"
	"sync"
)

type Pool struct {
	ctx        context.Context
	drain      <-chan struct{}
	execute    func(context.Context, Request) Outcome
	onOutcome  func(Outcome)
	capacity   int
	mu         sync.Mutex
	callbackMu sync.Mutex
	cond       *sync.Cond
	pending    []Job
	active     int
	paused     bool
	stopping   bool
	outcomes   []Outcome
	wg         sync.WaitGroup
	done       chan struct{}
	stopOnce   sync.Once
}

func (r *Runner) StartPool(ctx context.Context, workers, capacity int, drain <-chan struct{}, onOutcome func(Outcome)) *Pool {
	if workers < 1 {
		workers = 1
	}
	pool := &Pool{
		ctx:       ctx,
		drain:     drain,
		execute:   r.Execute,
		onOutcome: onOutcome,
		capacity:  capacity,
		done:      make(chan struct{}),
	}
	pool.cond = sync.NewCond(&pool.mu)
	for i := 0; i < workers; i++ {
		pool.wg.Add(1)
		go pool.worker()
	}
	go func() {
		select {
		case <-ctx.Done():
		case <-drain:
		case <-pool.done:
			return
		}
		pool.Stop()
	}()
	return pool
}

func (p *Pool) Submit(job Job, before func() error) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopping || p.paused || p.ctx.Err() != nil || p.draining() {
		return false
	}
	if p.capacity > 0 && len(p.pending) >= p.capacity {
		return false
	}
	if before != nil {
		if err := before(); err != nil {
			return false
		}
	}
	p.pending = append(p.pending, job)
	p.cond.Signal()
	return true
}

func (p *Pool) SetPaused(paused bool) {
	p.mu.Lock()
	if !p.stopping {
		p.paused = paused
	}
	p.mu.Unlock()
	p.cond.Broadcast()
}

func (p *Pool) Stop() {
	p.stopOnce.Do(func() {
		p.mu.Lock()
		p.stopping = true
		p.mu.Unlock()
		close(p.done)
		p.cond.Broadcast()
	})
}

func (p *Pool) Wait() []Outcome {
	p.wg.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Outcome(nil), p.outcomes...)
}

func (p *Pool) WaitAll() []Outcome {
	p.mu.Lock()
	for !p.stopping && p.ctx.Err() == nil && (len(p.pending) > 0 || p.active > 0) {
		p.cond.Wait()
	}
	p.mu.Unlock()
	p.Stop()
	return p.Wait()
}

func (p *Pool) worker() {
	defer p.wg.Done()
	for {
		job, ok := p.next()
		if !ok {
			return
		}
		outcome := p.execute(p.ctx, Request(job))
		p.mu.Lock()
		p.active--
		p.outcomes = append(p.outcomes, outcome)
		p.cond.Broadcast()
		p.mu.Unlock()
		if p.onOutcome != nil {
			p.callbackMu.Lock()
			p.onOutcome(outcome)
			p.callbackMu.Unlock()
		}
	}
}

func (p *Pool) next() (Job, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		if p.stopping || p.ctx.Err() != nil || p.draining() {
			return Job{}, false
		}
		if !p.paused && len(p.pending) > 0 {
			job := p.pending[0]
			p.pending = p.pending[1:]
			p.active++
			return job, true
		}
		p.cond.Wait()
	}
}

func (p *Pool) draining() bool {
	if p.drain == nil {
		return false
	}
	select {
	case <-p.drain:
		return true
	default:
		return false
	}
}
