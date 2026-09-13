// Package workgroup tracks units of in-flight work so that a shutdown can
// cancel all of them and wait until each has finished cleaning up.
package workgroup

import (
	"context"
	"errors"
	"sync"
)

// ErrClosed is returned by Start once the group has shut down.
var ErrClosed = errors.New("workgroup: closed")

// Group tracks in-flight work. The zero value is ready to use.
//
// Each unit receives a context that is cancelled when the caller's parent
// context is, when Cancel is called, or when the group shuts down. The unit
// reports completion through the finish function it was handed, and must do
// so only after its own cleanup (process exit, temporary files) is complete.
type Group struct {
	mu     sync.Mutex
	closed bool
	root   context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (g *Group) resetLocked() {
	g.root, g.cancel = context.WithCancel(context.Background())
}

// Start registers a unit of work derived from parent. It returns ErrClosed
// after Shutdown or Close. finish is idempotent and never nil on success.
func (g *Group) Start(parent context.Context) (ctx context.Context, finish func(), err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, nil, ErrClosed
	}
	if g.root == nil {
		g.resetLocked()
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(g.root, cancel)
	g.wg.Add(1)
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			stop()
			cancel()
			g.wg.Done()
		})
	}, nil
}

// Cancel cancels every unit started so far. The group stays open, so new
// work can still start; use it to abandon stale work on a refresh.
func (g *Group) Cancel() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.root != nil {
		g.cancel()
	}
	g.resetLocked()
}

// Shutdown cancels every unit and refuses new ones without waiting. Use it
// where blocking is not acceptable; a later Wait completes the shutdown.
func (g *Group) Shutdown() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
	if g.root != nil {
		g.cancel()
	}
}

// Wait blocks until every started unit has called finish. Call it after
// Shutdown, so no unit can start while Wait is in progress.
func (g *Group) Wait() { g.wg.Wait() }

// Close is Shutdown followed by Wait.
func (g *Group) Close() {
	g.Shutdown()
	g.Wait()
}
