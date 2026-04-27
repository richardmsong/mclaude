// providers.go implements OAuth provider integration:
// - Provider config loading from /etc/mclaude/providers.json (Helm ConfigMap mount)
// - OAuth state management (in-memory, 10-minute TTL)
// - HTTP handlers for all provider/connection endpoints
// - CLI config reconciliation (gh-hosts.yml and glab-config.yml in user-secrets Secret)
// - GitLab token refresh background goroutine
//
// See docs/plan-github-oauth.md for the full spec.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"






)

// ---------------------------------------------------------------------------
// Provider config types
// ---------------------------------------------------------------------------

// ProviderConfig is one entry in providers.json (from Helm values).
type ProviderConfig struct {
	ID              string `json:"id"`
	Type            string `json:"type"` // "github" or "gitlab"
	DisplayName     string `json:"displayName"`
	BaseURL         string `json:"baseUrl"`
	APIURL          string `json:"apiUrl,omitempty"`
	ClientID        string `json:"clientId"`
	ClientSecretRef string `json:"clientSecretRef"` // K8s Secret name
	Scopes          string `json:"scopes"`
	// ClientSecret is populated at startup by reading the referenced K8s Secret.
	ClientSecret string `json:"-"`
}

// ---------------------------------------------------------------------------
// OAuth state map
// ---------------------------------------------------------------------------

// oauthState holds the data stored per OAuth flow.
type oauthState struct {
	ReturnURL  string
	UserID     string
	ProviderID string
	ExpiresAt  time.Time
}

// OAuthStateStore is a thread-safe in-memory map of state token → oauthState.
type OAuthStateStore struct {
	mu     sync.Mutex
	states map[string]*oauthState
}

// NewOAuthStateStore creates an empty state store.
func NewOAuthStateStore() *OAuthStateStore {
	return &OAuthStateStore{states: make(map[string]*oauthState)}
}

// Put generates a cryptographically random 32-byte (64 hex char) state token,
// stores the state with a 10-minute TTL, and returns the token.
func (s *OAuthStateStore) Put(userID, providerID, returnURL string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate state token: %w", err)
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.states[token] = &oauthState{
		ReturnURL:  returnURL,
		UserID:     userID,
		ProviderID: providerID,
		ExpiresAt:  time.Now().Add(10 * time.Minute),
	}
	s.mu.Unlock()
	return token, nil
}

// Consume retrieves and removes a state entry. Returns nil if not found or expired.
func (s *OAuthStateStore) Consume(token string) *oauthState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.states[token]
	if !ok {
		return nil
	}
	delete(s.states, token)
	if time.Now().After(st.ExpiresAt) {
		return nil
	}
	return st
}

// Cleanup removes expired entries. Called periodically.
func (s *OAuthStateStore) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.states {
		if now.After(v.ExpiresAt) {
			delete(s.states, k)
		}
	}
}

// ---------------------------------------------------------------------------
// LoadProviders reads providers.json and resolves client secrets from K8s.
// Returns an empty slice when the file is missing (no providers configured).
// ---------------------------------------------------------------------------

// LoadProviders reads /etc/mclaude/providers.json and, for each provider,
// reads the client secret from the referenced K8s Secret (when running in cluster).
// When not in cluster or when clientSecretRef is empty, ClientSecret is left empty.
func LoadProviders(ctx context.Context, path string) ([]ProviderConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read providers.json: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var providers []ProviderConfig
	if err := json.Unmarshal(data, &providers); err != nil {
		return nil, fmt.Errorf("parse providers.json: %w", err)
	}

	// Per ADR-0035: control-plane has zero K8s imports.
	// Client secrets are loaded from environment variables named
	// PROVIDER_SECRET_{ID} (uppercased, dashes replaced with underscores).
	// Falls back to reading from a file at the clientSecretRef path.
	for i := range providers {
		if providers[i].ClientSecretRef == "" {
			continue
		}
		envKey := "PROVIDER_SECRET_" + strings.ToUpper(strings.ReplaceAll(providers[i].ID, "-", "_"))
		if v := os.Getenv(envKey); v != "" {
			providers[i].ClientSecret = v
			continue
		}
		secretPath := "/etc/mclaude/secrets/" + providers[i].ClientSecretRef + "/client-secret"
		if data, err := os.ReadFile(secretPath); err == nil {
			providers[i].ClientSecret = strings.TrimSpace(string(data))
		} else {
			log.Warn().Str("secretRef", providers[i].ClientSecretRef).Msg("provider client secret not found in env or file")
		}
	}
	return providers, nil
}

// ---------------------------------------------------------------------------
// Server fields added for OAuth (attached to Server via the providerRegistry)
// ---------------------------------------------------------------------------

// providerRegistry holds the loaded provider list and state store.
// Attached to Server on startup.
type providerRegistry struct {
	providers   []ProviderConfig
	stateStore  *OAuthStateStore
	externalURL string // e.g. "https://mclaude.internal"
}

