// Package api wires HTTP handlers to the cache, singleflight group,
// database store, metrics, and traffic simulator.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"icl/internal/cache"
	"icl/internal/config"
	"icl/internal/database"
	"icl/internal/metrics"
	"icl/internal/simulation"
	sf "icl/internal/singleflight"
)

// App holds the shared dependencies for all handlers.
type App struct {
	cfg    config.Config
	store  database.Store
	cache  *cache.Cache
	sf     *sf.Group
	metrics *metrics.Metrics
	sim    *simulation.Simulator
	start  time.Time
}

func NewApp(cfg config.Config, store database.Store, m *metrics.Metrics, sim *simulation.Simulator) *App {
	c := cache.New(cfg.DefaultCacheTTL, cfg.CacheMaxEntries)
	return &App{
		cfg:     cfg,
		store:   store,
		cache:   c,
		sf:      sf.New(m),
		metrics: m,
		sim:     sim,
		start:   time.Now(),
	}
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// scoreKey is the cache key for a match score.
func scoreKey(id int) string { return fmt.Sprintf("score:%d", id) }

// fetchScoreFromDB loads a score from the database. Called only on cache miss,
// and coalesced through singleflight so concurrent misses for the same key
// produce exactly one query.
func (a *App) fetchScoreFromDB(ctx context.Context, id int) (database.Score, error) {
	return a.store.GetScore(ctx, id)
}

// GetScore handles GET /api/matches/:id/score — the hot path.
//
// Lifecycle:
//  1. Cache hit  -> return immediately, zero DB queries.
//  2. Cache miss -> enter singleflight for this key; the leader queries
//     PostgreSQL once, populates the cache, and every waiter receives the
//     same result.
func (a *App) GetScore(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}

	// Fast path: cache hit (skipped entirely when caching is disabled).
	if a.cfg.CacheEnabled {
		if raw, hit := a.cache.Get(scoreKey(id)); hit {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Cache", "HIT")
			w.WriteHeader(http.StatusOK)
			w.Write(raw)
			return
		}
	}

	// Slow path: coalesced database fetch.
	res, err := a.sf.Do(r.Context(), scoreKey(id), func() (interface{}, error) {
		// Double-check the cache inside the flight: a previous leader may
		// have already populated it while we were waiting to lead.
		if a.cfg.CacheEnabled {
			if raw, hit := a.cache.Get(scoreKey(id)); hit {
				return raw, nil
			}
		}
		score, err := a.fetchScoreFromDB(r.Context(), id)
		if err != nil {
			return nil, err
		}
		raw, err := database.MarshalScore(score)
		if err != nil {
			return nil, err
		}
		if a.cfg.CacheEnabled {
			a.cache.Set(scoreKey(id), raw)
		}
		return raw, nil
	})
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "match not found")
		} else {
			log.Printf("score fetch error: %v", err)
			writeErr(w, http.StatusInternalServerError, "failed to fetch score")
		}
		return
	}

	raw, _ := res.Val.([]byte)
	// res.Val is json.RawMessage ([]byte) — normalize.
	if rb, ok2 := res.Val.(json.RawMessage); ok2 {
		raw = rb
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	w.WriteHeader(http.StatusOK)
	w.Write(raw)
}

// UpdateScore handles PUT /api/matches/:id/score — writes to the DB and
// invalidates the cache so the next read repopulates with fresh data.
func (a *App) UpdateScore(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var s database.Score
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&s); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if s.Runs < 0 || s.Wickets < 0 {
		writeErr(w, http.StatusBadRequest, "runs and wickets must be >= 0")
		return
	}
	s.MatchID = id
	s.LastUpdated = time.Now()
	if err := a.store.UpdateScore(r.Context(), id, s); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "match not found")
		} else {
			writeErr(w, http.StatusInternalServerError, "failed to update score")
		}
		return
	}
	// Invalidate: next read is a guaranteed cache miss -> fresh DB read.
	a.cache.Delete(scoreKey(id))
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "match_id": strconv.Itoa(id)})
}

func (a *App) ListMatches(w http.ResponseWriter, r *http.Request) {
	ms, err := a.store.ListMatches(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list matches")
		return
	}
	writeJSON(w, http.StatusOK, ms)
}

