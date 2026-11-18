// Package metrics collects real instrumentation counters and latency
// distributions for the monitoring dashboard.
package metrics

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const latencyWindow = 20000 // keep last N latencies for percentile math

// Metrics aggregates all backend instrumentation.
type Metrics struct {
	// HTTP layer
	RequestsTotal   atomic.Int64
	RequestsActive  atomic.Int64
	ErrorsTotal     atomic.Int64

	// Database layer (incremented via Store QueryFunc)
	DBQueries       atomic.Int64
	DBQueryNanos    atomic.Int64 // sum of query durations for avg
	DBErrors        atomic.Int64

	// Singleflight
	Coalesced       atomic.Int64 // requests that shared an in-flight call
	DBSaved         atomic.Int64 // db queries avoided by coalescing

	mu       sync.Mutex
	latencies []time.Duration // ring-ish: we just keep last N via slice trim
}

func New() *Metrics { return &Metrics{} }

// ObserveLatency records one HTTP request latency.
func (m *Metrics) ObserveLatency(d time.Duration) {
	m.mu.Lock()
	m.latencies = append(m.latencies, d)
	if len(m.latencies) > latencyWindow {
		// drop oldest quarter to amortize cost
		m.latencies = m.latencies[len(m.latencies)/4:]
	}
	m.mu.Unlock()
}

// RecordDBQuery records a real database query. Matches database.QueryFunc.
func (m *Metrics) RecordDBQuery(_ string, d time.Duration, err error) {
	m.DBQueries.Add(1)
	m.DBQueryNanos.Add(d.Nanoseconds())
	if err != nil {
		m.DBErrors.Add(1)
	}
}

// Percentiles returns p50/p95/p99/avg over the retained latency window.
func (m *Metrics) Percentiles() (p50, p95, p99, avg time.Duration) {
	m.mu.Lock()
	n := len(m.latencies)
	if n == 0 {
		m.mu.Unlock()
		return 0, 0, 0, 0
	}
	sorted := make([]time.Duration, n)
	copy(sorted, m.latencies)
	m.mu.Unlock()
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var sum time.Duration
	for _, d := range sorted {
		sum += d
	}
	avg = sum / time.Duration(n)
	p50 = sorted[int(float64(n)*0.50)]
	p95 = sorted[int(float64(n)*0.95)]
	if int(float64(n)*0.99) >= n {
		p99 = sorted[n-1]
	} else {
		p99 = sorted[int(float64(n)*0.99)]
	}
	return
}

// Snapshot is the JSON-friendly metrics view.
type Snapshot struct {
	RequestsTotal    int64   `json:"requests_total"`
	RequestsActive   int64   `json:"requests_active"`
	ErrorRate        float64 `json:"error_rate"`
	RequestsPerSec   float64 `json:"requests_per_sec_estimate"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	P50LatencyMs     float64 `json:"p50_latency_ms"`
	P95LatencyMs     float64 `json:"p95_latency_ms"`
	P99LatencyMs     float64 `json:"p99_latency_ms"`
	DBQueries        int64   `json:"db_queries"`
	DBQueriesPerSec  float64 `json:"db_queries_per_sec_estimate"`
	DBAvgLatencyMs   float64 `json:"db_avg_latency_ms"`
	DBErrors         int64   `json:"db_errors"`
	Coalesced        int64   `json:"coalesced_requests"`
	DBSaved          int64   `json:"db_queries_saved"`
	SingleflightEff  float64 `json:"singleflight_efficiency"`
	UptimeSeconds    float64 `json:"uptime_seconds"`
	Cache            interface{} `json:"cache,omitempty"`
}

func (m *Metrics) Snapshot(startTime time.Time) Snapshot {
	p50, p95, p99, avg := m.Percentiles()
	dbQ := m.DBQueries.Load()
	dbNanos := m.DBQueryNanos.Load()
	dbAvg := time.Duration(0)
	if dbQ > 0 {
		dbAvg = time.Duration(dbNanos / dbQ)
	}
	reqs := m.RequestsTotal.Load()
	errs := m.ErrorsTotal.Load()
	errRate := 0.0
	if reqs > 0 {
		errRate = float64(errs) / float64(reqs)
	}
	var eff float64
	if m.Coalesced.Load() > 0 {
		eff = float64(m.DBSaved.Load()) / float64(m.Coalesced.Load())
	}
	uptime := time.Since(startTime).Seconds()
	var rps, dbQps float64
	if uptime > 0 {
		rps = float64(reqs) / uptime
		dbQps = float64(dbQ) / uptime
	}
	return Snapshot{
		RequestsTotal:   reqs,
		RequestsActive:  m.RequestsActive.Load(),
		RequestsPerSec:  rps,
		ErrorRate:       errRate,
		AvgLatencyMs:    float64(avg.Milliseconds()),
		P50LatencyMs:    float64(p50.Milliseconds()),
		P95LatencyMs:    float64(p95.Milliseconds()),
		P99LatencyMs:    float64(p99.Milliseconds()),
		DBQueries:       dbQ,
		DBQueriesPerSec: dbQps,
		DBAvgLatencyMs:  float64(dbAvg.Milliseconds()),
		DBErrors:        m.DBErrors.Load(),
		Coalesced:       m.Coalesced.Load(),
		DBSaved:         m.DBSaved.Load(),
		SingleflightEff: eff,
		UptimeSeconds:   uptime,
	}
}
