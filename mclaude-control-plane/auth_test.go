package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nkeys"
)

// ---- Password hashing ----

func TestHashPassword_RoundTrip(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !bcryptCheck("correct-horse-battery-staple", hash) {
		t.Error("bcryptCheck failed for correct password")
	}
	if bcryptCheck("wrong-password", hash) {
		t.Error("bcryptCheck returned true for wrong password")
	}
}

func TestHashPassword_EmptyString(t *testing.T) {
	// Empty password hashes shouldn't bypass auth — but we do allow hashing it.
	// Login will reject SSO-only accounts (empty hash) before bcrypt is called.
	_, err := HashPassword("")
	if err != nil {
		t.Fatalf("HashPassword empty: %v", err)
	}
}

func TestHashPassword_Unique(t *testing.T) {
	h1, _ := HashPassword("samepassword")
	h2, _ := HashPassword("samepassword")
	// bcrypt uses random salt — same input, different hash.
	if h1 == h2 {
		t.Error("two hashes of the same password should be different (bcrypt salting)")
	}
}

// ---- Login handler ----

func newTestServer(t *testing.T) *Server {
	t.Helper()
	accountKP, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatalf("create account key: %v", err)
	}
	return NewServer(nil, accountKP, "nats://localhost:4222", "", 8*time.Hour, "test-admin-token")
}

func TestHandleLogin_MissingFields(t *testing.T) {
	srv := newTestServer(t)
	cases := []struct {
		name string
		body string
		want int
	}{
		{"empty body", `{}`, http.StatusBadRequest},
		{"missing password", `{"email":"a@b.com"}`, http.StatusBadRequest},
		{"missing email", `{"password":"pw"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/auth/login",
				bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			srv.handleLogin(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d; want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestHandleLogin_NilDB(t *testing.T) {
	// Server with nil DB should return 503 Service Unavailable, not panic.
	srv := newTestServer(t) // db=nil
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login",
		bytes.NewBufferString(`{"email":"nobody@example.com","password":"pw"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.handleLogin(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 when DB not configured", rec.Code)
	}
}

func TestHandleLogin_InvalidJSON(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login",
		bytes.NewBufferString("not json"))
	srv.handleLogin(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", rec.Code)
	}
}

// ---- Refresh handler ----

func TestHandleRefresh_NoToken(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	srv.handleRefresh(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rec.Code)
	}
}

func TestHandleRefresh_MalformedToken(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	req.Header.Set("Authorization", "Bearer not.a.jwt")
	srv.handleRefresh(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rec.Code)
	}
}

// TestHandleRefresh_NilDB verifies that handleRefresh returns 503 when the
// database is not configured. Full refresh happy-path (with real DB) is covered
// by TestIntegration_HandleRefresh_ReturnsSlug in integration_test.go.
func TestHandleRefresh_NilDB(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()
	srv := NewServer(nil, accountKP, "nats://localhost:4222", "", 8*time.Hour, "admin")

	expiresAt := time.Now().Add(8 * time.Hour).Unix()
	jwt, _, err := IssueUserJWT("refresh-user-uuid", "refresh-user-slug", accountKP, expiresAt)
	if err != nil {
		t.Fatalf("IssueUserJWT: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.handleRefresh(rec, req)

	// db=nil → service unavailable
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 when db=nil", rec.Code)
	}
}

func TestHandleRefresh_WrongAccountKey(t *testing.T) {
	// JWT signed by accountA, server has accountB → should reject.
	accountA, _ := nkeys.CreateAccount()
	accountB, _ := nkeys.CreateAccount()
	srv := NewServer(nil, accountB, "nats://localhost:4222", "", time.Hour, "admin")

	expiresAt := time.Now().Add(time.Hour).Unix()
	jwt, _, _ := IssueUserJWT("user", "user-slug", accountA, expiresAt)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.handleRefresh(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401 for wrong key", rec.Code)
	}
}

// ---- Auth middleware ----

func TestAuthMiddleware_NoToken(t *testing.T) {
	srv := newTestServer(t)
	called := false
	handler := srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	handler.ServeHTTP(rec, req)
	if called {
		t.Error("handler should not be called without token")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rec.Code)
	}
}

