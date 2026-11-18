// Package simulation implements the admin traffic generator. It fires real
// HTTP requests at the configured target (the load balancer or a backend)
// so every simulated user produces genuine network traffic through the
// entire stack: nginx -> backend -> cache/singleflight -> database.
package simulation

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// RunConfig describes one simulation run.
type RunConfig struct {
	Users     int    `json:"users"`
	MatchID   int    `json:"match_id"`
	Duration  string `json:"duration"` // e.g. "10s"
	Endpoint  string `json:"endpoint"` // e.g. "/api/matches/104/score"
	TargetURL string `json:"target_url"`
}

// Status is a snapshot of the active (or last) run.
type Status struct {
	Running      bool      `json:"running"`
	Config       RunConfig `json:"config"`
	Requests     int64     `json:"requests_sent"`
	Successes    int64     `json:"successes"`
	Failures     int64     `json:"failures"`
	CacheHits    int64     `json:"cache_hits"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
}

// Simulator runs concurrent HTTP load against a target URL.
type Simulator struct {
	mu     sync.Mutex
	status Status
	cancel context.CancelFunc
	client *http.Client
	hits   atomic.Int64
}

func New() *Simulator {
	return &Simulator{
		client: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10000,
				MaxIdleConnsPerHost: 5000,
				MaxConnsPerHost:     0, // unlimited
			},
		},
	}
}

// Start launches a new simulation. Returns error if one is already running.
func (s *Simulator) Start(cfg RunConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.Running {
		return fmt.Errorf("simulation already running")
	}
	dur, err := time.ParseDuration(cfg.Duration)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", cfg.Duration, err)
	}
	if cfg.Users <= 0 {
		return fmt.Errorf("users must be > 0")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = fmt.Sprintf("/api/matches/%d/score", cfg.MatchID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), dur)
	s.cancel = cancel
	s.hits.Store(0)
	s.status = Status{Running: true, Config: cfg, StartedAt: time.Now()}

	go s.run(ctx, cfg)
	return nil
}

// Stop halts the active simulation.
func (s *Simulator) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.status.Running {
		return fmt.Errorf("no simulation running")
	}
	s.cancel()
	return nil
}

// Status returns the current snapshot.
func (s *Simulator) Status() Status {
	s.mu.Lock()
	st := s.status
	s.mu.Unlock()
	st.CacheHits = s.hits.Load()
	return st
}

func (s *Simulator) run(ctx context.Context, cfg RunConfig) {
	url := cfg.TargetURL + cfg.Endpoint
	var wg sync.WaitGroup
	var sent, ok, fail atomic.Int64

	// Each "user" is a goroutine issuing requests as fast as it can for the
	// duration, like a browser polling a live score.
	for i := 0; i < cfg.Users; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
				if err != nil {
					fail.Add(1)
					continue
				}
				resp, err := s.client.Do(req)
				sent.Add(1)
				if err != nil {
					fail.Add(1)
					// brief backoff on transport errors
					time.Sleep(50 * time.Millisecond)
					continue
				}
				io.Copy(io.Discard, resp.Body)
				if resp.Header.Get("X-Cache") == "HIT" {
					s.hits.Add(1)
				}
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					ok.Add(1)
				} else {
					fail.Add(1)
				}
			}
		}(i)
	}
	wg.Wait()

	s.mu.Lock()
	s.status.Running = false
	s.status.Requests = sent.Load()
	s.status.Successes = ok.Load()
	s.status.Failures = fail.Load()
	s.status.FinishedAt = time.Now()
	s.mu.Unlock()
}
