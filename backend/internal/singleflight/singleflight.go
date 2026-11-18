// Package singleflight implements request coalescing ("singleflight"):
// concurrent requests for the same cache key share a single in-flight
// execution, collapsing the thundering herd of simultaneous cache misses
// into one database query.
//
// This is an equivalent mechanism to golang.org/x/sync/singleflight,
// extended with:
//   - per-call waiter tracking (for metrics),
//   - context cancellation so waiting requests can abort when their
//     HTTP client disconnects.
package singleflight

import (
	"context"
	"sync"

	"icl/internal/metrics"
)

// call represents one in-flight execution shared by many waiters.
type call struct {
	done  chan struct{}
	val   interface{}
	err   error
	waits int32 // number of *additional* waiters beyond the leader
}

// Group manages in-flight calls keyed by string.
type Group struct {
	mu    sync.Mutex
	m     *metrics.Metrics
	calls map[string]*call
}

func New(m *metrics.Metrics) *Group {
	return &Group{m: m, calls: make(map[string]*call)}
}

// Result is what waiters receive.
type Result struct {
	Val    interface{}
	Err    error
	Shared bool // true if this caller joined an already in-flight call
}

// Do executes fn for key, coalescing concurrent callers.
//
// The first caller becomes the leader and runs fn. Concurrent callers
// for the same key block until fn completes and receive the same result.
// The ctx is honored: if the caller's context is cancelled while waiting,
// Do returns ctx.Err() without affecting the shared call.
func (g *Group) Do(ctx context.Context, key string, fn func() (interface{}, error)) (Result, error) {
	g.mu.Lock()
	if c, ok := g.calls[key]; ok {
		// Join the in-flight call.
		c.waits++
		g.mu.Unlock()
		if g.m != nil {
			g.m.Coalesced.Add(1)
			g.m.DBSaved.Add(1)
		}
		select {
		case <-c.done:
			return Result{Val: c.val, Err: c.err, Shared: true}, c.err
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	// Become the leader.
	c := &call{done: make(chan struct{})}
	g.calls[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()

	g.mu.Lock()
	delete(g.calls, key)
	close(c.done)
	g.mu.Unlock()

	return Result{Val: c.val, Err: c.err, Shared: false}, c.err
}
