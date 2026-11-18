// Package cache implements a concurrent-safe in-memory cache with TTL-based
// expiration, automatic cleanup, and statistics tracking.
package cache

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// Entry is a single cached value with its expiration time.
type Entry struct {
	Value     json.RawMessage
	ExpiresAt time.Time
}

type stats struct {
	hits       atomic.Int64
	misses     atomic.Int64
	sets       atomic.Int64
	deletes    atomic.Int64
	expirations atomic.Int64
}

// Cache is a sharded-by-RWMutex in-memory key/value store.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	ttl     time.Duration
	max     int
	stats   stats
	done    chan struct{}
}

func New(defaultTTL time.Duration, maxEntries int) *Cache {
	c := &Cache{
		entries: make(map[string]*Entry, 1024),
		ttl:     defaultTTL,
		max:     maxEntries,
		done:    make(chan struct{}),
	}
	go c.janitor()
	return c
}

// janitor periodically sweeps expired entries so memory does not grow unbounded.
func (c *Cache) janitor() {
	t := time.NewTicker(c.ttl / 2)
	if t == nil {
		t = time.NewTicker(time.Second)
	}
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			c.sweep()
		}
	}
}

func (c *Cache) sweep() {
	now := time.Now()
	c.mu.Lock()
	for k, e := range c.entries {
		if now.After(e.ExpiresAt) {
			delete(c.entries, k)
			c.stats.expirations.Add(1)
		}
	}
	c.mu.Unlock()
}

// Close stops the background janitor goroutine.
func (c *Cache) Close() { close(c.done) }

// Get returns the cached value. A hit is only counted if the entry exists and
// has not expired; expired entries count as misses and are removed lazily.
func (c *Cache) Get(key string) (json.RawMessage, bool) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		c.stats.misses.Add(1)
		return nil, false
	}
	if time.Now().After(e.ExpiresAt) {
		c.mu.Lock()
		// Re-check under write lock: another goroutine may have refreshed it.
		if cur, still := c.entries[key]; still && time.Now().After(cur.ExpiresAt) {
			delete(c.entries, key)
			c.stats.expirations.Add(1)
		}
		c.mu.Unlock()
		c.stats.misses.Add(1)
		return nil, false
	}
	c.stats.hits.Add(1)
	return e.Value, true
}

// Set stores value under key with the cache's default TTL.
func (c *Cache) Set(key string, value json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Simple eviction: if at capacity, drop an arbitrary entry (map order).
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.max {
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}
	cp := make(json.RawMessage, len(value))
	copy(cp, value)
	c.entries[key] = &Entry{Value: cp, ExpiresAt: time.Now().Add(c.ttl)}
	c.stats.sets.Add(1)
}

// SetTTL stores value under key with an explicit TTL.
func (c *Cache) SetTTL(key string, value json.RawMessage, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make(json.RawMessage, len(value))
	copy(cp, value)
	c.entries[key] = &Entry{Value: cp, ExpiresAt: time.Now().Add(ttl)}
	c.stats.sets.Add(1)
}

// Delete removes a key. Returns true if the key existed.
func (c *Cache) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; ok {
		delete(c.entries, key)
		c.stats.deletes.Add(1)
		return true
	}
	return false
}

// Stats is a snapshot of cache counters and memory usage.
type Stats struct {
	Hits         int64   `json:"hits"`
	Misses       int64   `json:"misses"`
	HitRatio     float64 `json:"hit_ratio"`
	Sets         int64   `json:"sets"`
	Deletes      int64   `json:"deletes"`
	Expirations  int64   `json:"expirations"`
	Entries      int     `json:"entries"`
	MemoryBytes  int64   `json:"memory_bytes"`
	TTLSeconds   float64 `json:"ttl_seconds"`
}

func (c *Cache) Stats() Stats {
	c.mu.RLock()
	n := len(c.entries)
	var bytes int64
	for _, e := range c.entries {
		bytes += int64(len(e.Value))
	}
	c.mu.RUnlock()
	hits := c.stats.hits.Load()
	misses := c.stats.misses.Load()
	total := hits + misses
	ratio := 0.0
	if total > 0 {
		ratio = float64(hits) / float64(total)
	}
	return Stats{
		Hits:        hits,
		Misses:      misses,
		HitRatio:    ratio,
		Sets:        c.stats.sets.Load(),
		Deletes:     c.stats.deletes.Load(),
		Expirations: c.stats.expirations.Load(),
		Entries:     n,
		MemoryBytes: bytes,
		TTLSeconds:  c.ttl.Seconds(),
	}
}