// findProvider returns the ProviderConfig with the given id, or nil.
func (r *providerRegistry) findProvider(id string) *ProviderConfig {
	for i := range r.providers {
		if r.providers[i].ID == id {
			return &r.providers[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

// handleGetProviders handles GET /api/providers.
// Returns admin-configured OAuth providers (from Helm). Does not include user PATs.
func (s *Server) handleGetProviders(w http.ResponseWriter, r *http.Request) {
	type providerEntry struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		DisplayName string `json:"displayName"`
		BaseURL     string `json:"baseUrl"`
		Source      string `json:"source"`
	}
	var entries []providerEntry
	if s.providers != nil {
		for _, p := range s.providers.providers {
			entries = append(entries, providerEntry{
				ID:          p.ID,
				Type:        p.Type,
				DisplayName: p.DisplayName,
				BaseURL:     p.BaseURL,
				Source:      "admin",
			})
		}
	}
	if entries == nil {
		entries = []providerEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"providers": entries}) //nolint:errcheck
}

// handleConnectProvider handles POST /api/providers/{id}/connect.
// Starts the OAuth flow for admin OAuth providers. Returns {redirectUrl}.
func (s *Server) handleConnectProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID, ok := r.Context().Value(contextKeyUserID).(string)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Extract {id} from path: /api/providers/{id}/connect
	path := strings.TrimPrefix(r.URL.Path, "/api/providers/")
	path = strings.TrimSuffix(path, "/connect")
	providerID := path

	// PAT connections are added via POST /api/providers/pat, not via OAuth flow.
	if providerID == "pat" {
		http.Error(w, "PAT providers do not use the OAuth connect flow — use POST /api/providers/pat instead", http.StatusBadRequest)
		return
	}

	if s.providers == nil {
		http.Error(w, "no providers configured", http.StatusNotFound)
		return
	}
	p := s.providers.findProvider(providerID)
	if p == nil {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}

	var body struct {
		ReturnURL string `json:"returnUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ReturnURL == "" {
		http.Error(w, "returnUrl required", http.StatusBadRequest)
		return
	}
	if err := validateReturnURL(body.ReturnURL); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Strip non-allowlisted query params before storing the return URL.
	sanitized := sanitizeReturnURL(body.ReturnURL)

	stateToken, err := s.providers.stateStore.Put(userID, providerID, sanitized)
	if err != nil {
		http.Error(w, "failed to generate state", http.StatusInternalServerError)
		return
	}

	callbackURL := s.providers.externalURL + "/auth/providers/" + providerID + "/callback"
	redirectURL := buildAuthorizeURL(p, stateToken, callbackURL)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"redirectUrl": redirectURL}) //nolint:errcheck
}

// handleOAuthCallback handles GET /auth/providers/{id}/callback.
// Exchanges code for token, stores it, and redirects the browser.
func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	// Extract {id} from path: /auth/providers/{id}/callback
	path := strings.TrimPrefix(r.URL.Path, "/auth/providers/")
	path = strings.TrimSuffix(path, "/callback")
	providerID := path

	errorParam := r.URL.Query().Get("error")
	code := r.URL.Query().Get("code")
	stateToken := r.URL.Query().Get("state")

	externalURL := ""
	if s.providers != nil {
		externalURL = s.providers.externalURL
	}

	// Validate state first — applies even for error responses.
	st := s.providers.stateStore.Consume(stateToken)
	if st == nil {
		http.Redirect(w, r, externalURL+"/?error=csrf", http.StatusFound)
		return
	}

	returnURL := st.ReturnURL

	// Handle user-denied authorization.
	if errorParam == "access_denied" {
		redirectWithError(w, r, externalURL, returnURL, "denied")
		return
	}

	p := s.providers.findProvider(providerID)
	if p == nil {
		redirectWithError(w, r, externalURL, returnURL, "csrf")
		return
	}

	callbackURL := externalURL + "/auth/providers/" + providerID + "/callback"

	// Exchange code for token.
	tokenResp, err := exchangeCode(p, code, callbackURL)
	if err != nil {
		log.Error().Err(err).Str("provider", providerID).Msg("OAuth token exchange failed")
		redirectWithError(w, r, externalURL, returnURL, "exchange_failed")
		return
	}

	// Fetch user profile.
	profile, err := fetchUserProfile(p, tokenResp.AccessToken)
	if err != nil {
		log.Error().Err(err).Str("provider", providerID).Msg("OAuth profile fetch failed")
		redirectWithError(w, r, externalURL, returnURL, "profile_failed")
		return
	}

	// GitLab: enforce one-identity-per-host.
	if p.Type == "gitlab" && s.db != nil {
		existing, dbErr := s.db.GetOAuthConnectionsByUser(r.Context(), st.UserID)
		if dbErr == nil {
			for _, c := range existing {
				if c.ProviderType == "gitlab" && c.BaseURL == p.BaseURL {
					redirectWithError(w, r, externalURL, returnURL, "gitlab_one_identity")
					return
				}
			}
		}
	}

	connID := uuid.NewString()
	now := time.Now().UTC()

	// Write tokens to K8s Secret.
	secretKeys := map[string]string{
		"conn-" + connID + "-token":    tokenResp.AccessToken,
		"conn-" + connID + "-username": profile.Username,
	}
	if tokenResp.RefreshToken != "" {
		secretKeys["conn-"+connID+"-refresh-token"] = tokenResp.RefreshToken
	}

	if err := s.patchUserSecret(r.Context(), st.UserID, secretKeys); err != nil {
		log.Error().Err(err).Str("userId", st.UserID).Msg("OAuth: patch user-secrets failed")
		redirectWithError(w, r, externalURL, returnURL, "storage")
		return
	}

	// Write connection metadata to DB.
	var tokenExpiresAt *time.Time
	if tokenResp.ExpiresIn > 0 {
		t := now.Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		tokenExpiresAt = &t
	}

	conn := &OAuthConnection{
		ID:             connID,
		UserID:         st.UserID,
		ProviderID:     providerID,
		ProviderType:   p.Type,
		AuthType:       "oauth",
		BaseURL:        p.BaseURL,
		DisplayName:    p.DisplayName,
		ProviderUserID: profile.ProviderUserID,
		Username:       profile.Username,
		Scopes:         p.Scopes,
		TokenExpiresAt: tokenExpiresAt,
		ConnectedAt:    now,
	}
	if s.db != nil {
		if dbErr := s.db.CreateOAuthConnection(r.Context(), conn); dbErr != nil {
			log.Error().Err(dbErr).Str("userId", st.UserID).Msg("OAuth: DB upsert failed — cleaning up token")
			_ = s.removeSecretKeys(r.Context(), st.UserID, []string{
				"conn-" + connID + "-token",
				"conn-" + connID + "-username",
				"conn-" + connID + "-refresh-token",
			})
			redirectWithError(w, r, externalURL, returnURL, "storage")
			return
		}
		// Reconcile CLI config.
		if err := s.reconcileUserCLIConfig(r.Context(), st.UserID); err != nil {
			log.Warn().Err(err).Str("userId", st.UserID).Msg("OAuth: CLI config reconcile failed (non-fatal)")
		}
	}

	http.Redirect(w, r, externalURL+returnURL, http.StatusFound)
}

// handleAddPAT handles POST /api/providers/pat.
// Validates PAT, stores in Secret + DB. Returns 201 with connection info.
func (s *Server) handleAddPAT(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID, ok := r.Context().Value(contextKeyUserID).(string)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		BaseURL     string `json:"baseUrl"`
		DisplayName string `json:"displayName"`
		Token       string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.BaseURL == "" || body.Token == "" {
		http.Error(w, "baseUrl and token required", http.StatusBadRequest)
		return
	}

	// Auto-detect provider type: GitHub first, then GitLab.
	provType, profile, err := detectPATProvider(body.BaseURL, body.Token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check for duplicate connection.
	if s.db != nil {
		existing, _ := s.db.GetOAuthConnectionsByUser(r.Context(), userID)
		for _, c := range existing {
			if c.BaseURL == body.BaseURL && c.ProviderUserID == profile.ProviderUserID {
				http.Error(w, "connection already exists for this account on this host — disconnect the existing one first", http.StatusConflict)
				return
			}
		}
	}

	connID := uuid.NewString()
	secretKeys := map[string]string{
		"conn-" + connID + "-token":    body.Token,
		"conn-" + connID + "-username": profile.Username,
	}
	if err := s.patchUserSecret(r.Context(), userID, secretKeys); err != nil {
		http.Error(w, "failed to store token", http.StatusInternalServerError)
		return
	}

	displayName := body.DisplayName
	if displayName == "" {
		displayName = body.BaseURL
	}

	conn := &OAuthConnection{
		ID:             connID,
		UserID:         userID,
		ProviderID:     "pat",
		ProviderType:   provType,
		AuthType:       "pat",
		BaseURL:        body.BaseURL,
		DisplayName:    displayName,
		ProviderUserID: profile.ProviderUserID,
		Username:       profile.Username,
		ConnectedAt:    time.Now().UTC(),
	}
	if s.db != nil {
		if err := s.db.CreateOAuthConnection(r.Context(), conn); err != nil {
			_ = s.removeSecretKeys(r.Context(), userID, []string{
				"conn-" + connID + "-token",
				"conn-" + connID + "-username",
			})
			http.Error(w, "failed to save connection", http.StatusInternalServerError)
			return
		}
		if err := s.reconcileUserCLIConfig(r.Context(), userID); err != nil {
			log.Warn().Err(err).Str("userId", userID).Msg("PAT: CLI config reconcile failed (non-fatal)")
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"connectionId": connID,
		"providerType": provType,
		"displayName":  displayName,
		"username":     profile.Username,
	})
}

// handleDeleteConnection handles DELETE /api/connections/{connection_id}.
func (s *Server) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID, ok := r.Context().Value(contextKeyUserID).(string)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	connID := strings.TrimPrefix(r.URL.Path, "/api/connections/")
	connID = strings.TrimSuffix(connID, "/repos") // safety — should not match here
	if connID == "" {
		http.Error(w, "connection_id required", http.StatusBadRequest)
		return
	}

	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	conn, err := s.db.GetOAuthConnectionByID(r.Context(), connID)
	if err != nil || conn == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if conn.UserID != userID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Revoke token on provider (OAuth only, best-effort).
	if conn.AuthType == "oauth" {
		token, _ := s.readSecretKey(r.Context(), userID, "conn-"+connID+"-token")
		if token != "" {
			revokeToken(conn, token, s.providers)
		}
	}

	// Remove token keys from Secret.
	keysToRemove := []string{
		"conn-" + connID + "-token",
		"conn-" + connID + "-username",
		"conn-" + connID + "-refresh-token",
	}
	_ = s.removeSecretKeys(r.Context(), userID, keysToRemove)

	// Delete DB row. The DB CASCADE ON DELETE SET NULL will clear git_identity_id
	// on all projects rows.
	if _, err := s.db.DeleteOAuthConnection(r.Context(), connID); err != nil {
		http.Error(w, "failed to delete connection", http.StatusInternalServerError)
		return
	}

	// Per ADR-0035: MCProject CRD reconciliation is handled by controller-k8s
	// via NATS. The DB CASCADE ON DELETE SET NULL handles git_identity_id cleanup.

	// Update NATS KV for all projects that were linked to this connection,
	// so the SPA reflects the cleared git identity in real-time.
	if s.nc != nil && s.db != nil {
		if affectedProjects, err := s.db.GetProjectsByUser(r.Context(), userID); err == nil {
			for _, proj := range affectedProjects {
				// Only write KV for projects that were affected (git_identity_id now NULL).
				// After the DB cascade, these will have nil GitIdentityID.
				if proj.GitIdentityID == nil {
					if kvErr := writeProjectKV(s.nc, userID, proj); kvErr != nil {
						log.Warn().Err(kvErr).Str("projectId", proj.ID).Msg("disconnect: write KV for affected project failed (non-fatal)")
					}
				}
			}
		}
	}

	// Rebuild CLI config.
	if err := s.reconcileUserCLIConfig(r.Context(), userID); err != nil {
		log.Warn().Err(err).Str("userId", userID).Msg("disconnect: CLI config reconcile failed (non-fatal)")
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleGetConnectionRepos handles GET /api/connections/{connection_id}/repos.
func (s *Server) handleGetConnectionRepos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID, ok := r.Context().Value(contextKeyUserID).(string)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Path: /api/connections/{connection_id}/repos
	path := strings.TrimPrefix(r.URL.Path, "/api/connections/")
	path = strings.TrimSuffix(path, "/repos")
	connID := path

	if s.db == nil {
		http.Error(w, `{"error":"not_connected"}`, http.StatusNotFound)
		return
	}

	conn, err := s.db.GetOAuthConnectionByID(r.Context(), connID)
	if err != nil || conn == nil || conn.UserID != userID {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"not_connected"}`, http.StatusNotFound)
		return
	}

	token, err := s.readSecretKey(r.Context(), userID, "conn-"+connID+"-token")
	if err != nil || token == "" {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"not_connected"}`, http.StatusNotFound)
		return
	}

	q := r.URL.Query().Get("q")
	pageStr := r.URL.Query().Get("page")
	page := 1
	if pageStr != "" {
		fmt.Sscanf(pageStr, "%d", &page) //nolint:errcheck
		if page < 1 {
			page = 1
		}
	}

	result, err := listRepos(conn, token, q, page)
	if err != nil {
		// Pass through rate-limit responses.
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "403") {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "provider unavailable", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result) //nolint:errcheck
}

// handlePatchProject handles PATCH /api/projects/{project_id}.
// Updates the project's git_identity_id.
func (s *Server) handlePatchProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID, ok := r.Context().Value(contextKeyUserID).(string)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	projectID := strings.TrimPrefix(r.URL.Path, "/api/projects/")
	if projectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}

	var body struct {
		GitIdentityID *string `json:"gitIdentityId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if s.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	proj, err := s.db.GetProjectByID(r.Context(), projectID)
	if err != nil || proj == nil || proj.UserID != userID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Validate the connection if one is provided.
	if body.GitIdentityID != nil && *body.GitIdentityID != "" {
		conn, err := s.db.GetOAuthConnectionByID(r.Context(), *body.GitIdentityID)
		if err != nil || conn == nil || conn.UserID != userID {
			http.Error(w, "connection not found", http.StatusBadRequest)
			return
		}
		// Validate hostname matches.
		if proj.GitURL != "" {
			projHost := extractHost(proj.GitURL)
			connHost := extractHost(conn.BaseURL)
			if projHost != "" && connHost != "" && projHost != connHost {
				http.Error(w, "connection hostname does not match project git URL", http.StatusBadRequest)
				return
			}
		}
	}

	if err := s.db.UpdateProjectGitIdentity(r.Context(), projectID, body.GitIdentityID); err != nil {
		http.Error(w, "failed to update project", http.StatusInternalServerError)
		return
	}

	// Per ADR-0035: MCProject CRD sync is handled by controller-k8s.
	// Project update notifications flow through NATS.

	// Write updated ProjectKVState to NATS KV so the SPA sees the change in real-time.
	if s.nc != nil {
		// Re-fetch the updated project so KV state is authoritative.
		updatedProj, fetchErr := s.db.GetProjectByID(r.Context(), projectID)
		if fetchErr == nil && updatedProj != nil {
			if kvErr := writeProjectKV(s.nc, userID, updatedProj); kvErr != nil {
				log.Warn().Err(kvErr).Str("projectId", projectID).Msg("PATCH project: write KV failed (non-fatal)")
			}
		}
	}

	w.WriteHeader(http.StatusOK)
}

