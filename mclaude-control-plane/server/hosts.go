package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	cpauth "mclaude-control-plane/auth"
	"mclaude-control-plane/slug"
)

// HostResponse is a single host entry in list/CRUD responses.
// ADR-0063: JsDomain field removed (leaf topology dropped per ADR-0054).
// ADR-0097: Type and Role fields removed (BYOH removed; all hosts are K8s user-agent hosts).
type HostResponse struct {
	ID            string     `json:"id"`
	Slug          string     `json:"slug"`
	Name          string     `json:"name"`
	DirectNATSURL *string    `json:"directNatsUrl,omitempty"`
	LastSeenAt    *time.Time `json:"lastSeenAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	JWT           string     `json:"jwt,omitempty"` // GAP-CP-09: minted JWT for host auth
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
	Status    string `json:"status"` // "pending" or "completed"
	Slug      string `json:"slug,omitempty"`
	JWT       string `json:"jwt,omitempty"`
	HubURL    string `json:"hubUrl,omitempty"`
	ExpiresAt int64  `json:"expiresAt,omitempty"` // KNOWN-15: Unix timestamp when the code expires
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

// handleUserAPIRoutes dispatches /api/users/{uslug}/* requests to the correct sub-handler.
func (s *Server) handleUserAPIRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/users/")
	// Split into at most 5 parts to handle deep paths like {uslug}/hosts/{hslug}/grant/accept
	parts := strings.SplitN(path, "/", 6)
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	uslug := parts[0]
	switch {
	case parts[1] == "hosts" && len(parts) >= 4 && parts[3] == "grant" && len(parts) >= 5 && parts[4] == "accept":
		// POST /api/users/{uslug}/hosts/{hslug}/grant/accept
		hslug := parts[2]
		s.handleGrantAccept(w, r, uslug, hslug)
	case parts[1] == "hosts" && len(parts) >= 5 && parts[3] == "user-agent" && parts[4] == "pubkey":
		// GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey
		hslug := parts[2]
		s.handleUserAgentPubkey(w, r, uslug, hslug)
	case parts[1] == "grants":
		// GET /api/users/{uslug}/grants
		s.handleListGrants(w, r, uslug)
	case parts[1] == "hosts":
		s.handleHostRoutes(w, r)
	case parts[1] == "projects":
		s.handleProjectHTTPRoutes(w, r)
	default:
		http.NotFound(w, r)
	}
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

	// ADR-0097: POST /api/users/{uslug}/hosts removed — BYOH dead.
	// All host registration goes through NATS (mclaude.users.*.hosts._.register).
	switch {
	case r.Method == http.MethodGet && len(parts) == 2:
		// GET /api/users/{uslug}/hosts
		s.handleListHosts(w, r, userID)
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
// ADR-0097: type and role columns removed from query and scan.
func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request, userID string) {
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	rows, err := s.db.Pool().Query(r.Context(), `
		SELECT id, slug, name, direct_nats_url, last_seen_at, created_at
		FROM hosts WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var hosts []HostResponse
	for rows.Next() {
		var h HostResponse
		if err := rows.Scan(&h.ID, &h.Slug, &h.Name, &h.DirectNATSURL, &h.LastSeenAt, &h.CreatedAt); err != nil {
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

	tag, err := s.db.Pool().Exec(r.Context(), `
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
	// ADR-0053: Also delete S3 prefixes for each project (best-effort).
	// ADR-0097: all hosts route through user-agent unconditionally (no type branching).
	if s.nc != nil || s.s3 != nil {
		user, userErr := s.db.GetUserByID(r.Context(), userID)
		if userErr == nil && user != nil {
			projects, projErr := s.db.GetProjectsByHostSlug(r.Context(), userID, hslug)
			if projErr == nil {
				for _, p := range projects {
					if s.nc != nil {
						publishProjectsDeleteToHost(s.nc, user.Slug, hslug, p.Slug, p.ID)
					}
					// ADR-0053: Delete S3 prefix for this project (best-effort).
					if s.s3 != nil && p.Slug != "" {
						prefix := user.Slug + "/" + hslug + "/" + p.Slug + "/"
						if s3Err := s.s3.s3DeletePrefix(prefix); s3Err != nil {
							log.Warn().Err(s3Err).Str("prefix", prefix).Msg("host delete: S3 cleanup failed (non-fatal)")
						}
					}
				}
			}
			// Broadcast user-level projects.updated so SPA refreshes.
			if s.nc != nil {
				publishProjectsUpdated(s.nc, user.Slug)
			}
		}
	}

	tag, err := s.db.Pool().Exec(r.Context(), `
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
		Status:    "pending",
		ExpiresAt: entry.ExpiresAt.Unix(), // KNOWN-15: always include expiresAt
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
	hostSlug := slugify(req.Name)
	if hostSlug == "" {
		hostSlug = "host-" + req.Code[:4]
	}

	// ADR-0101: publicKey is required. All hosts generate their own NKey pair
	// (via gen-host-nkey Helm Job) and provide it during device-code registration.
	if entry.PublicKey == "" {
		http.Error(w, `{"error":"public_key is required"}`, http.StatusBadRequest)
		return
	}

	// Issue a per-host JWT using the host-provided public key (ADR-0054, ADR-0101).
	hostJWT, err := cpauth.IssueHostJWT(entry.PublicKey, hostSlug, s.accountKP)
	if err != nil {
		http.Error(w, "failed to issue host jwt", http.StatusInternalServerError)
		return
	}

	// ADR-0097: type and role columns removed from hosts table.
	hostID := uuid.NewString()
	if _, execErr := s.db.Pool().Exec(context.Background(), `
		INSERT INTO hosts (id, user_id, slug, name, public_key, user_jwt)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, slug) DO UPDATE SET name = EXCLUDED.name, public_key = EXCLUDED.public_key, user_jwt = EXCLUDED.user_jwt`,
		hostID, entry.UserID, hostSlug, req.Name, entry.PublicKey, hostJWT); execErr != nil {
		log.Error().Err(execErr).Msg("create host from device code")
		http.Error(w, "failed to register host", http.StatusInternalServerError)
		return
	}

	// Mark device code as completed.
	globalDeviceCodeStore.mu.Lock()
	entry.Completed = true
	entry.Slug = hostSlug
	entry.JWT = hostJWT
	globalDeviceCodeStore.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(HostRegisterResponse{ //nolint:errcheck
		Slug:   hostSlug,
		JWT:    hostJWT,
		HubURL: s.natsWsURL,
	})
}

// ----- ADR-0091: Grant endpoints -----

// GrantEntry is a single entry in the GET /api/users/{uslug}/grants response.
type GrantEntry struct {
	HostSlug           string  `json:"hostSlug"`
	Status             string  `json:"status"`
	CreatedAt          string  `json:"createdAt"`
	AcceptedAt         *string `json:"acceptedAt,omitempty"`
	X25519Fingerprint  *string `json:"x25519Fingerprint,omitempty"` // sha256(x25519_pub) hex; present after user-agent registers
}

// handleListGrants handles GET /api/users/{uslug}/grants (ADR-0091).
// Returns pending and accepted host grants for the user, including x25519 fingerprint.
// Auth: self (uslug must match JWT subject; admin bypass).
func (s *Server) handleListGrants(w http.ResponseWriter, r *http.Request, uslug string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	// Auth: self-only (or admin bypass).
	if err := s.requireSelfOrAdmin(r, uslug); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	ctx := r.Context()
	grants, err := s.db.GetHostGrantsByUserSlug(ctx, uslug)
	if err != nil {
		http.Error(w, "query error", http.StatusInternalServerError)
		return
	}

	entries := make([]GrantEntry, 0, len(grants))
	for _, g := range grants {
		e := GrantEntry{
			HostSlug:  g.HostSlug,
			Status:    g.Status,
			CreatedAt: g.CreatedAt.UTC().Format(time.RFC3339),
		}
		if g.AcceptedAt != nil {
			s := g.AcceptedAt.UTC().Format(time.RFC3339)
			e.AcceptedAt = &s
		}
		// Compute x25519 fingerprint if pubkey is registered.
		ua, _ := s.db.GetUserHostAgentBySlug(ctx, g.HostSlug, uslug)
		if ua != nil && ua.X25519Pub != nil && *ua.X25519Pub != "" {
			fp := computeX25519Fingerprint(*ua.X25519Pub)
			e.X25519Fingerprint = &fp
		}
		entries = append(entries, e)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries) //nolint:errcheck
}

// GrantAcceptRequest is the body for POST /api/users/{uslug}/hosts/{hslug}/grant/accept.
type GrantAcceptRequest struct {
	X25519Fingerprint string `json:"x25519_fingerprint"` // sha256(x25519_pub) hex
}

// handleGrantAccept handles POST /api/users/{uslug}/hosts/{hslug}/grant/accept (ADR-0091).
// Body: {x25519_fingerprint}. CP verifies fingerprint matches user_host_agents.x25519_pub.
// Match → sets host_grants.status='accepted', publishes activation signal. Mismatch → 409.
func (s *Server) handleGrantAccept(w http.ResponseWriter, r *http.Request, uslug, hslug string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	// Auth: self-only (or admin bypass).
	if err := s.requireSelfOrAdmin(r, uslug); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var req GrantAcceptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.X25519Fingerprint == "" {
		http.Error(w, "x25519_fingerprint is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Look up user_host_agents.x25519_pub for (hslug, uslug).
	ua, err := s.db.GetUserHostAgentBySlug(ctx, hslug, uslug)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if ua == nil || ua.X25519Pub == nil || *ua.X25519Pub == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "agent_not_registered"}) //nolint:errcheck
		return
	}

	// Verify fingerprint matches.
	expectedFP := computeX25519Fingerprint(*ua.X25519Pub)
	if req.X25519Fingerprint != expectedFP {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "fingerprint_mismatch"}) //nolint:errcheck
		return
	}

	// Accept the grant.
	if err := s.db.AcceptHostGrant(ctx, hslug, uslug); err != nil {
		http.Error(w, "failed to accept grant", http.StatusInternalServerError)
		return
	}

	// Publish activation signal to user-agent.
	if s.nc != nil {
		subject := "mclaude.users." + uslug + ".hosts." + hslug + ".agent.activate"
		payload, _ := json.Marshal(map[string]string{"status": "accepted"})
		if pubErr := s.nc.Publish(subject, payload); pubErr != nil {
			log.Warn().Err(pubErr).Str("subject", subject).Msg("grant accept: failed to publish activate signal (non-fatal)")
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// UserAgentPubkeyResponse is returned by GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey.
type UserAgentPubkeyResponse struct {
	X25519Pub string `json:"x25519_pub"` // base64-encoded X25519 public key (32 bytes)
}

// handleUserAgentPubkey handles GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey (ADR-0091).
// Returns the user-agent's X25519 public key from user_host_agents.
// SPA reads this to perform crypto_box_seal(token, x25519_pub) before publishing to .agent.token-delivery.
// 404 if x25519_pub is NULL (agent not yet registered).
func (s *Server) handleUserAgentPubkey(w http.ResponseWriter, r *http.Request, uslug, hslug string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	// Auth: self-only (or admin bypass).
	if err := s.requireSelfOrAdmin(r, uslug); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	ctx := r.Context()
	ua, err := s.db.GetUserHostAgentBySlug(ctx, hslug, uslug)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if ua == nil || ua.X25519Pub == nil || *ua.X25519Pub == "" {
		http.Error(w, "user-agent pubkey not yet registered", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(UserAgentPubkeyResponse{X25519Pub: *ua.X25519Pub}) //nolint:errcheck
}

// computeX25519Fingerprint computes sha256(x25519_pub) and returns the result as a hex string.
// The x25519Pub value is the base64-encoded public key string (as stored in user_host_agents.x25519_pub).
// Per ADR-0091: fingerprint = sha256(x25519_pub_bytes) hex, where x25519_pub_bytes is the
// UTF-8 encoding of the base64 string. SPA computes the same fingerprint before calling grant/accept.
func computeX25519Fingerprint(x25519Pub string) string {
	h := sha256.Sum256([]byte(x25519Pub))
	return hex.EncodeToString(h[:])
}

// requireSelfOrAdmin checks that the authenticated user matches uslug (or is admin).
// Returns nil on success, error on failure.
func (s *Server) requireSelfOrAdmin(r *http.Request, uslug string) error {
	userID, ok := r.Context().Value(contextKeyUserID).(string)
	if !ok || userID == "" {
		return fmt.Errorf("not authenticated")
	}
	if s.db == nil {
		return fmt.Errorf("db not available")
	}
	user, err := s.db.GetUserByID(r.Context(), userID)
	if err != nil || user == nil {
		return fmt.Errorf("user not found")
	}
	if user.IsAdmin {
		return nil // admin bypass
	}
	if user.Slug == uslug {
		return nil // self
	}
	return fmt.Errorf("forbidden")
}

// slugify converts a display name to a URL-safe slug.
// GAP-CP-08: Uses slug.Slugify from mclaude-common for proper Unicode handling,
// then validates against the reserved-word blocklist and enforces max 63 char length.
// Returns a fallback slug on reserved-word collision.
func slugify(name string) string {
	s := slug.Slugify(name)
	if s == "" {
		return ""
	}
	// Validate against blocklist and charset; generate fallback on collision.
	if err := slug.Validate(s); err != nil {
		// Generate a deterministic fallback using a UUID seed.
		uid := uuid.New()
		return slug.ValidateOrFallback(s, slug.KindHost, uid)
	}
	return s
}
