package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration, sourced from environment variables.
type Config struct {
	// Server
	Port        string
	InstanceID  string
	ReadTimeout time.Duration
	// Cache
	CacheEnabled     bool
	DefaultCacheTTL  time.Duration
	CacheMaxEntries  int
	// Database
	DatabaseURL string // empty => in-memory dev store
	DBMaxConns  int
	// Simulation
	SimTargetURL string // base URL the simulator fires requests at (e.g. http://nginx)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func Load() Config {
	port := env("PORT", "8080")
	return Config{
		Port:            port,
		InstanceID:      env("INSTANCE_ID", "backend-"+port),
		ReadTimeout:     envDur("READ_TIMEOUT", 10*time.Second),
		CacheEnabled:    envBool("CACHE_ENABLED", true),
		DefaultCacheTTL: envDur("CACHE_TTL", 2*time.Second),
		CacheMaxEntries: envInt("CACHE_MAX_ENTRIES", 10000),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		DBMaxConns:      envInt("DB_MAX_CONNS", 25),
		SimTargetURL:    env("SIM_TARGET_URL", "http://127.0.0.1:"+port),
	}
}
