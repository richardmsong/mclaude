package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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

// handleAdminUsers routes POST/DELETE/GET /admin/users.
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
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
	// Break-glass: no DB configured → return 202 with warning.
	if s.db == nil {
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"status":  "accepted",
			"warning": "database not configured; stop not persisted",
		})
		return
	}
	// Note: the authoritative stop path is via NATS. This break-glass endpoint
	// records a stop intent in the DB. The session agent will detect the stopped
	// state on next heartbeat. For immediate effect, the operator should also
	// send a SIGTERM to the session pod.
	_, err := s.db.pool.Exec(context.Background(),
		`UPDATE sessions SET status = 'stopped', stopped_at = NOW()
		 WHERE user_id = $1 AND project_id = $2 AND session_id = $3`,
		req.UserID, req.ProjectID, req.SessionID)
	if err != nil {
		// sessions table may not exist yet — return 202 (best-effort break-glass)
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