func (a *App) GetMatch(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	m, err := a.store.GetMatch(r.Context(), id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "match not found")
		} else {
			writeErr(w, http.StatusInternalServerError, "failed to get match")
		}
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (a *App) Leaderboard(w http.ResponseWriter, r *http.Request) {
	lb, err := a.store.Leaderboard(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load leaderboard")
		return
	}
	writeJSON(w, http.StatusOK, lb)
}

// Stats returns the combined monitoring snapshot.
func (a *App) Stats(w http.ResponseWriter, r *http.Request) {
	snap := a.metrics.Snapshot(a.start)
	snap.Cache = a.cache.Stats()
	writeJSON(w, http.StatusOK, snap)
}

func (a *App) CacheStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.cache.Stats())
}

// InvalidateCache handles POST /api/cache/invalidate/:id — forces the next
// score read to miss the cache (used to demonstrate TTL/invalidation).
func (a *App) InvalidateCache(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	deleted := a.cache.Delete(scoreKey(id))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"invalidated": deleted,
		"match_id":    id,
	})
}

// Health is the load-balancer health check endpoint.
func (a *App) Health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	status := "ok"
	code := http.StatusOK
	if _, err := a.store.GetMatch(ctx, 104); err != nil && !errors.Is(err, database.ErrNotFound) {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]string{
		"status":      status,
		"instance":    a.cfg.InstanceID,
		"cache":       strconv.FormatBool(a.cfg.CacheEnabled),
	})
}

// Simulation handlers -------------------------------------------------

type simStartReq struct {
	Users     int    `json:"users"`
	MatchID   int    `json:"match_id"`
	Duration  string `json:"duration"`
	TargetURL string `json:"target_url"`
}

func (a *App) SimStart(w http.ResponseWriter, r *http.Request) {
	var req simStartReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Users <= 0 {
		req.Users = 100
	}
	if req.MatchID <= 0 {
		req.MatchID = 104
	}
	if req.Duration == "" {
		req.Duration = "10s"
	}
	if req.TargetURL == "" {
		req.TargetURL = a.cfg.SimTargetURL
	}
	cfg := simulation.RunConfig{
		Users:     req.Users,
		MatchID:   req.MatchID,
		Duration:  req.Duration,
		TargetURL: req.TargetURL,
	}
	if err := a.sim.Start(cfg); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, a.sim.Status())
}

func (a *App) SimStop(w http.ResponseWriter, r *http.Request) {
	if err := a.sim.Stop(); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.sim.Status())
}

func (a *App) SimStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.sim.Status())
}

// SimSpike fires N simultaneous one-shot requests at the score endpoint,
// designed to trigger a thundering herd after cache expiry.
func (a *App) SimSpike(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Requests int `json:"requests"`
		MatchID  int `json:"match_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Requests <= 0 {
		req.Requests = 1000
	}
	if req.MatchID <= 0 {
		req.MatchID = 104
	}
	target := a.cfg.SimTargetURL + fmt.Sprintf("/api/matches/%d/score", req.MatchID)

	start := time.Now()
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        req.Requests,
			MaxIdleConnsPerHost: req.Requests,
			MaxConnsPerHost:     req.Requests,
		},
	}
	var wg sync.WaitGroup
	var okCount atomic.Int64
	for i := 0; i < req.Requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(target)
			if err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode == 200 {
					okCount.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"requests":     req.Requests,
		"succeeded":    okCount.Load(),
		"duration_ms":  time.Since(start).Milliseconds(),
		"match_id":     req.MatchID,
		"target":       target,
	})
}

func parseID(w http.ResponseWriter, r *http.Request) (int, bool) {
	idStr := r.PathValue("id")
	if idStr == "" {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		for i, p := range parts {
			if p == "matches" && i+1 < len(parts) {
				idStr = parts[i+1]
				break
			}
		}
	}
	if idStr == "" {
		writeErr(w, http.StatusBadRequest, "missing match id")
		return 0, false
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid match id")
		return 0, false
	}
	return id, true
}

// Cache returns the cache for external access (tests).
func (a *App) Cache() *cache.Cache { return a.cache }

// Metrics returns the metrics registry.
func (a *App) Metrics() *metrics.Metrics { return a.metrics }
