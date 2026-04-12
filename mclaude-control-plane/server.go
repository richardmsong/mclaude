package main

import (
	"net/http"
)

// RegisterRoutes wires all HTTP handlers onto the given mux.
// Public routes (no auth): /auth/login, /auth/refresh, /version, /health
// Protected routes (NATS JWT auth): /auth/me
// Admin routes (admin bearer token): /admin/* on separate mux (see adminMux)
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Public
	mux.HandleFunc("/auth/login", s.handleLogin)
	mux.HandleFunc("/auth/refresh", s.handleRefresh)
	mux.HandleFunc("/version", handleVersion)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Health probes (Kubernetes liveness + readiness)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Protected
	mux.Handle("/auth/me", s.authMiddleware(http.HandlerFunc(s.handleMe)))
}

// AdminMux returns an http.ServeMux for the break-glass admin port (:9091).
// All routes require the static admin bearer token (env ADMIN_TOKEN).
// This port must not be exposed externally — bind to 127.0.0.1 in production.
func (s *Server) AdminMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/admin/", s.adminAuthMiddleware(http.HandlerFunc(s.handleAdminUsers)))
	mux.HandleFunc("/metrics", s.handleMetrics)
	return mux
}

// adminAuthMiddleware enforces the static admin bearer token on admin routes.
func (s *Server) adminAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" || token != s.adminToken {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleMetrics serves Prometheus metrics from MetricsRegistry.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	metricsHandler().ServeHTTP(w, r)
}
