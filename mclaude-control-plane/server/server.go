package server

import (
	"net/http"
	"strings"
)

// RegisterRoutes wires all HTTP handlers onto the given mux.
// Public routes (no auth): /auth/login, /auth/refresh, /version, /health
// Protected routes (NATS JWT auth): /auth/me, /api/*
// OAuth callback (no auth — redirects): /auth/providers/*/callback
// Admin routes (isAdminMiddleware — JWT + is_admin=true): /admin/*, /scim/v2/
// Metrics (unauthenticated): /metrics
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Public
	mux.HandleFunc("/auth/login", s.handleLogin)
	mux.HandleFunc("/auth/refresh", s.handleRefresh)
	mux.HandleFunc("/version", handleVersion)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// ADR-0054: NKey challenge-response auth endpoints (all identity types).
	mux.HandleFunc("/api/auth/challenge", s.handleAuthChallenge)
	mux.HandleFunc("/api/auth/verify", s.handleAuthVerify)

	// ADR-0054: CLI device-code auth flow.
	mux.HandleFunc("/api/auth/device-code", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.handleCLIDeviceCodeCreate(w, r)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/api/auth/device-code/poll", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.handleCLIDeviceCodePoll(w, r)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/api/auth/device-code/verify", s.handleCLIDeviceCodeVerify)

	// Health probes (Kubernetes liveness + readiness)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.db == nil {
			http.Error(w, "database not configured", http.StatusServiceUnavailable)
			return
		}
		if err := s.db.Pool().Ping(r.Context()); err != nil {
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

	// ADR-0053: Attachment endpoints (POST upload-url, POST confirm, GET download).
	mux.Handle("/api/attachments/", s.authMiddleware(http.HandlerFunc(s.handleAttachmentRoutes)))
	mux.Handle("/api/attachments", s.authMiddleware(http.HandlerFunc(s.handleAttachmentRoutes)))

	// Host and Project CRUD endpoints (ADR-0035)
	mux.Handle("/api/users/", s.authMiddleware(http.HandlerFunc(s.handleUserAPIRoutes)))

	// Device-code registration (ADR-0035)
	mux.HandleFunc("/api/hosts/register", s.handleHostRegister)

	// Admin routes — on main port, behind isAdminMiddleware (JWT + is_admin=true in DB).
	// Returns 401 if no JWT, 403 if JWT valid but user is not admin.
	mux.Handle("/admin/", s.isAdminMiddleware(http.HandlerFunc(s.handleAdminRoutes)))

	// SCIM 2.0 endpoints — on main port, behind isAdminMiddleware.
	mux.Handle("/scim/v2/", s.isAdminMiddleware(http.HandlerFunc(s.handleSCIMRoutes)))

	// Prometheus metrics — unauthenticated on main port.
	// Acceptable since the port is behind the cluster ingress (not externally reachable without auth).
	mux.HandleFunc("/metrics", s.handleMetrics)
}

// isAdminMiddleware enforces admin-only access on protected routes.
// It calls authMiddleware first (parses + validates JWT, injects userID into context),
// then fetches the user from Postgres and checks is_admin == true.
// Returns 401 if no JWT, 403 if JWT valid but user is not admin.
func (s *Server) isAdminMiddleware(next http.Handler) http.Handler {
	return s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := r.Context().Value(contextKeyUserID).(string)
		if !ok || userID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if s.db == nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		user, err := s.db.GetUserByID(r.Context(), userID)
		if err != nil || user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !user.IsAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// handleMetrics serves Prometheus metrics from MetricsRegistry.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	metricsHandler().ServeHTTP(w, r)
}
