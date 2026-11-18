package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"icl/internal/config"
	"icl/internal/database"
	"icl/internal/metrics"
	"icl/internal/middleware"
	"icl/internal/simulation"
)

// queryCounter wraps MemoryStore to count actual DB queries.
type queryCounter struct {
	*database.MemoryStore
	queries atomic.Int64
}

func (q *queryCounter) GetScore(ctx context.Context, id int) (database.Score, error) {
	q.queries.Add(1)
	return q.MemoryStore.GetScore(ctx, id)
}

func newTestApp(t *testing.T) (*App, *queryCounter) {
	t.Helper()
	cfg := config.Config{
		InstanceID:      "test",
		CacheEnabled:    true,
		DefaultCacheTTL: 200 * time.Millisecond, // short TTL for tests
		CacheMaxEntries: 1000,
		SimTargetURL:    "http://unused",
	}
	m := metrics.New()
	store := &queryCounter{MemoryStore: database.NewMemoryStore(m.RecordDBQuery)}
	app := NewApp(cfg, store, m, simulation.New())
	return app, store
}

func testServer(app *App) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/matches/{id}/score", app.GetScore)
	mux.HandleFunc("PUT /api/matches/{id}/score", app.UpdateScore)
	mux.HandleFunc("GET /api/matches", app.ListMatches)
	mux.HandleFunc("GET /api/leaderboard", app.Leaderboard)
	mux.HandleFunc("GET /api/health", app.Health)
	mux.HandleFunc("GET /api/stats", app.Stats)
	mux.HandleFunc("POST /api/cache/invalidate/{id}", app.InvalidateCache)
	return httptest.NewServer(middleware.Instance("test")(mux))
}

func TestCacheHitMiss(t *testing.T) {
	app, store := newTestApp(t)
	srv := testServer(app)
	defer srv.Close()

	// First request: miss -> 1 DB query.
	resp, err := http.Get(srv.URL + "/api/matches/104/score")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("X-Cache") != "MISS" {
		t.Fatalf("expected MISS, got %s", resp.Header.Get("X-Cache"))
	}
	if store.queries.Load() != 1 {
		t.Fatalf("expected 1 db query, got %d", store.queries.Load())
	}

	// Second request within TTL: hit -> still 1 DB query.
	resp, _ = http.Get(srv.URL + "/api/matches/104/score")
	resp.Body.Close()
	if resp.Header.Get("X-Cache") != "HIT" {
		t.Fatalf("expected HIT, got %s", resp.Header.Get("X-Cache"))
	}
	if store.queries.Load() != 1 {
		t.Fatalf("cache hit caused db query: %d", store.queries.Load())
	}
}

func TestTTLExpiry(t *testing.T) {
	app, store := newTestApp(t)
	srv := testServer(app)
	defer srv.Close()

	http.Get(srv.URL + "/api/matches/104/score")
	time.Sleep(300 * time.Millisecond) // TTL is 200ms
	resp, _ := http.Get(srv.URL + "/api/matches/104/score")
	resp.Body.Close()
	if resp.Header.Get("X-Cache") != "MISS" {
		t.Fatal("expected MISS after TTL expiry")
	}
	if store.queries.Load() != 2 {
		t.Fatalf("expected 2 db queries, got %d", store.queries.Load())
	}
}

func TestThunderingHerdThroughHTTP(t *testing.T) {
	app, store := newTestApp(t)
	srv := testServer(app)
	defer srv.Close()

	// Prime cache then let it expire.
	http.Get(srv.URL + "/api/matches/104/score")
	time.Sleep(300 * time.Millisecond)
	before := store.queries.Load()

	// Fire 300 simultaneous requests at the expired key.
	const n = 300
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, err := http.Get(srv.URL + "/api/matches/104/score")
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	close(start)
	wg.Wait()

	after := store.queries.Load() - before
	if after > 3 {
		t.Fatalf("thundering herd not coalesced: %d db queries for %d requests", after, n)
	}
}

func TestUpdateInvalidatesCache(t *testing.T) {
	app, store := newTestApp(t)
	srv := testServer(app)
	defer srv.Close()

	http.Get(srv.URL + "/api/matches/104/score") // prime cache
	q := store.queries.Load()

	body := `{"team1":"India","team2":"Australia","runs":150,"wickets":3,"overs":"16.0","status":"live"}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/matches/104/score", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("update failed: %d", resp.StatusCode)
	}

	resp2, err := http.Get(srv.URL + "/api/matches/104/score")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.Header.Get("X-Cache") != "MISS" {
		t.Fatal("expected MISS after invalidation")
	}
	var score database.Score
	json.NewDecoder(resp2.Body).Decode(&score)
	if score.Runs != 150 {
		t.Fatalf("expected updated runs 150, got %d", score.Runs)
	}
	if store.queries.Load() <= q {
		t.Fatal("expected a new db query after invalidation")
	}
}

func TestStatsEndpoint(t *testing.T) {
	app, _ := newTestApp(t)
	srv := testServer(app)
	defer srv.Close()

	http.Get(srv.URL + "/api/matches/104/score")
	http.Get(srv.URL + "/api/matches/104/score")

	resp, err := http.Get(srv.URL + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var snap map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&snap)
	cacheStats, ok := snap["cache"].(map[string]interface{})
	if !ok {
		t.Fatal("missing cache stats in /api/stats")
	}
	if cacheStats["hits"].(float64) != 1 {
		t.Fatalf("expected 1 hit, got %v", cacheStats["hits"])
	}
}

func TestCacheDisabled(t *testing.T) {
	cfg := config.Config{
		InstanceID:      "test",
		CacheEnabled:    false,
		DefaultCacheTTL: time.Second,
		CacheMaxEntries: 100,
	}
	m := metrics.New()
	store := &queryCounter{MemoryStore: database.NewMemoryStore(m.RecordDBQuery)}
	app := NewApp(cfg, store, m, simulation.New())
	srv := testServer(app)
	defer srv.Close()

	for i := 0; i < 3; i++ {
		resp, _ := http.Get(srv.URL + "/api/matches/104/score")
		resp.Body.Close()
	}
	if store.queries.Load() != 3 {
		t.Fatalf("cache-disabled should query db every time, got %d", store.queries.Load())
	}
}
