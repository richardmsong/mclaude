package main

import (
	"net/http"
	"strings"
)

// RegisterRoutes wires all HTTP handlers onto the given mux.
// Public routes (no auth): /auth/login, /auth/refresh, /version, /health
// Protected routes (NATS JWT auth): /auth/me, /api/*
// OAuth callback (no auth — redirects): /auth/providers/*/callback
// Admin routes (admin bearer token): /admin/* on separate mux (see AdminMux)
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
		if s.db == nil {
			http.Error(w, "database not configured", http.StatusServiceUnavailable)
			return
		}
		if err := s.db.pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// OAuth callbacks (no auth — browser redirect, state validates the request)
	mux.HandleFunc("/auth/providers/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/callback") {
			s.handleOAuthCallback(w, r)
			return
		}
		http.NotFound(w, r)
	})

	// Protected
	mux.Handle("/auth/me", s.authMiddleware(http.HandlerFunc(s.handleMe)))

	// Protected API routes
	mux.Handle("/api/providers", s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.handleGetProviders(w, r)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})))

	// POST /api/providers/pat (must be registered before /api/providers/{id}/connect)
	mux.Handle("/api/providers/pat", s.authMiddleware(http.HandlerFunc(s.handleAddPAT)))

	// POST /api/providers/{id}/connect
	mux.Handle("/api/providers/", s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/providers/")
		if strings.HasSuffix(path, "/connect") {
			s.handleConnectProvider(w, r)
			return
		}
		http.NotFound(w, r)
	})))

	// GET /api/connections/{connection_id}/repos
	// DELETE /api/connections/{connection_id}
	mux.Handle("/api/connections/", s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/connections/")
		if strings.HasSuffix(path, "/repos") && r.Method == http.MethodGet {
			s.handleGetConnectionRepos(w, r)
			return
		}
		if r.Method == http.MethodDelete {
			s.handleDeleteConnection(w, r)
			return
		}
		http.NotFound(w, r)
	})))

	// PATCH /api/projects/{project_id}
	mux.Handle("/api/projects/", s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			s.handlePatchProject(w, r)
			return
		}
		http.NotFound(w, r)
	})))

	// Host CRUD endpoints (ADR-0035)
	mux.Handle("/api/users/", s.authMiddleware(http.HandlerFunc(s.handleHostRoutes)))

	// Device-code registration (ADR-0035)
	mux.HandleFunc("/api/hosts/register", s.handleHostRegister)
}

// AdminMux returns an http.ServeMux for the break-glass admin port (:9091).
// All routes require the static admin bearer token (env ADMIN_TOKEN).
// This port must not be exposed externally — bind to 127.0.0.1 in production.
func (s *Server) AdminMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/admin/", s.adminAuthMiddleware(http.HandlerFunc(s.handleAdminRoutes)))
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
