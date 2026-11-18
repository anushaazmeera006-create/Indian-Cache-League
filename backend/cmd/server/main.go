// ICL backend server: in-memory cache + singleflight + PostgreSQL + metrics.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"icl/internal/api"
	"icl/internal/config"
	"icl/internal/database"
	"icl/internal/metrics"
	"icl/internal/middleware"
	"icl/internal/simulation"
)

func main() {
	cfg := config.Load()
	m := metrics.New()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The database layer reports every real query to the metrics registry.
	store, err := database.New(ctx, cfg.DatabaseURL, cfg.DBMaxConns, m.RecordDBQuery)
	if err != nil {
		log.Fatalf("database init failed: %v", err)
	}
	defer store.Close()
	if cfg.DatabaseURL == "" {
		log.Println("DATABASE_URL not set — using in-memory dev store (no PostgreSQL)")
	} else {
		log.Println("connected to PostgreSQL")
	}

	app := api.NewApp(cfg, store, m, simulation.New())

	mux := http.NewServeMux()
	// Cricket data plane
	mux.HandleFunc("GET /api/matches", app.ListMatches)
	mux.HandleFunc("GET /api/matches/{id}", app.GetMatch)
	mux.HandleFunc("GET /api/matches/{id}/score", app.GetScore)
	mux.HandleFunc("PUT /api/matches/{id}/score", app.UpdateScore)
	mux.HandleFunc("GET /api/leaderboard", app.Leaderboard)
	// Ops plane
	mux.HandleFunc("GET /api/health", app.Health)
	mux.HandleFunc("GET /api/stats", app.Stats)
	mux.HandleFunc("GET /api/cache/stats", app.CacheStats)
	mux.HandleFunc("POST /api/cache/invalidate/{id}", app.InvalidateCache)
	// Simulation plane
	mux.HandleFunc("POST /api/simulation/start", app.SimStart)
	mux.HandleFunc("POST /api/simulation/stop", app.SimStop)
	mux.HandleFunc("GET /api/simulation/status", app.SimStatus)
	mux.HandleFunc("POST /api/simulation/spike", app.SimSpike)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"service":"ICL backend","instance":"` + cfg.InstanceID + `"}`))
	})

	handler := middleware.Logging()(middleware.Instance(cfg.InstanceID)(middleware.Metrics(m)(mux)))

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		log.Printf("ICL backend %s listening on :%s (cache_ttl=%s enabled=%v)",
			cfg.InstanceID, cfg.Port, cfg.DefaultCacheTTL, cfg.CacheEnabled)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}
	log.Println("stopped")
}
