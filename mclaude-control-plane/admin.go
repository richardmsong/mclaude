package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Admin endpoints are served on the break-glass admin port (:9091).
// They operate entirely through Postgres — no NATS required — so they work
// even when NATS is down. All endpoints require the admin bearer token.
//
// Routes (all under /admin/):
//
//	POST   /admin/users          — create user
//	DELETE /admin/users/{id}     — delete user
//	GET    /admin/users          — list all users
//	POST   /admin/sessions/stop  — stop a session (stub — real stop via NATS)
//	POST   /admin/clusters       — register a cluster (ADR-0035)
//	GET    /admin/clusters       — list clusters (ADR-0035)
//	POST   /admin/clusters/{cslug}/grants — grant user access to cluster (ADR-0035)

// AdminUserRequest is the body for POST /admin/users.
type AdminUserRequest struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"` // optional; empty = SSO-only account
}

// AdminUserResponse is a single user entry in list/create responses.
type AdminUserResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// AdminSessionStopRequest is the body for POST /admin/sessions/stop.
type AdminSessionStopRequest struct {
	UserID    string `json:"userId"`
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId"`
}

// AdminClusterRegisterRequest is the body for POST /admin/clusters (ADR-0035).
type AdminClusterRegisterRequest struct {
	Slug         string `json:"slug"`
	Name         string `json:"name,omitempty"`
	JsDomain     string `json:"jsDomain"`
	LeafURL      string `json:"leafUrl"`
	DirectNATSURL string `json:"directNatsUrl,omitempty"`
}

// AdminClusterRegisterResponse is returned on successful cluster registration.
type AdminClusterRegisterResponse struct {
	Slug         string `json:"slug"`
	LeafJWT      string `json:"leafJwt,omitempty"`
	LeafSeed     string `json:"leafSeed,omitempty"`
	AccountJWT   string `json:"accountJwt,omitempty"`
	OperatorJWT  string `json:"operatorJwt,omitempty"`
	JsDomain     string `json:"jsDomain"`
	DirectNATSURL string `json:"directNatsUrl,omitempty"`
}

// AdminClusterGrantRequest is the body for POST /admin/clusters/{cslug}/grants.
type AdminClusterGrantRequest struct {
	UserSlug string `json:"userSlug"`
}

// handleAdminRoutes routes all /admin/* requests.
func (s *Server) handleAdminRoutes(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/admin/users":
		s.adminListUsers(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/users":
		s.adminCreateUser(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/admin/users/"):
		id := strings.TrimPrefix(r.URL.Path, "/admin/users/")
		s.adminDeleteUser(w, r, id)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/sessions/stop":
		s.adminStopSession(w, r)
	// ADR-0035 cluster endpoints
	case r.Method == http.MethodPost && r.URL.Path == "/admin/clusters":
		s.adminRegisterCluster(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/clusters":
		s.adminListClusters(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/admin/clusters/") && strings.HasSuffix(r.URL.Path, "/grants"):
		cslug := strings.TrimPrefix(r.URL.Path, "/admin/clusters/")
		cslug = strings.TrimSuffix(cslug, "/grants")
		s.adminGrantCluster(w, r, cslug)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) adminListUsers(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}
	rows, err := s.db.pool.Query(r.Context(),
		`SELECT id, email, name FROM users ORDER BY created_at`)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var users []AdminUserResponse
	for rows.Next() {
		var u AdminUserResponse
		if err := rows.Scan(&u.ID, &u.Email, &u.Name); err != nil {
			http.Error(w, "scan error", http.StatusInternalServerError)
			return
		}
		users = append(users, u)
	}
	if users == nil {
		users = []AdminUserResponse{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users) //nolint:errcheck
}

func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req AdminUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.ID == "" || req.Email == "" || req.Name == "" {
		http.Error(w, "id, email, and name are required", http.StatusBadRequest)
		return
	}
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	var passwordHash string
	if req.Password != "" {
		var err error
		passwordHash, err = HashPassword(req.Password)
		if err != nil {
			http.Error(w, "failed to hash password", http.StatusInternalServerError)
			return
		}
	}

	user, err := s.db.CreateUser(r.Context(), req.ID, req.Email, req.Name, passwordHash)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			http.Error(w, "email already exists", http.StatusConflict)
			return
		}
		http.Error(w, "create user error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(AdminUserResponse{ //nolint:errcheck
		ID:    user.ID,
		Email: user.Email,
		Name:  user.Name,
	})
}

func (s *Server) adminDeleteUser(w http.ResponseWriter, r *http.Request, userID string) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}
	if userID == "" {
		http.Error(w, "user id required", http.StatusBadRequest)
		return
	}
	if err := s.db.DeleteUser(r.Context(), userID); err != nil {
		http.Error(w, "delete error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminStopSession(w http.ResponseWriter, r *http.Request) {
	var req AdminSessionStopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.UserID == "" || req.ProjectID == "" || req.SessionID == "" {
		http.Error(w, "userId, projectId, and sessionId are required", http.StatusBadRequest)
		return
	}
	if s.db == nil {
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"status":  "accepted",
			"warning": "database not configured; stop not persisted",
		})
		return
	}
	_, err := s.db.pool.Exec(context.Background(),
		`UPDATE sessions SET status = 'stopped', stopped_at = NOW()
		 WHERE user_id = $1 AND project_id = $2 AND session_id = $3`,
		req.UserID, req.ProjectID, req.SessionID)
	if err != nil {
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"status":  "accepted",
			"warning": "NATS stop not sent; session table may not exist",
		})
		return
	}
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "accepted"}) //nolint:errcheck
}