// ---------------------------------------------------------------------------
// Return URL validation
// ---------------------------------------------------------------------------

// returnURLAllowedParams is the allowlist of query parameter names permitted in returnUrl.
// Any other params are stripped before the URL is stored and used in redirects.
// Spec: plan-github-oauth.md §Return URL Validation.
var returnURLAllowedParams = map[string]bool{
	"provider":  true,
	"connected": true,
	"goto":      true,
	"error":     true,
}

// validateReturnURL ensures the return URL is a safe relative path.
// Must start with "/" but not "//" (protocol-relative URLs can be redirected off-host).
// Must not contain "://" (absolute URL injection).
// Query params are filtered to the allowlist: provider, connected, goto, error.
// Returns the sanitized URL (with non-allowlisted params stripped) if valid.
func validateReturnURL(u string) error {
	if !strings.HasPrefix(u, "/") {
		return fmt.Errorf("returnUrl must start with /")
	}
	if strings.HasPrefix(u, "//") {
		return fmt.Errorf("returnUrl must not be a protocol-relative URL (//...)")
	}
	if strings.Contains(u, "://") {
		return fmt.Errorf("returnUrl must be a relative path")
	}
	return nil
}

// sanitizeReturnURL strips query parameters not in the allowlist from a validated returnUrl.
// Assumes validateReturnURL has already been called on u.
func sanitizeReturnURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	if parsed.RawQuery == "" {
		return u
	}
	q := parsed.Query()
	for k := range q {
		if !returnURLAllowedParams[k] {
			delete(q, k)
		}
	}
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

