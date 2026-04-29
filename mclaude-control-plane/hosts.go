package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// HostResponse is a single host entry in list/CRUD responses.
type HostResponse struct {
	ID            string     `json:"id"`
	Slug          string     `json:"slug"`
	Name          string     `json:"name"`
	Type          string     `json:"type"`
	Role          string     `json:"role"`
	JsDomain      *string    `json:"jsDomain,omitempty"`
	DirectNATSURL *string    `json:"directNatsUrl,omitempty"`
	LastSeenAt    *time.Time `json:"lastSeenAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// HostCreateRequest is the body for POST /api/users/{uslug}/hosts.
type HostCreateRequest struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	PublicKey string `json:"publicKey,omitempty"`
}

// HostUpdateRequest is the body for PUT /api/users/{uslug}/hosts/{hslug}.
type HostUpdateRequest struct {
	Name string `json:"name"`
}

// DeviceCodeRequest is the body for POST /api/users/{uslug}/hosts/code.
type DeviceCodeRequest struct {
	PublicKey string `json:"publicKey"`
}

// DeviceCodeResponse is returned for POST /api/users/{uslug}/hosts/code.
type DeviceCodeResponse struct {
	Code      string `json:"code"`
	ExpiresAt int64  `json:"expiresAt"`
}

// DeviceCodeStatusResponse is returned for GET /api/users/{uslug}/hosts/code/{code}.
type DeviceCodeStatusResponse struct {
	Status string  `json:"status"` // "pending" or "completed"
	Slug   string  `json:"slug,omitempty"`
	JWT    string  `json:"jwt,omitempty"`
	HubURL string  `json:"hubUrl,omitempty"`
}

// HostRegisterRequest is the body for POST /api/hosts/register.
type HostRegisterRequest struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// HostRegisterResponse is returned for POST /api/hosts/register.
type HostRegisterResponse struct {
	Slug   string `json:"slug"`
	JWT    string `json:"jwt"`
	HubURL string `json:"hubUrl"`
}

// deviceCodeEntry stores a pending device code for host registration.
type deviceCodeEntry struct {
	UserID    string
	PublicKey string
	ExpiresAt time.Time
	// Completed state — set when the code is redeemed via POST /api/hosts/register.
	Completed bool
	Slug      string
	JWT       string
}

// deviceCodeStore is an in-memory store for device codes.
// In production, use a database or distributed cache with TTL.
type deviceCodeStore struct {
	mu    sync.RWMutex
	codes map[string]*deviceCodeEntry
}

var globalDeviceCodeStore = &deviceCodeStore{
	codes: make(map[string]*deviceCodeEntry),
}

// generateDeviceCode creates a 6-character hex device code.
func generateDeviceCode() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(b)), nil
}

// handleHostRoutes dispatches /api/users/{uslug}/hosts/* requests.
func (s *Server) handleHostRoutes(w http.ResponseWriter, r *http.Request) {
	// Parse: /api/users/{uslug}/hosts[/{hslug}]
	path := strings.TrimPrefix(r.URL.Path, "/api/users/")
	parts := strings.SplitN(path, "/", 4) // [uslug, "hosts", hslug?, ...]

	if len(parts) < 2 || parts[1] != "hosts" {
		http.NotFound(w, r)
		return
	}

	userID, ok := r.Context().Value(contextKeyUserID).(string)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// /api/users/{uslug}/hosts/code or /api/users/{uslug}/hosts/code/{code}
	if len(parts) >= 3 && parts[2] == "code" {
		if r.Method == http.MethodPost && len(parts) == 3 {
			s.handleHostCodeCreate(w, r, userID)
			return
		}
		if r.Method == http.MethodGet && len(parts) == 4 {
			s.handleHostCodeStatus(w, r, userID, parts[3])
			return
		}
		http.NotFound(w, r)
		return
	}

	switch {
	case r.Method == http.MethodGet && len(parts) == 2:
		// GET /api/users/{uslug}/hosts
		s.handleListHosts(w, r, userID)
	case r.Method == http.MethodPost && len(parts) == 2:
		// POST /api/users/{uslug}/hosts
		s.handleCreateHost(w, r, userID)
	case r.Method == http.MethodPut && len(parts) >= 3:
		// PUT /api/users/{uslug}/hosts/{hslug}
		s.handleUpdateHost(w, r, userID, parts[2])
	case r.Method == http.MethodDelete && len(parts) >= 3:
		// DELETE /api/users/{uslug}/hosts/{hslug}
		s.handleDeleteHost(w, r, userID, parts[2])
	default:
		http.NotFound(w, r)
	}
}

// handleListHosts handles GET /api/users/{uslug}/hosts.
func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request, userID string) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	rows, err := s.db.pool.Query(r.Context(), `
		SELECT id, slug, name, type, role, js_domain, direct_nats_url, last_seen_at, created_at
		FROM hosts WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var hosts []HostResponse
	for rows.Next() {
		var h HostResponse
		if err := rows.Scan(&h.ID, &h.Slug, &h.Name, &h.Type, &h.Role, &h.JsDomain, &h.DirectNATSURL, &h.LastSeenAt, &h.CreatedAt); err != nil {
			http.Error(w, "scan error", http.StatusInternalServerError)
			return
		}
		hosts = append(hosts, h)
	}
	if hosts == nil {
		hosts = []HostResponse{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(hosts) //nolint:errcheck
}

// handleCreateHost handles POST /api/users/{uslug}/hosts.
func (s *Server) handleCreateHost(w http.ResponseWriter, r *http.Request, userID string) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	var req HostCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Slug == "" || req.Name == "" {
		http.Error(w, "slug and name are required", http.StatusBadRequest)
		return
	}

	// Issue a per-host user JWT.
	hostJWT, _, err := IssueHostJWT(userID, req.Slug, s.accountKP)
	if err != nil {
		http.Error(w, "failed to issue host jwt", http.StatusInternalServerError)
		return
	}

	hostID := uuid.NewString()
	_, err = s.db.pool.Exec(r.Context(), `
		INSERT INTO hosts (id, user_id, slug, name, type, role, public_key, user_jwt)
		VALUES ($1, $2, $3, $4, 'machine', 'owner', $5, $6)`,
		hostID, userID, req.Slug, req.Name, req.PublicKey, hostJWT)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			http.Error(w, "host slug already exists", http.StatusConflict)
			return
		}
		http.Error(w, "failed to create host", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(HostResponse{ //nolint:errcheck
		ID:        hostID,
		Slug:      req.Slug,
		Name:      req.Name,
		Type:      "machine",
		Role:      "owner",
		CreatedAt: time.Now().UTC(),
	})
}

// handleUpdateHost handles PUT /api/users/{uslug}/hosts/{hslug}.
func (s *Server) handleUpdateHost(w http.ResponseWriter, r *http.Request, userID, hslug string) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	var req HostUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	tag, err := s.db.pool.Exec(r.Context(), `
		UPDATE hosts SET name = $1 WHERE user_id = $2 AND slug = $3`,
		req.Name, userID, hslug)
	if err != nil {
		http.Error(w, "update error", http.StatusInternalServerError)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"}) //nolint:errcheck
}

// handleDeleteHost handles DELETE /api/users/{uslug}/hosts/{hslug}.
func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request, userID, hslug string) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	// GAP-CP-04: Before deleting the host (which cascades to projects),
	// publish delete notifications for each project on this host so controllers
	// can tear down per-project resources (namespaces, Deployments, PVCs, RBAC).
	if s.nc != nil {
		user, userErr := s.db.GetUserByID(r.Context(), userID)
		if userErr == nil && user != nil {
			projects, projErr := s.db.GetProjectsByHostSlug(r.Context(), userID, hslug)
			if projErr == nil {
				for _, p := range projects {
					publishProjectsDeleteToHost(s.nc, user.Slug, hslug, p.ID)
				}
			}
			// Broadcast user-level projects.updated so SPA refreshes.
			publishProjectsUpdated(s.nc, user.Slug)
		}
	}

	tag, err := s.db.pool.Exec(r.Context(), `
		DELETE FROM hosts WHERE user_id = $1 AND slug = $2`,
		userID, hslug)
	if err != nil {
		http.Error(w, "delete error", http.StatusInternalServerError)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleHostCodeCreate handles POST /api/users/{uslug}/hosts/code (ADR-0035 device-code flow).
func (s *Server) handleHostCodeCreate(w http.ResponseWriter, r *http.Request, userID string) {
	var req DeviceCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.PublicKey == "" {
		http.Error(w, "publicKey is required", http.StatusBadRequest)
		return
	}

	code, err := generateDeviceCode()
	if err != nil {
		http.Error(w, "failed to generate code", http.StatusInternalServerError)
		return
	}

	expiresAt := time.Now().Add(10 * time.Minute)

	globalDeviceCodeStore.mu.Lock()
	globalDeviceCodeStore.codes[code] = &deviceCodeEntry{
		UserID:    userID,
		PublicKey: req.PublicKey,
		ExpiresAt: expiresAt,
	}
	globalDeviceCodeStore.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(DeviceCodeResponse{ //nolint:errcheck
		Code:      code,
		ExpiresAt: expiresAt.Unix(),
	})
}

// handleHostCodeStatus handles GET /api/users/{uslug}/hosts/code/{code}.
// CLI polls this until status changes from "pending" to "completed".
func (s *Server) handleHostCodeStatus(w http.ResponseWriter, r *http.Request, userID, code string) {
	globalDeviceCodeStore.mu.RLock()
	entry, exists := globalDeviceCodeStore.codes[code]
	globalDeviceCodeStore.mu.RUnlock()

	if !exists {
		http.Error(w, "code not found", http.StatusNotFound)
		return
	}
	if time.Now().After(entry.ExpiresAt) {
		http.Error(w, "code expired", http.StatusGone)
		return
	}
	if entry.UserID != userID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	resp := DeviceCodeStatusResponse{
		Status: "pending",
	}
	if entry.Completed {
		resp.Status = "completed"
		resp.Slug = entry.Slug
		resp.JWT = entry.JWT
		resp.HubURL = s.natsWsURL
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// handleHostRegister handles POST /api/hosts/register (ADR-0035 device-code redemption).
// Called by the dashboard after the user enters the device code.
func (s *Server) handleHostRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req HostRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Code == "" || req.Name == "" {
		http.Error(w, "code and name are required", http.StatusBadRequest)
		return
	}

	globalDeviceCodeStore.mu.Lock()
	entry, exists := globalDeviceCodeStore.codes[req.Code]
	if !exists {
		globalDeviceCodeStore.mu.Unlock()
		http.Error(w, "code not found", http.StatusNotFound)
		return
	}
	if time.Now().After(entry.ExpiresAt) {
		delete(globalDeviceCodeStore.codes, req.Code)
		globalDeviceCodeStore.mu.Unlock()
		http.Error(w, "code expired, restart registration", http.StatusGone)
		return
	}
	if entry.Completed {
		globalDeviceCodeStore.mu.Unlock()
		http.Error(w, "code already redeemed", http.StatusConflict)
		return
	}
	// Hold lock while we create the host (prevent double-redemption).
	globalDeviceCodeStore.mu.Unlock()

	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	// Slugify the name for the host slug.
	slug := slugify(req.Name)
	if slug == "" {
		slug = "host-" + req.Code[:4]
	}

	// Issue a per-host user JWT.
	hostJWT, _, err := IssueHostJWT(entry.UserID, slug, s.accountKP)
	if err != nil {
		http.Error(w, "failed to issue host jwt", http.StatusInternalServerError)
		return
	}

	hostID := uuid.NewString()
	_, err = s.db.pool.Exec(context.Background(), `
		INSERT INTO hosts (id, user_id, slug, name, type, role, public_key, user_jwt)
		VALUES ($1, $2, $3, $4, 'machine', 'owner', $5, $6)
		ON CONFLICT (user_id, slug) DO UPDATE SET name = EXCLUDED.name, public_key = EXCLUDED.public_key, user_jwt = EXCLUDED.user_jwt`,
		hostID, entry.UserID, slug, req.Name, entry.PublicKey, hostJWT)
	if err != nil {
		log.Error().Err(err).Msg("create host from device code")
		http.Error(w, "failed to register host", http.StatusInternalServerError)
		return
	}

	// Mark device code as completed.
	globalDeviceCodeStore.mu.Lock()
	entry.Completed = true
	entry.Slug = slug
	entry.JWT = hostJWT
	globalDeviceCodeStore.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(HostRegisterResponse{ //nolint:errcheck
		Slug:   slug,
		JWT:    hostJWT,
		HubURL: s.natsWsURL,
	})
}

// slugify converts a display name to a URL-safe slug.
func slugify(name string) string {
	s := strings.ToLower(name)
	var b strings.Builder
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		} else if c == ' ' || c == '_' {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
