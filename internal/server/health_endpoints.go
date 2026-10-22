package server

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"time"
)

func (s *Server) handleLiveness(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ready(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

// handleMetrics exposes only process-level, non-identifying Prometheus data.
// Device IDs, SIM identities, phone numbers and proxy information never enter
// this unauthenticated endpoint.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ready := 0
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	if s.store.Ready(ctx) == nil {
		ready = 1
	}
	cancel()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, "# HELP vonet_up Whether the process is running.\n# TYPE vonet_up gauge\nvonet_up 1\n")
	fmt.Fprintf(w, "# HELP vonet_ready Whether the database is ready.\n# TYPE vonet_ready gauge\nvonet_ready %d\n", ready)
	fmt.Fprintf(w, "# HELP vonet_uptime_seconds Process uptime.\n# TYPE vonet_uptime_seconds counter\nvonet_uptime_seconds %.0f\n", time.Since(s.startedAt).Seconds())
	fmt.Fprintf(w, "# HELP vonet_go_goroutines Current Go goroutines.\n# TYPE vonet_go_goroutines gauge\nvonet_go_goroutines %d\n", runtime.NumGoroutine())
}
