// Package middleware provides HTTP cross-cutting concerns: request metrics,
// backend instance identification, and access logging.
package middleware

import (
	"log"
	"net/http"
	"time"

	"icl/internal/metrics"
)

// InstanceHeader names the response header carrying the backend instance ID
// that served the request (used to verify load-balancer distribution).
const InstanceHeader = "X-Backend-Instance"

// Metrics records latency, throughput, active requests, and errors.
func Metrics(m *metrics.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.RequestsTotal.Add(1)
			m.RequestsActive.Add(1)
			defer m.RequestsActive.Add(-1)

			start := time.Now()
			// Wrap the writer to capture status code.
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			lat := time.Since(start)
			m.ObserveLatency(lat)
			if sw.status >= 500 {
				m.ErrorsTotal.Add(1)
			}
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Instance stamps every response with the backend instance that served it.
func Instance(id string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(InstanceHeader, id)
			next.ServeHTTP(w, r)
		})
	}
}

// Logging emits one access-log line per request.
func Logging() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start))
		})
	}
}
