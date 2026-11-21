package cli

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type drainState struct {
	drain     chan struct{}
	done      chan struct{}
	sigs      chan os.Signal
	cancel    context.CancelFunc
	once      sync.Once
	pauseOnce sync.Once
}

func newDrainState(timeout time.Duration) (*drainState, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	state := &drainState{
		drain:  make(chan struct{}),
		done:   make(chan struct{}),
		sigs:   make(chan os.Signal, 2),
		cancel: cancel,
	}
	signal.Notify(state.sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-state.sigs:
			state.Pause()
			select {
			case <-state.sigs:
				cancel()
			case <-time.After(timeout):
				cancel()
			case <-state.done:
			}
		case <-state.done:
		}
	}()
	return state, ctx
}

func (d *drainState) Pause() {
	d.pauseOnce.Do(func() { close(d.drain) })
}

func (d *drainState) Draining() bool {
	select {
	case <-d.drain:
		return true
	default:
		return false
	}
}

func (d *drainState) Close() {
	d.once.Do(func() {
		close(d.done)
		signal.Stop(d.sigs)
		d.cancel()
	})
}