func TestAuthMiddleware_ValidToken_InjectsUserID(t *testing.T) {
	accountKP, _ := nkeys.CreateAccount()
	srv := NewServer(nil, accountKP, "nats://test", "", time.Hour, "admin")

	expiresAt := time.Now().Add(time.Hour).Unix()
	jwt, _, _ := IssueUserJWT("middleware-user", "middleware-slug", accountKP, expiresAt)

	var gotUserID string
	handler := srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserID = r.Context().Value(contextKeyUserID).(string)
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", rec.Code)
	}
	if gotUserID != "middleware-user" {
		t.Errorf("userID = %q; want middleware-user", gotUserID)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	srv := newTestServer(t)
	called := false
	handler := srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Header.Set("Authorization", "Bearer INVALID")
	handler.ServeHTTP(rec, req)
	if called {
		t.Error("handler should not be called for invalid token")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rec.Code)
	}
}

// ---- Admin middleware ----

func TestAdminAuthMiddleware_Enforced(t *testing.T) {
	srv := NewServer(nil, mustAccountKP(t), "nats://test", "", time.Hour, "secret-admin-token")
	called := false
	handler := srv.adminAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name   string
		bearer string
		want   int
	}{
		{"no token", "", http.StatusForbidden},
		{"wrong token", "wrong", http.StatusForbidden},
		{"correct token", "secret-admin-token", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d; want %d", rec.Code, tc.want)
			}
			if tc.want == http.StatusOK && !called {
				t.Error("handler not called for valid admin token")
			}
		})
	}
}

// ---- bearer token extraction ----

func TestBearerToken(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"Bearer abc123", "abc123"},
		{"bearer abc123", ""},   // case-sensitive
		{"Basic abc123", ""},    // wrong scheme
		{"", ""},                // no header
		{"Bearer ", ""},         // empty token → empty string (valid to extract, invalid JWT)
	}
	for _, tc := range cases {
		req := &http.Request{Header: http.Header{}}
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		got := bearerToken(req)
		if got != tc.want {
			t.Errorf("bearerToken(%q) = %q; want %q", tc.header, got, tc.want)
		}
	}
}

// ---- Route registration smoke test ----

func TestRegisterRoutes_HealthEndpoint(t *testing.T) {
	srv := newTestServer(t)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /health status = %d; want 200", rec.Code)
	}
}

func TestRegisterRoutes_LoginRouteExists(t *testing.T) {
	srv := newTestServer(t)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// POST /auth/login with empty body should be 400 (not 404).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login",
		bytes.NewBufferString("{}"))
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Error("POST /auth/login returned 404 — route not registered")
	}
}

// ---- SSO stub (validates structure) ----

func TestLoginResponse_NATSURLField(t *testing.T) {
	// LoginResponse must carry natsUrl so clients know where to connect NATS.
	resp := LoginResponse{
		NATSUrl:   "wss://mclaude.example.com/nats",
		JWT:       "test.jwt.value",
		NKeySeed:  "SUABC123",
		UserID:    "user-001",
		ExpiresAt: time.Now().Add(8 * time.Hour).Unix(),
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(b)
	for _, field := range []string{`"natsUrl"`, `"jwt"`, `"nkeySeed"`, `"userId"`, `"expiresAt"`} {
		if !strings.Contains(body, field) {
			t.Errorf("LoginResponse JSON missing field %s", field)
		}
	}
}

// ---- helpers ----

func mustAccountKP(t *testing.T) nkeys.KeyPair {
	t.Helper()
	kp, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatalf("create account kp: %v", err)
	}
	return kp
}

