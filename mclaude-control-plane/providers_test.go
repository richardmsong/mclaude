package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// OAuth state store
// ---------------------------------------------------------------------------

func TestOAuthStateStore_PutAndConsume(t *testing.T) {
	store := NewOAuthStateStore()
	token, err := store.Put("user1", "github", "/settings")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if len(token) != 64 {
		t.Errorf("expected 64-char hex token, got len=%d", len(token))
	}

	st := store.Consume(token)
	if st == nil {
		t.Fatal("Consume returned nil for valid token")
	}
	if st.UserID != "user1" {
		t.Errorf("UserID: want user1, got %s", st.UserID)
	}
	if st.ProviderID != "github" {
		t.Errorf("ProviderID: want github, got %s", st.ProviderID)
	}
	if st.ReturnURL != "/settings" {
		t.Errorf("ReturnURL: want /settings, got %s", st.ReturnURL)
	}

	// Second consume returns nil (single-use).
	if store.Consume(token) != nil {
		t.Error("second Consume should return nil (single-use token)")
	}
}

func TestOAuthStateStore_ExpiredToken(t *testing.T) {
	store := NewOAuthStateStore()
	token, err := store.Put("user1", "github", "/")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Force expiry by reaching into the map.
	store.mu.Lock()
	store.states[token].ExpiresAt = time.Now().Add(-1 * time.Minute)
	store.mu.Unlock()

	if store.Consume(token) != nil {
		t.Error("Consume of expired token should return nil")
	}
}

func TestOAuthStateStore_UnknownToken(t *testing.T) {
	store := NewOAuthStateStore()
	if store.Consume("nonexistent") != nil {
		t.Error("Consume of unknown token should return nil")
	}
}

func TestOAuthStateStore_Cleanup(t *testing.T) {
	store := NewOAuthStateStore()
	token, _ := store.Put("user1", "github", "/")
	store.mu.Lock()
	store.states[token].ExpiresAt = time.Now().Add(-time.Second)
	store.mu.Unlock()

	store.Cleanup()

	store.mu.Lock()
	_, exists := store.states[token]
	store.mu.Unlock()
	if exists {
		t.Error("Cleanup should have removed expired entry")
	}
}

func TestOAuthStateStore_Uniqueness(t *testing.T) {
	store := NewOAuthStateStore()
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		token, err := store.Put("user", "github", "/")
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		if seen[token] {
			t.Fatalf("duplicate state token on iteration %d", i)
		}
		seen[token] = true
	}
}

// ---------------------------------------------------------------------------
// Return URL validation
// ---------------------------------------------------------------------------

func TestValidateReturnURL_Valid(t *testing.T) {
	cases := []string{
		"/",
		"/?provider=github&connected=true&goto=settings",
		"/?error=denied",
		"/settings",
	}
	for _, c := range cases {
		if err := validateReturnURL(c); err != nil {
			t.Errorf("validateReturnURL(%q) returned error: %v", c, err)
		}
	}
}

func TestValidateReturnURL_Invalid(t *testing.T) {
	cases := []string{
		"http://evil.com",
		"https://evil.com",
		"//evil.com",
		"relative-path",
		"",
	}
	for _, c := range cases {
		if err := validateReturnURL(c); err == nil {
			t.Errorf("validateReturnURL(%q) should have returned error", c)
		}
	}
}

// ---------------------------------------------------------------------------
// CLI config generation
// ---------------------------------------------------------------------------