// redirectWithError modifies the return URL: replaces "connected=true" with "error={code}",
// then redirects to externalURL + modifiedReturnURL.
func redirectWithError(w http.ResponseWriter, r *http.Request, externalURL, returnURL, code string) {
	modified := strings.ReplaceAll(returnURL, "connected=true", "error="+code)
	if modified == returnURL {
		// No connected=true present — append error param.
		sep := "?"
		if strings.Contains(returnURL, "?") {
			sep = "&"
		}
		modified = returnURL + sep + "error=" + code
	}
	http.Redirect(w, r, externalURL+modified, http.StatusFound)
}

// ---------------------------------------------------------------------------
// OAuth URL construction
// ---------------------------------------------------------------------------

func buildAuthorizeURL(p *ProviderConfig, state, callbackURL string) string {
	var authorizeURL string
	switch p.Type {
	case "gitlab":
		authorizeURL = p.BaseURL + "/oauth/authorize"
	default: // github
		if p.BaseURL == "https://github.com" {
			authorizeURL = "https://github.com/login/oauth/authorize"
		} else {
			authorizeURL = p.BaseURL + "/login/oauth/authorize"
		}
	}
	params := url.Values{
		"client_id":    {p.ClientID},
		"scope":        {p.Scopes},
		"state":        {state},
		"redirect_uri": {callbackURL},
	}
	return authorizeURL + "?" + params.Encode()
}

