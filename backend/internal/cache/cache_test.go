package cache

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSetGet(t *testing.T) {
	c := New(2*time.Second, 100)
	defer c.Close()
	c.Set("k", json.RawMessage(`{"a":1}`))
	v, ok := c.Get("k")
	if !ok {
		t.Fatal("expected hit")
	}
	if string(v) != `{"a":1}` {
		t.Fatalf("unexpected value %s", v)
	}
}

func TestMiss(t *testing.T) {
	c := New(time.Second, 100)
	defer c.Close()
	if _, ok := c.Get("nope"); ok {
		t.Fatal("expected miss")
	}
}

func TestTTLExpiration(t *testing.T) {
	c := New(50*time.Millisecond, 100)
	defer c.Close()
	c.Set("k", json.RawMessage(`1`))
	time.Sleep(80 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatal("expected expired entry to miss")
	}
}

func TestDelete(t *testing.T) {
	c := New(time.Second, 100)
	defer c.Close()
	c.Set("k", json.RawMessage(`1`))
	if !c.Delete("k") {
		t.Fatal("expected delete to report true")
	}
	if c.Delete("k") {
		t.Fatal("second delete should be false")
	}
}

func TestStats(t *testing.T) {
	c := New(time.Second, 100)
	defer c.Close()
	c.Set("k", json.RawMessage(`1`))
	c.Get("k") // hit
	c.Get("missing")
	s := c.Stats()
	if s.Hits != 1 || s.Misses != 1 {
		t.Fatalf("bad stats: %+v", s)
	}
	if s.HitRatio < 0.49 || s.HitRatio > 0.51 {
		t.Fatalf("bad ratio: %v", s.HitRatio)
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := New(time.Second, 1000)
	defer c.Close()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(n int) {
			for j := 0; j < 500; j++ {
				key := string(rune('a' + n%26))
				c.Set(key, json.RawMessage(`1`))
				c.Get(key)
			}
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