// adminRegisterCluster handles POST /admin/clusters (ADR-0035).
// Creates a cluster host row for the admin user and returns credentials.
func (s *Server) adminRegisterCluster(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	var req AdminClusterRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Slug == "" || req.JsDomain == "" || req.LeafURL == "" {
		http.Error(w, "slug, jsDomain, and leafUrl are required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = req.Slug
	}

	// Generate a per-cluster NKey pair for the controller/leaf-node.
	clusterNKey, _, err := GenerateUserNKey()
	if err != nil {
		http.Error(w, "failed to generate cluster nkey", http.StatusInternalServerError)
		return
	}

	// Issue a cluster controller JWT scoped to mclaude.users.*.hosts.{cslug}.>
	clusterJWT, clusterSeed, err := IssueHostJWT("*", req.Slug, s.accountKP)
	if err != nil {
		http.Error(w, "failed to issue cluster jwt", http.StatusInternalServerError)
		return
	}

	// TODO: In production, look up admin user from bearer token.
	// For now, create the host row without a specific admin user binding.
	// The admin user's row is created by the caller (e.g., CLI's mclaude cluster register).
	hostID := uuid.NewString()
	_, err = s.db.pool.Exec(r.Context(), `
		INSERT INTO hosts (id, user_id, slug, name, type, role, js_domain, leaf_url, account_jwt, direct_nats_url, public_key, user_jwt)
		VALUES ($1, (SELECT id FROM users LIMIT 1), $2, $3, 'cluster', 'owner', $4, $5, '', $6, $7, $8)`,
		hostID, req.Slug, req.Name, req.JsDomain, req.LeafURL, req.DirectNATSURL, clusterNKey.PublicKey, clusterJWT)
	if err != nil {
		log.Error().Err(err).Msg("create cluster host row")
		http.Error(w, "failed to create cluster", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(AdminClusterRegisterResponse{ //nolint:errcheck
		Slug:         req.Slug,
		LeafJWT:      clusterJWT,
		LeafSeed:     string(clusterSeed),
		JsDomain:     req.JsDomain,
		DirectNATSURL: req.DirectNATSURL,
	})
}

// adminListClusters handles GET /admin/clusters (ADR-0035).
func (s *Server) adminListClusters(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	rows, err := s.db.pool.Query(r.Context(),
		`SELECT DISTINCT slug, name, js_domain, leaf_url, direct_nats_url
		 FROM hosts WHERE type = 'cluster' ORDER BY slug`)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type clusterEntry struct {
		Slug         string  `json:"slug"`
		Name         string  `json:"name"`
		JsDomain     *string `json:"jsDomain"`
		LeafURL      *string `json:"leafUrl"`
		DirectNATSURL *string `json:"directNatsUrl"`
	}
	var clusters []clusterEntry
	for rows.Next() {
		var c clusterEntry
		if err := rows.Scan(&c.Slug, &c.Name, &c.JsDomain, &c.LeafURL, &c.DirectNATSURL); err != nil {
			http.Error(w, "scan error", http.StatusInternalServerError)
			return
		}
		clusters = append(clusters, c)
	}
	if clusters == nil {
		clusters = []clusterEntry{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(clusters) //nolint:errcheck
}

// adminGrantCluster handles POST /admin/clusters/{cslug}/grants (ADR-0035).
// Creates a hosts row for the granted user with the cluster's shared fields.
func (s *Server) adminGrantCluster(w http.ResponseWriter, r *http.Request, cslug string) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	var req AdminClusterGrantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.UserSlug == "" {
		http.Error(w, "userSlug is required", http.StatusBadRequest)
		return
	}

	// Look up the cluster's shared fields from an existing host row.
	var jsDomain, leafURL, accountJWT, directNATSURL, publicKey *string
	err := s.db.pool.QueryRow(r.Context(), `
		SELECT js_domain, leaf_url, account_jwt, direct_nats_url, public_key
		FROM hosts WHERE slug = $1 AND type = 'cluster' LIMIT 1`, cslug).
		Scan(&jsDomain, &leafURL, &accountJWT, &directNATSURL, &publicKey)
	if err != nil {
		http.Error(w, "cluster not found", http.StatusNotFound)
		return
	}

	// Find the target user by email or slug-like identifier.
	// For simplicity, look up by matching the user slug pattern in the email.
	user, err := s.db.GetUserByEmail(r.Context(), req.UserSlug)
	if err != nil || user == nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	// Issue a per-user JWT for this user scoped to mclaude.users.{uslug}.hosts.{cslug}.>
	userJWT, _, err := IssueHostJWT(user.ID, cslug, s.accountKP)
	if err != nil {
		http.Error(w, "failed to issue user jwt", http.StatusInternalServerError)
		return
	}

	hostID := uuid.NewString()
	_, err = s.db.pool.Exec(r.Context(), `
		INSERT INTO hosts (id, user_id, slug, name, type, role, js_domain, leaf_url, account_jwt, direct_nats_url, public_key, user_jwt)
		VALUES ($1, $2, $3, $4, 'cluster', 'user', $5, $6, $7, $8, $9, $10)
		ON CONFLICT (user_id, slug) DO NOTHING`,
		hostID, user.ID, cslug, cslug, jsDomain, leafURL, accountJWT, directNATSURL, publicKey, userJWT)
	if err != nil {
		http.Error(w, "failed to grant cluster access", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "granted"}) //nolint:errcheck
}
