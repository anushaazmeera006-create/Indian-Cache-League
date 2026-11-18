package singleflight

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestThunderingHerd proves that N concurrent callers for the same key
// produce exactly ONE execution of fn.
func TestThunderingHerd(t *testing.T) {
	g := New(nil)
	const callers = 500
	var executions atomic.Int64

	fn := func() (interface{}, error) {
		executions.Add(1)
		// Simulate a slow DB query so all callers overlap.
		time.Sleep(50 * time.Millisecond)
		return "score", nil
	}

	var wg sync.WaitGroup
	results := make([]Result, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res, err := g.Do(context.Background(), "score:104", fn)
			results[i] = res
			errs[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	if got := executions.Load(); got != 1 {
		t.Fatalf("expected 1 execution, got %d", got)
	}
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("caller %d err: %v", i, errs[i])
		}
		if results[i].Val != "score" {
			t.Fatalf("caller %d bad val %v", i, results[i].Val)
		}
	}
	// At least callers-1 should be marked shared.
	shared := 0
	for _, r := range results {
		if r.Shared {
			shared++
		}
	}
	if shared < callers-1 {
		t.Fatalf("expected ~%d shared, got %d", callers-1, shared)
	}
}

// TestDifferentKeysDontBlock verifies independent keys execute in parallel.
func TestDifferentKeysDontBlock(t *testing.T) {
	g := New(nil)
	var wg sync.WaitGroup
	for _, key := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			g.Do(context.Background(), k, func() (interface{}, error) {
				time.Sleep(30 * time.Millisecond)
				return k, nil
			})
		}(key)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("keys appear to block each other")
	}
}

// TestContextCancel verifies a waiter can abort via context.
func TestContextCancel(t *testing.T) {
	g := New(nil)
	release := make(chan struct{})
	go g.Do(context.Background(), "k", func() (interface{}, error) {
		<-release
		return 1, nil
	})
	time.Sleep(10 * time.Millisecond) // let leader start

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := g.Do(ctx, "k", func() (interface{}, error) { return 2, nil })
	if err != context.DeadlineExceeded {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	close(release)
}