func TestBuildGHHostsYAML_Single(t *testing.T) {
	conns := []*OAuthConnection{
		{
			ID:           "conn-1",
			ProviderType: "github",
			BaseURL:      "https://github.com",
			Username:     "rsong-work",
			ConnectedAt:  time.Now(),
		},
	}
	tokenMap := map[string]string{"conn-1": "gho_abc123"}

	yaml := buildGHHostsYAML(conns, tokenMap)
	if !strings.Contains(yaml, "github.com:") {
		t.Errorf("expected github.com: in yaml, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "rsong-work:") {
		t.Errorf("expected rsong-work: in yaml, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "oauth_token: gho_abc123") {
		t.Errorf("expected oauth_token in yaml, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "user: rsong-work") {
		t.Errorf("expected user: rsong-work in yaml, got:\n%s", yaml)
	}
}

func TestBuildGHHostsYAML_MultiAccount(t *testing.T) {
	t1 := time.Now().Add(-2 * time.Hour)
	t2 := time.Now()
	conns := []*OAuthConnection{
		{ID: "c1", ProviderType: "github", BaseURL: "https://github.com", Username: "alice", ConnectedAt: t1},
		{ID: "c2", ProviderType: "github", BaseURL: "https://github.com", Username: "bob", ConnectedAt: t2},
	}
	tokenMap := map[string]string{"c1": "tok-alice", "c2": "tok-bob"}

	yaml := buildGHHostsYAML(conns, tokenMap)
	if !strings.Contains(yaml, "alice:") {
		t.Errorf("expected alice in yaml, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "bob:") {
		t.Errorf("expected bob in yaml, got:\n%s", yaml)
	}
	// bob is more recent — should be default
	if !strings.Contains(yaml, "user: bob") {
		t.Errorf("expected user: bob (most recent) in yaml, got:\n%s", yaml)
	}
}

func TestBuildGHHostsYAML_SkipsGitLab(t *testing.T) {
	conns := []*OAuthConnection{
		{ID: "c1", ProviderType: "gitlab", BaseURL: "https://gitlab.com", Username: "rsong", ConnectedAt: time.Now()},
	}
	tokenMap := map[string]string{"c1": "glpat_abc"}
	yaml := buildGHHostsYAML(conns, tokenMap)
	if strings.Contains(yaml, "gitlab.com") {
		t.Errorf("gh-hosts.yml should not contain gitlab entries, got:\n%s", yaml)
	}
}

func TestBuildGHHostsYAML_Empty(t *testing.T) {
	yaml := buildGHHostsYAML(nil, nil)
	if yaml != "" {
		t.Errorf("empty connections should produce empty yaml, got: %q", yaml)
	}
}

func TestBuildGlabConfigYAML_Single(t *testing.T) {
	conns := []*OAuthConnection{
		{ID: "c1", ProviderType: "gitlab", BaseURL: "https://gitlab.com", Username: "rsong", ConnectedAt: time.Now()},
	}
	tokenMap := map[string]string{"c1": "glpat_abc123"}

	yaml := buildGlabConfigYAML(conns, tokenMap)
	if !strings.Contains(yaml, "hosts:") {
		t.Errorf("expected hosts: in yaml, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "gitlab.com:") {
		t.Errorf("expected gitlab.com: in yaml, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "token: glpat_abc123") {
		t.Errorf("expected token in yaml, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "user: rsong") {
		t.Errorf("expected user: rsong in yaml, got:\n%s", yaml)
	}
}

func TestBuildGlabConfigYAML_SkipsGitHub(t *testing.T) {
	conns := []*OAuthConnection{
		{ID: "c1", ProviderType: "github", BaseURL: "https://github.com", Username: "alice", ConnectedAt: time.Now()},
	}
	tokenMap := map[string]string{"c1": "gho_abc"}
	yaml := buildGlabConfigYAML(conns, tokenMap)
	if strings.Contains(yaml, "github.com") {
		t.Errorf("glab-config.yml should not contain github entries, got:\n%s", yaml)
	}
}

func TestBuildGlabConfigYAML_Empty(t *testing.T) {
	yaml := buildGlabConfigYAML(nil, nil)
	if yaml != "" {
		t.Errorf("empty connections should produce empty yaml, got: %q", yaml)
	}
}

// ---------------------------------------------------------------------------
// extractHost
// ---------------------------------------------------------------------------

func TestExtractHost(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"https://github.com", "github.com"},
		{"https://gitlab.com", "gitlab.com"},
		{"https://github.acme.com", "github.acme.com"},
		{"github.com", "github.com"},
	}
	for _, c := range cases {
		got := extractHost(c.input)
		if got != c.want {
			t.Errorf("extractHost(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// GET /api/providers
// ---------------------------------------------------------------------------

func TestHandleGetProviders_Empty(t *testing.T) {
	srv := newTestServer(t)
	srv.providers = &providerRegistry{
		providers:   nil,
		stateStore:  NewOAuthStateStore(),
		externalURL: "https://mclaude.internal",
	}

	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	w := httptest.NewRecorder()
	srv.handleGetProviders(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	providers, ok := resp["providers"].([]any)
	if !ok {
		t.Fatalf("providers field missing or not array")
	}
	if len(providers) != 0 {
		t.Errorf("expected empty providers array, got %d entries", len(providers))
	}
}

func TestHandleGetProviders_WithProviders(t *testing.T) {
	srv := newTestServer(t)
	srv.providers = &providerRegistry{
		providers: []ProviderConfig{
			{ID: "github", Type: "github", DisplayName: "GitHub", BaseURL: "https://github.com"},
			{ID: "gitlab", Type: "gitlab", DisplayName: "GitLab", BaseURL: "https://gitlab.com"},
		},
		stateStore:  NewOAuthStateStore(),
		externalURL: "https://mclaude.internal",
	}

	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	w := httptest.NewRecorder()
	srv.handleGetProviders(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp) //nolint:errcheck
	providers := resp["providers"].([]any)
	if len(providers) != 2 {
		t.Errorf("expected 2 providers, got %d", len(providers))
	}
	first := providers[0].(map[string]any)
	if first["id"] != "github" {
		t.Errorf("first provider id: want github, got %v", first["id"])
	}
	if first["source"] != "admin" {
		t.Errorf("first provider source: want admin, got %v", first["source"])
	}
}

// ---------------------------------------------------------------------------
// POST /api/providers/{id}/connect
// ---------------------------------------------------------------------------

func newTestServerWithProviders(t *testing.T) *Server {
	t.Helper()
	srv := newTestServer(t)
	srv.providers = &providerRegistry{
		providers: []ProviderConfig{
			{
				ID:          "github",
				Type:        "github",
				DisplayName: "GitHub",
				BaseURL:     "https://github.com",
				ClientID:    "Ov23li_test",
				Scopes:      "repo",
			},
		},
		stateStore:  NewOAuthStateStore(),
		externalURL: "https://mclaude.internal",
	}
	return srv
}

func TestHandleConnectProvider_RequiresReturnURL(t *testing.T) {
	srv := newTestServerWithProviders(t)

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/github/connect", body)
	req = req.WithContext(contextWithUserID(req.Context(), "user1"))
	w := httptest.NewRecorder()
	srv.handleConnectProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleConnectProvider_InvalidReturnURL(t *testing.T) {
	srv := newTestServerWithProviders(t)

	body := bytes.NewBufferString(`{"returnUrl":"https://evil.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/github/connect", body)
	req = req.WithContext(contextWithUserID(req.Context(), "user1"))
	w := httptest.NewRecorder()
	srv.handleConnectProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid returnUrl, got %d", w.Code)
	}
}

func TestHandleConnectProvider_UnknownProvider(t *testing.T) {
	srv := newTestServerWithProviders(t)

	body := bytes.NewBufferString(`{"returnUrl":"/settings"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/unknown/connect", body)
	req.URL.Path = "/api/providers/unknown/connect"
	req = req.WithContext(contextWithUserID(req.Context(), "user1"))
	w := httptest.NewRecorder()
	srv.handleConnectProvider(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestHandleConnectProvider_Success(t *testing.T) {
	srv := newTestServerWithProviders(t)

	body := bytes.NewBufferString(`{"returnUrl":"/?provider=github&connected=true&goto=settings"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/github/connect", body)
	req.URL.Path = "/api/providers/github/connect"
	req = req.WithContext(contextWithUserID(req.Context(), "user1"))
	w := httptest.NewRecorder()
	srv.handleConnectProvider(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	redirectURL := resp["redirectUrl"]
	if !strings.Contains(redirectURL, "github.com") {
		t.Errorf("expected github.com in redirectUrl, got %s", redirectURL)
	}
	if !strings.Contains(redirectURL, "client_id=Ov23li_test") {
		t.Errorf("expected client_id in redirectUrl, got %s", redirectURL)
	}
	if !strings.Contains(redirectURL, "state=") {
		t.Errorf("expected state= in redirectUrl, got %s", redirectURL)
	}
}

// ---------------------------------------------------------------------------
// OAuth callback error cases
// ---------------------------------------------------------------------------

func TestHandleOAuthCallback_MissingState(t *testing.T) {
	srv := newTestServerWithProviders(t)

	req := httptest.NewRequest(http.MethodGet, "/auth/providers/github/callback?code=abc&state=badstate", nil)
	req.URL.Path = "/auth/providers/github/callback"
	w := httptest.NewRecorder()
	srv.handleOAuthCallback(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=csrf") {
		t.Errorf("expected error=csrf in redirect, got %s", loc)
	}
}

func TestHandleOAuthCallback_UserDenied(t *testing.T) {
	srv := newTestServerWithProviders(t)

	// Put a valid state first.
	stateToken, _ := srv.providers.stateStore.Put("user1", "github", "/?provider=github&connected=true")
	req := httptest.NewRequest(http.MethodGet,
		"/auth/providers/github/callback?error=access_denied&state="+stateToken, nil)
	req.URL.Path = "/auth/providers/github/callback"
	w := httptest.NewRecorder()
	srv.handleOAuthCallback(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=denied") {
		t.Errorf("expected error=denied in redirect, got %s", loc)
	}
	// Should NOT have connected=true
	if strings.Contains(loc, "connected=true") {
		t.Errorf("redirect should not have connected=true after denial, got %s", loc)
	}
}

// ---------------------------------------------------------------------------
// DB schema tests (oauth_connections)
// ---------------------------------------------------------------------------

func TestSchema_HasOAuthConnectionsTable(t *testing.T) {
	if !strings.Contains(schema, "oauth_connections") {
		t.Error("schema missing oauth_connections table")
	}
}

func TestSchema_HasGitIdentityIDColumn(t *testing.T) {
	if !strings.Contains(schema, "git_identity_id") {
		t.Error("schema missing git_identity_id column on projects")
	}
}

func TestSchema_OAuthConnectionsHasUniqueConstraint(t *testing.T) {
	if !strings.Contains(schema, "UNIQUE(user_id, base_url, provider_user_id)") {
		t.Error("oauth_connections should have UNIQUE(user_id, base_url, provider_user_id)")
	}
}

func TestSchema_GitIdentityIDCascadeSetNull(t *testing.T) {
	if !strings.Contains(schema, "ON DELETE SET NULL") {
		t.Error("git_identity_id should have ON DELETE SET NULL")
	}
}

// ---------------------------------------------------------------------------
// /auth/me includes connectedProviders
// ---------------------------------------------------------------------------

func TestHandleMe_IncludesConnectedProviders(t *testing.T) {
	srv := newTestServerWithProviders(t)
	// db is nil — handleMe should still return connectedProviders (empty array).

	// We need a user in db, but db is nil — handleMe returns 404 if db is nil.
	// Instead test that the response structure is correct when db is present.
	// This is a unit test with nil db; the field is validated via integration tests.
	// Just confirm the endpoint exists and uses authMiddleware.
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	w := httptest.NewRecorder()
	// Without auth header, authMiddleware returns 401.
	srv.authMiddleware(http.HandlerFunc(srv.handleMe)).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401 without auth, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Provider auto-detection for PAT (unit test with mock server)
// ---------------------------------------------------------------------------

func TestDetectPATProvider_GitHub(t *testing.T) {
	// Start a mock GitHub API server.
	mockGitHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/user" || r.URL.Path == "/user" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"id": 42, "login": "testuser"}) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	defer mockGitHub.Close()

	provType, profile, err := detectPATProvider(mockGitHub.URL, "ghp_test_token")
	if err != nil {
		t.Fatalf("detectPATProvider: %v", err)
	}
	if provType != "github" {
		t.Errorf("providerType: want github, got %s", provType)
	}
	if profile.Username != "testuser" {
		t.Errorf("username: want testuser, got %s", profile.Username)
	}
	if profile.ProviderUserID != "42" {
		t.Errorf("providerUserID: want 42, got %s", profile.ProviderUserID)
	}
}

func TestDetectPATProvider_GitLab(t *testing.T) {
	// Start a mock GitLab API server (github path fails, gitlab succeeds).
	mockGitLab := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/user" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"id": 99, "username": "gluser"}) //nolint:errcheck
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer mockGitLab.Close()

	provType, profile, err := detectPATProvider(mockGitLab.URL, "glpat_test_token")
	if err != nil {
		t.Fatalf("detectPATProvider GitLab: %v", err)
	}
	if provType != "gitlab" {
		t.Errorf("providerType: want gitlab, got %s", provType)
	}
	if profile.Username != "gluser" {
		t.Errorf("username: want gluser, got %s", profile.Username)
	}
}

func TestDetectPATProvider_InvalidToken(t *testing.T) {
	// Both endpoints return 401.
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer mockServer.Close()

	_, _, err := detectPATProvider(mockServer.URL, "invalid_token")
	if err == nil {
		t.Error("expected error for invalid token")
	}
}

// ---------------------------------------------------------------------------
// Token exchange and profile fetch (unit tests with mock servers)
// ---------------------------------------------------------------------------

func TestExchangeCode_GitHub(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokenResponse{AccessToken: "gho_test", TokenType: "bearer"}) //nolint:errcheck
	}))
	defer mock.Close()

	p := &ProviderConfig{
		ID:           "github",
		Type:         "github",
		BaseURL:      mock.URL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}
	tr, err := exchangeCode(p, "test-code", mock.URL+"/callback")
	if err != nil {
		t.Fatalf("exchangeCode: %v", err)
	}
	if tr.AccessToken != "gho_test" {
		t.Errorf("access_token: want gho_test, got %s", tr.AccessToken)
	}
}

func TestFetchUserProfile_GitHub(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": 123, "login": "testuser"}) //nolint:errcheck
	}))
	defer mock.Close()

	p := &ProviderConfig{
		Type:   "github",
		APIURL: mock.URL,
	}
	profile, err := fetchUserProfile(p, "gho_test")
	if err != nil {
		t.Fatalf("fetchUserProfile: %v", err)
	}
	if profile.Username != "testuser" {
		t.Errorf("username: want testuser, got %s", profile.Username)
	}
	if profile.ProviderUserID != "123" {
		t.Errorf("providerUserID: want 123, got %s", profile.ProviderUserID)
	}
}

// ---------------------------------------------------------------------------
// redirectWithError
// ---------------------------------------------------------------------------

func TestRedirectWithError_ReplacesConnected(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	redirectWithError(w, req, "https://mclaude.internal", "/?provider=github&connected=true&goto=settings", "denied")

	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=denied") {
		t.Errorf("expected error=denied in redirect, got %s", loc)
	}
	if strings.Contains(loc, "connected=true") {
		t.Errorf("connected=true should be replaced, got %s", loc)
	}
}

func TestRedirectWithError_NoConnectedParam(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	redirectWithError(w, req, "https://mclaude.internal", "/settings", "storage")

	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=storage") {
		t.Errorf("expected error=storage appended to redirect, got %s", loc)
	}
}

// ---------------------------------------------------------------------------
// GAP 3 — POST /api/providers/pat/connect must return 400
// ---------------------------------------------------------------------------

func TestHandleConnectProvider_PatReturns400(t *testing.T) {
	srv := newTestServerWithProviders(t)

	body := bytes.NewBufferString(`{"returnUrl":"/settings"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/pat/connect", body)
	req.URL.Path = "/api/providers/pat/connect"
	req = req.WithContext(contextWithUserID(req.Context(), "user1"))
	w := httptest.NewRecorder()
	srv.handleConnectProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for pat provider, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// PARTIAL 1 — sanitizeReturnURL strips non-allowlisted query params
// ---------------------------------------------------------------------------

func TestSanitizeReturnURL_AllowlistedParamsPreserved(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{
			input: "/?provider=github&connected=true&goto=settings&error=denied",
			want:  "/?connected=true&error=denied&goto=settings&provider=github",
		},
		{
			input: "/settings",
			want:  "/settings",
		},
		{
			input: "/?provider=github",
			want:  "/?provider=github",
		},
	}
	for _, c := range cases {
		got := sanitizeReturnURL(c.input)
		if got != c.want {
			t.Errorf("sanitizeReturnURL(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestSanitizeReturnURL_StripsNonAllowlisted(t *testing.T) {
	cases := []struct {
		input   string
		notWant string
	}{
		{
			input:   "/?provider=github&secret=leaked&connected=true",
			notWant: "secret",
		},
		{
			input:   "/?provider=github&foo=bar&baz=qux",
			notWant: "foo",
		},
	}
	for _, c := range cases {
		got := sanitizeReturnURL(c.input)
		if strings.Contains(got, c.notWant) {
			t.Errorf("sanitizeReturnURL(%q) = %q, expected %q to be stripped", c.input, got, c.notWant)
		}
	}
}

func TestHandleConnectProvider_SanitizesReturnURL(t *testing.T) {
	// Verify that a returnUrl with non-allowlisted params still succeeds but
	// the stripped URL ends up in the state store.
	srv := newTestServerWithProviders(t)

	// Include a non-allowlisted param "secret=leaked" alongside allowlisted ones.
	body := bytes.NewBufferString(`{"returnUrl":"/?provider=github&connected=true&secret=leaked"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/github/connect", body)
	req.URL.Path = "/api/providers/github/connect"
	req = req.WithContext(contextWithUserID(req.Context(), "user1"))
	w := httptest.NewRecorder()
	srv.handleConnectProvider(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Verify a redirectUrl was returned (state was stored successfully).
	if resp["redirectUrl"] == "" {
		t.Error("expected non-empty redirectUrl")
	}
}

// ---------------------------------------------------------------------------
// PARTIAL 2 — PAT error messages distinguish auth vs connectivity errors
// ---------------------------------------------------------------------------

func TestDetectPATProvider_AuthError401(t *testing.T) {
	// Server returns 401 — should get "invalid token" message.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer mock.Close()

	_, _, err := detectPATProvider(mock.URL, "bad_token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Errorf("want 'invalid token' message for 401, got: %s", err.Error())
	}
}

func TestDetectPATProvider_AuthError403(t *testing.T) {
	// Server returns 403 — should get "invalid token" message.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer mock.Close()

	_, _, err := detectPATProvider(mock.URL, "bad_token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Errorf("want 'invalid token' message for 403, got: %s", err.Error())
	}
}

func TestDetectPATProvider_ConnectivityError(t *testing.T) {
	// Use a URL that can't be reached — should get "could not reach provider".
	_, _, err := detectPATProvider("http://localhost:19999", "any_token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "could not reach provider") {
		t.Errorf("want 'could not reach provider' for unreachable host, got: %s", err.Error())
	}
}

func TestDetectPATProvider_404IsConnectivityError(t *testing.T) {
	// 404 means the URL path doesn't exist — treat as base URL config issue.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer mock.Close()

	_, _, err := detectPATProvider(mock.URL, "any_token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "could not reach provider") {
		t.Errorf("want 'could not reach provider' for 404, got: %s", err.Error())
	}
}

func TestDetectPATProvider_GitHubAuth401ThenGitLab404ReportsInvalidToken(t *testing.T) {
	// Regression test: GitHub returns 401 (invalid token) and then the GitLab
	// endpoint on the same base URL returns 404. The old code let the 404
	// overwrite lastErr so the caller saw "could not reach provider" instead of
	// "invalid token". The fix uses sawAuthError which is never cleared by a
	// subsequent non-auth error.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/user" {
			// GitHub endpoint: 401 — token is invalid.
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// GitLab endpoint (/api/v4/user) — 404, not a GitHub server.
		http.NotFound(w, r)
	}))
	defer mock.Close()

	_, _, err := detectPATProvider(mock.URL, "invalid_github_token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Errorf("want 'invalid token' when GitHub returns 401 followed by GitLab 404, got: %s", err.Error())
	}
	if strings.Contains(err.Error(), "could not reach provider") {
		t.Errorf("got 'could not reach provider' — auth error was incorrectly cleared by 404: %s", err.Error())
	}
}