// ---------------------------------------------------------------------------
// Token exchange
// ---------------------------------------------------------------------------

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	TokenType        string `json:"token_type"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func exchangeCode(p *ProviderConfig, code, callbackURL string) (*tokenResponse, error) {
	var tokenURL string
	switch p.Type {
	case "gitlab":
		tokenURL = p.BaseURL + "/oauth/token"
	default:
		if p.BaseURL == "https://github.com" {
			tokenURL = "https://github.com/login/oauth/access_token"
		} else {
			tokenURL = p.BaseURL + "/login/oauth/access_token"
		}
	}

	params := url.Values{
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
		"code":          {code},
		"redirect_uri":  {callbackURL},
	}
	if p.Type == "gitlab" {
		params.Set("grant_type", "authorization_code")
	}

	req, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange: status %d", resp.StatusCode)
	}
	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, err
	}
	if tr.Error != "" {
		desc := tr.Error
		if tr.ErrorDescription != "" {
			desc += ": " + tr.ErrorDescription
		}
		return nil, fmt.Errorf("token exchange: %s", desc)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("token exchange: empty access_token")
	}
	return &tr, nil
}

// ---------------------------------------------------------------------------
// User profile fetching
// ---------------------------------------------------------------------------

type providerProfile struct {
	ProviderUserID string
	Username       string
}

func fetchUserProfile(p *ProviderConfig, token string) (*providerProfile, error) {
	var profileURL string
	switch p.Type {
	case "gitlab":
		profileURL = p.BaseURL + "/api/v4/user"
	default:
		if p.APIURL != "" {
			profileURL = p.APIURL + "/user"
		} else {
			profileURL = "https://api.github.com/user"
		}
	}

	req, err := http.NewRequest(http.MethodGet, profileURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if p.Type == "github" || p.Type == "" {
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("profile fetch: status %d", resp.StatusCode)
	}

	var profile struct {
		ID    interface{} `json:"id"`
		Login string      `json:"login"`    // GitHub
		Name  string      `json:"username"` // GitLab uses "username"
	}
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, err
	}

	username := profile.Login
	if username == "" {
		username = profile.Name
	}
	providerUserID := fmt.Sprintf("%v", profile.ID)

	return &providerProfile{
		ProviderUserID: providerUserID,
		Username:       username,
	}, nil
}

// ---------------------------------------------------------------------------
// PAT provider auto-detection
// ---------------------------------------------------------------------------

// patError is a typed error returned by fetchProfileWithToken to distinguish
// network/reachability errors from authentication errors.
type patError struct {
	msg         string
	isAuthError bool // true = 401/403; false = network error, 404, etc.
}

func (e *patError) Error() string { return e.msg }

func detectPATProvider(baseURL, token string) (string, *providerProfile, error) {
	// Try GitHub: GET {baseUrl}/api/v3/user (works for GHES; github.com uses /user)
	githubURLs := []string{baseURL + "/api/v3/user"}
	if baseURL == "https://github.com" {
		githubURLs = []string{"https://api.github.com/user"}
	}

	sawAuthError := false
	for _, u := range githubURLs {
		profile, err := fetchProfileWithToken(u, token, "github")
		if err == nil {
			return "github", profile, nil
		}
		if pe, ok := err.(*patError); ok && pe.isAuthError {
			sawAuthError = true
		}
	}

	// Try GitLab: GET {baseUrl}/api/v4/user
	gitlabURL := baseURL + "/api/v4/user"
	profile, err := fetchProfileWithToken(gitlabURL, token, "gitlab")
	if err == nil {
		return "gitlab", profile, nil
	}
	if pe, ok := err.(*patError); ok && pe.isAuthError {
		sawAuthError = true
	}

	// Choose error message based on whether any attempt returned an auth error.
	// A subsequent non-auth error (e.g. 404 from a wrong endpoint) must not
	// clear the auth signal — once we know the token was rejected, report that.
	if sawAuthError {
		return "", nil, fmt.Errorf("invalid token — check that the token has at least read access")
	}
	return "", nil, fmt.Errorf("could not reach provider — check the base URL")
}

func fetchProfileWithToken(apiURL, token, provType string) (*providerProfile, error) {
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		// Network-level error building request — treat as connectivity failure.
		return nil, &patError{msg: err.Error(), isAuthError: false}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if provType == "github" {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	} else {
		req.Header.Set("Accept", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Connection refused, DNS failure, timeout — provider unreachable.
		return nil, &patError{msg: err.Error(), isAuthError: false}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 401/403 = bad token; anything else (404, 5xx) = reachability/config issue.
		isAuth := resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
		return nil, &patError{
			msg:         fmt.Sprintf("status %d", resp.StatusCode),
			isAuthError: isAuth,
		}
	}

	var data struct {
		ID    interface{} `json:"id"`
		Login string      `json:"login"`
		Name  string      `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, &patError{msg: err.Error(), isAuthError: false}
	}
	username := data.Login
	if username == "" {
		username = data.Name
	}
	return &providerProfile{
		ProviderUserID: fmt.Sprintf("%v", data.ID),
		Username:       username,
	}, nil
}

// ---------------------------------------------------------------------------
// Token revocation
// ---------------------------------------------------------------------------

func revokeToken(conn *OAuthConnection, token string, reg *providerRegistry) {
	if reg == nil {
		return
	}
	p := reg.findProvider(conn.ProviderID)
	if p == nil {
		return
	}
	switch p.Type {
	case "github":
		// DELETE /applications/{client_id}/token
		revokeURL := "https://api.github.com/applications/" + p.ClientID + "/token"
		if p.BaseURL != "https://github.com" {
			revokeURL = p.APIURL + "/applications/" + p.ClientID + "/token"
		}
		body := strings.NewReader(`{"access_token":"` + token + `"}`)
		req, err := http.NewRequest(http.MethodDelete, revokeURL, body)
		if err != nil {
			return
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.SetBasicAuth(p.ClientID, p.ClientSecret)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	case "gitlab":
		// POST {baseUrl}/oauth/revoke
		revokeURL := p.BaseURL + "/oauth/revoke"
		params := url.Values{
			"token":         {token},
			"client_id":     {p.ClientID},
			"client_secret": {p.ClientSecret},
		}
		resp, err := http.PostForm(revokeURL, params)
		if err == nil {
			resp.Body.Close()
		}
	}
}

// ---------------------------------------------------------------------------
// Per ADR-0035: K8s Secret operations are handled by the controller-k8s.
// Control-plane no longer manages K8s Secrets directly. These no-op stubs
// maintain the call interface while the controller handles the actual K8s
// resource management via NATS commands.
// ---------------------------------------------------------------------------

// patchUserSecret is a no-op — K8s Secret management moved to controller-k8s per ADR-0035.
func (s *Server) patchUserSecret(ctx context.Context, userID string, data map[string]string) error {
	return nil
}

// removeSecretKeys is a no-op — K8s Secret management moved to controller-k8s per ADR-0035.
func (s *Server) removeSecretKeys(ctx context.Context, userID string, keys []string) error {
	return nil
}

// readSecretKey is a no-op — K8s Secret management moved to controller-k8s per ADR-0035.
func (s *Server) readSecretKey(ctx context.Context, userID, key string) (string, error) {
	return "", nil
}

// ---------------------------------------------------------------------------
// CLI config reconciliation (no-op per ADR-0035)
// ---------------------------------------------------------------------------

// reconcileUserCLIConfig is a no-op — K8s Secret management moved to controller-k8s per ADR-0035.
func (s *Server) reconcileUserCLIConfig(ctx context.Context, userID string) error {
	return nil
}

// buildGHHostsYAML builds the gh-hosts.yml content from GitHub-type connections.
// Format: per-host users map, most-recently-connected user is the active default.
func buildGHHostsYAML(conns []*OAuthConnection, tokenMap map[string]string) string {
	// Group GitHub connections by host.
	type hostEntry struct {
		users       map[string]string // username -> token
		defaultUser string
		latestTime  time.Time
	}
	hosts := make(map[string]*hostEntry)

	for _, c := range conns {
		if c.ProviderType != "github" {
			continue
		}
		token := tokenMap[c.ID]
		if token == "" {
			continue
		}
		host := extractHost(c.BaseURL)
		if host == "" {
			continue
		}
		if _, ok := hosts[host]; !ok {
			hosts[host] = &hostEntry{users: make(map[string]string)}
		}
		hosts[host].users[c.Username] = token
		if c.ConnectedAt.After(hosts[host].latestTime) {
			hosts[host].latestTime = c.ConnectedAt
			hosts[host].defaultUser = c.Username
		}
	}

	if len(hosts) == 0 {
		return ""
	}

	// Sort hosts for deterministic output.
	hostNames := make([]string, 0, len(hosts))
	for h := range hosts {
		hostNames = append(hostNames, h)
	}
	sort.Strings(hostNames)

	var sb strings.Builder
	for _, host := range hostNames {
		entry := hosts[host]
		sb.WriteString(host + ":\n")
		sb.WriteString("    users:\n")

		// Sort users for determinism.
		userNames := make([]string, 0, len(entry.users))
		for u := range entry.users {
			userNames = append(userNames, u)
		}
		sort.Strings(userNames)
		for _, u := range userNames {
			sb.WriteString("        " + u + ":\n")
			sb.WriteString("            oauth_token: " + entry.users[u] + "\n")
		}
		sb.WriteString("    user: " + entry.defaultUser + "\n")
	}
	return sb.String()
}

// buildGlabConfigYAML builds the glab-config.yml content from GitLab-type connections.
func buildGlabConfigYAML(conns []*OAuthConnection, tokenMap map[string]string) string {
	type hostEntry struct {
		token    string
		host     string
		username string
	}
	var hosts []hostEntry

	for _, c := range conns {
		if c.ProviderType != "gitlab" {
			continue
		}
		token := tokenMap[c.ID]
		if token == "" {
			continue
		}
		host := extractHost(c.BaseURL)
		if host == "" {
			continue
		}
		hosts = append(hosts, hostEntry{token: token, host: host, username: c.Username})
	}

	if len(hosts) == 0 {
		return ""
	}

	sort.Slice(hosts, func(i, j int) bool { return hosts[i].host < hosts[j].host })

	var sb strings.Builder
	sb.WriteString("hosts:\n")
	for _, h := range hosts {
		sb.WriteString("  " + h.host + ":\n")
		sb.WriteString("    token: " + h.token + "\n")
		sb.WriteString("    api_host: " + h.host + "\n")
		sb.WriteString("    user: " + h.username + "\n")
	}
	return sb.String()
}

// extractHost parses a URL and returns just the hostname (no scheme, no port if standard).
func extractHost(rawURL string) string {
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// ---------------------------------------------------------------------------
// Repo listing
// ---------------------------------------------------------------------------

type repoEntry struct {
	Name        string `json:"name"`
	FullName    string `json:"fullName"`
	Private     bool   `json:"private"`
	Description string `json:"description"`
	CloneURL    string `json:"cloneUrl"`
	UpdatedAt   string `json:"updatedAt"`
}

type repoListResult struct {
	Repos    []repoEntry `json:"repos"`
	NextPage *int        `json:"nextPage"`
	HasMore  bool        `json:"hasMore"`
}

func listRepos(conn *OAuthConnection, token, query string, page int) (*repoListResult, error) {
	switch conn.ProviderType {
	case "github":
		return listGitHubRepos(conn, token, query, page)
	case "gitlab":
		return listGitLabRepos(conn, token, query, page)
	default:
		return nil, fmt.Errorf("unknown provider type: %s", conn.ProviderType)
	}
}

func listGitHubRepos(conn *OAuthConnection, token, query string, page int) (*repoListResult, error) {
	apiURL := "https://api.github.com"
	if conn.BaseURL != "https://github.com" {
		apiURL = conn.BaseURL + "/api/v3"
	}

	var reqURL string
	if query != "" {
		reqURL = fmt.Sprintf("%s/search/repositories?q=%s&page=%d&per_page=30",
			apiURL, url.QueryEscape(query), page)
	} else {
		reqURL = fmt.Sprintf("%s/user/repos?sort=pushed&page=%d&per_page=30", apiURL, page)
	}

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("429")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)

	var repos []repoEntry
	if query != "" {
		var searchResult struct {
			Items []struct {
				Name        string `json:"name"`
				FullName    string `json:"full_name"`
				Private     bool   `json:"private"`
				Description string `json:"description"`
				CloneURL    string `json:"clone_url"`
				UpdatedAt   string `json:"updated_at"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &searchResult); err != nil {
			return nil, err
		}
		for _, r := range searchResult.Items {
			repos = append(repos, repoEntry{
				Name: r.Name, FullName: r.FullName, Private: r.Private,
				Description: r.Description, CloneURL: r.CloneURL, UpdatedAt: r.UpdatedAt,
			})
		}
	} else {
		var rawRepos []struct {
			Name        string `json:"name"`
			FullName    string `json:"full_name"`
			Private     bool   `json:"private"`
			Description string `json:"description"`
			CloneURL    string `json:"clone_url"`
			UpdatedAt   string `json:"updated_at"`
		}
		if err := json.Unmarshal(body, &rawRepos); err != nil {
			return nil, err
		}
		for _, r := range rawRepos {
			repos = append(repos, repoEntry{
				Name: r.Name, FullName: r.FullName, Private: r.Private,
				Description: r.Description, CloneURL: r.CloneURL, UpdatedAt: r.UpdatedAt,
			})
		}
	}

	hasMore := len(repos) == 30
	var nextPage *int
	if hasMore {
		np := page + 1
		nextPage = &np
	}
	if repos == nil {
		repos = []repoEntry{}
	}
	return &repoListResult{Repos: repos, NextPage: nextPage, HasMore: hasMore}, nil
}

func listGitLabRepos(conn *OAuthConnection, token, query string, page int) (*repoListResult, error) {
	apiURL := conn.BaseURL + "/api/v4"

	var reqURL string
	if query != "" {
		reqURL = fmt.Sprintf("%s/projects?search=%s&page=%d&per_page=30&membership=true",
			apiURL, url.QueryEscape(query), page)
	} else {
		reqURL = fmt.Sprintf("%s/projects?order_by=last_activity_at&page=%d&per_page=30&membership=true",
			apiURL, page)
	}

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("429")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var rawRepos []struct {
		Name              string `json:"name"`
		PathWithNamespace string `json:"path_with_namespace"`
		Description       string `json:"description"`
		HTTPURLToRepo     string `json:"http_url_to_repo"`
		LastActivityAt    string `json:"last_activity_at"`
		Visibility        string `json:"visibility"`
	}

	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &rawRepos); err != nil {
		return nil, err
	}

	var repos []repoEntry
	for _, r := range rawRepos {
		repos = append(repos, repoEntry{
			Name:        r.Name,
			FullName:    r.PathWithNamespace,
			Private:     r.Visibility == "private",
			Description: r.Description,
			CloneURL:    r.HTTPURLToRepo,
			UpdatedAt:   r.LastActivityAt,
		})
	}

	nextPageHeader := resp.Header.Get("X-Next-Page")
	hasMore := nextPageHeader != ""
	var nextPage *int
	if hasMore {
		np := page + 1
		nextPage = &np
	}
	if repos == nil {
		repos = []repoEntry{}
	}
	return &repoListResult{Repos: repos, NextPage: nextPage, HasMore: hasMore}, nil
}

// ---------------------------------------------------------------------------
// GitLab token refresh goroutine
// ---------------------------------------------------------------------------

// StartGitLabRefreshGoroutine starts a background goroutine that refreshes
// GitLab tokens expiring within 30 minutes, every 15 minutes.
func (s *Server) StartGitLabRefreshGoroutine(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.refreshExpiringGitLabTokens(ctx); err != nil {
					log.Error().Err(err).Msg("GitLab token refresh cycle failed")
				}
			}
		}
	}()
}

func (s *Server) refreshExpiringGitLabTokens(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	conns, err := s.db.GetExpiringGitLabConnections(ctx, 30*time.Minute)
	if err != nil {
		return fmt.Errorf("get expiring GitLab connections: %w", err)
	}

	for _, conn := range conns {
		if err := s.refreshGitLabToken(ctx, conn); err != nil {
			log.Warn().Err(err).Str("connectionId", conn.ID).Str("userId", conn.UserID).
				Msg("GitLab token refresh failed")
		}
	}
	return nil
}

func (s *Server) refreshGitLabToken(ctx context.Context, conn *OAuthConnection) error {
	refreshToken, err := s.readSecretKey(ctx, conn.UserID, "conn-"+conn.ID+"-refresh-token")
	if err != nil || refreshToken == "" {
		return fmt.Errorf("read refresh token: %w", err)
	}

	p := s.providers.findProvider(conn.ProviderID)
	if p == nil {
		return fmt.Errorf("provider %s not found in config", conn.ProviderID)
	}

	newTokens, err := exchangeRefreshToken(p, refreshToken)
	if err != nil {
		// Retry once with the latest refresh token from Secret.
		refreshToken2, _ := s.readSecretKey(ctx, conn.UserID, "conn-"+conn.ID+"-refresh-token")
		if refreshToken2 != refreshToken {
			newTokens, err = exchangeRefreshToken(p, refreshToken2)
		}
		if err != nil {
			// Refresh token is expired/revoked — delete connection.
			log.Warn().Str("connectionId", conn.ID).Msg("GitLab refresh token expired — removing connection")
			_ = s.removeSecretKeys(ctx, conn.UserID, []string{
				"conn-" + conn.ID + "-token",
				"conn-" + conn.ID + "-refresh-token",
				"conn-" + conn.ID + "-username",
			})
			if _, dbErr := s.db.DeleteOAuthConnection(ctx, conn.ID); dbErr != nil {
				log.Warn().Err(dbErr).Str("connectionId", conn.ID).Msg("delete expired GitLab connection")
			}
			_ = s.reconcileUserCLIConfig(ctx, conn.UserID)
			return fmt.Errorf("refresh token expired")
		}
	}

	// Write new tokens to Secret.
	secretUpdates := map[string]string{
		"conn-" + conn.ID + "-token": newTokens.AccessToken,
	}
	if newTokens.RefreshToken != "" {
		secretUpdates["conn-"+conn.ID+"-refresh-token"] = newTokens.RefreshToken
	}
	if err := s.patchUserSecret(ctx, conn.UserID, secretUpdates); err != nil {
		return fmt.Errorf("update token in secret: %w", err)
	}

	// Update expiry in DB.
	if newTokens.ExpiresIn > 0 {
		newExpiry := time.Now().Add(time.Duration(newTokens.ExpiresIn) * time.Second)
		if err := s.db.UpdateTokenExpiry(ctx, conn.ID, newExpiry); err != nil {
			log.Warn().Err(err).Str("connectionId", conn.ID).Msg("update token_expires_at")
		}
	}

	// Rebuild CLI config.
	if err := s.reconcileUserCLIConfig(ctx, conn.UserID); err != nil {
		log.Warn().Err(err).Str("connectionId", conn.ID).Msg("CLI config reconcile after refresh")
	}
	return nil
}

func exchangeRefreshToken(p *ProviderConfig, refreshToken string) (*tokenResponse, error) {
	tokenURL := p.BaseURL + "/oauth/token"
	params := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
	}
	req, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("401 unauthorized")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, err
	}
	return &tr, nil
}

// ---------------------------------------------------------------------------
// Startup reconcile
// ---------------------------------------------------------------------------

// ReconcileAllUserCLIConfigs runs reconcileUserCLIConfig for all users on startup.
// Best-effort: each user gets a 10-second timeout; failures are logged and skipped.
// Also cleans up orphaned connections (oauth type whose provider_id no longer exists in config).
func (s *Server) ReconcileAllUserCLIConfigs(ctx context.Context) {
	if s.db == nil {
		return
	}

	rows, err := s.db.pool.Query(ctx, `SELECT id FROM users`)
	if err != nil {
		log.Warn().Err(err).Msg("startup reconcile: list users failed")
		return
	}
	defer rows.Close()

	var userIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			userIDs = append(userIDs, id)
		}
	}

	// Collect current provider IDs.
	adminProviderIDs := make(map[string]bool)
	if s.providers != nil {
		for _, p := range s.providers.providers {
			adminProviderIDs[p.ID] = true
		}
	}

	for _, userID := range userIDs {
		userCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		func() {
			defer cancel()
			// Cleanup orphaned OAuth connections.
			conns, err := s.db.GetOAuthConnectionsByUser(userCtx, userID)
			if err == nil {
				for _, c := range conns {
					if c.AuthType == "oauth" && !adminProviderIDs[c.ProviderID] {
						log.Info().Str("connectionId", c.ID).Str("userId", userID).
							Str("providerId", c.ProviderID).
							Msg("startup: removing orphaned OAuth connection (provider removed from Helm)")
						_ = s.removeSecretKeys(userCtx, userID, []string{
							"conn-" + c.ID + "-token",
							"conn-" + c.ID + "-refresh-token",
							"conn-" + c.ID + "-username",
						})
						if _, err := s.db.DeleteOAuthConnection(userCtx, c.ID); err != nil {
							log.Warn().Err(err).Str("connectionId", c.ID).Msg("startup: delete orphaned connection")
						}
					}
				}
			}
			if err := s.reconcileUserCLIConfig(userCtx, userID); err != nil {
				log.Warn().Err(err).Str("userId", userID).Msg("startup: CLI config reconcile failed (non-fatal)")
			}
		}()
	}
}
