package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Break-glass tests exercise the admin port endpoints (:9091) without NATS.
// All tests use an in-process httptest.Server pointed at AdminMux.
// DB-backed tests are unit tests using a nil DB (expect 503) — the full
// DB-backed path is tested in the integration category.

// ---- AdminMux wiring ----

func TestAdminMux_MetricsEndpoint(t *testing.T) {
	srv := newTestServer(t)
	mux := srv.AdminMux()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer test-admin-token")
	mux.ServeHTTP(rec, req)

	// Metrics endpoint is implemented (not 404). Content verified in monitoring category.
	if rec.Code == http.StatusNotFound {
		t.Error("/metrics returned 404 — endpoint not registered on admin mux")
	}
}

// ---- Admin bearer token enforcement ----

func TestAdminMux_TokenRequired(t *testing.T) {
	srv := NewServer(nil, mustAccountKP(t), "nats://test", 0, "my-secret-token")
	mux := srv.AdminMux()

	cases := []struct {
		name   string
		path   string
		method string
		body   string
		bearer string
		want   int
	}{
		{
			name:   "no token GET users",
			path:   "/admin/users",
			method: "GET",
			want:   http.StatusForbidden,
		},
		{
			name:   "wrong token POST users",
			path:   "/admin/users",
			method: "POST",
			body:   `{"id":"u1","email":"a@b.com","name":"A"}`,
			bearer: "wrong-token",
			want:   http.StatusForbidden,
		},
		{
			name:   "correct token GET users (no DB → 503)",
			path:   "/admin/users",
			method: "GET",
			bearer: "my-secret-token",
			want:   http.StatusServiceUnavailable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body *bytes.Buffer
			if tc.body != "" {
				body = bytes.NewBufferString(tc.body)
			} else {
				body = bytes.NewBuffer(nil)
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, body)
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d; want %d", rec.Code, tc.want)
			}
		})
	}
}

// ---- Create user validation ----

func TestAdminCreateUser_MissingFields(t *testing.T) {
	srv := NewServer(nil, mustAccountKP(t), "nats://test", 0, "token")

	cases := []struct {
		name string
		body string
		want int
	}{
		{"empty body", `{}`, http.StatusBadRequest},
		{"missing name", `{"id":"u1","email":"a@b.com"}`, http.StatusBadRequest},
		{"missing email", `{"id":"u1","name":"A"}`, http.StatusBadRequest},
		{"missing id", `{"email":"a@b.com","name":"A"}`, http.StatusBadRequest},
		// DB nil → 503 (passes validation, hits DB)
		{"all fields no db", `{"id":"u1","email":"a@b.com","name":"A"}`, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/admin/users",
				bytes.NewBufferString(tc.body))
			req.Header.Set("Authorization", "Bearer token")
			srv.AdminMux().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d; want %d; body: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// ---- Delete user ----

func TestAdminDeleteUser_NilDB(t *testing.T) {
	srv := NewServer(nil, mustAccountKP(t), "nats://test", 0, "token")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/some-user-id", nil)
	req.Header.Set("Authorization", "Bearer token")
	srv.AdminMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503", rec.Code)
	}
}

// ---- Session stop ----

func TestAdminStopSession_MissingFields(t *testing.T) {
	srv := NewServer(nil, mustAccountKP(t), "nats://test", 0, "token")
	cases := []struct {
		name string
		body string
		want int
	}{
		{"empty body", `{}`, http.StatusBadRequest},
		{"missing projectId", `{"userId":"u","sessionId":"s"}`, http.StatusBadRequest},
		{"missing sessionId", `{"userId":"u","projectId":"p"}`, http.StatusBadRequest},
		{"missing userId", `{"projectId":"p","sessionId":"s"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/admin/sessions/stop",
				bytes.NewBufferString(tc.body))
			req.Header.Set("Authorization", "Bearer token")
			srv.AdminMux().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d; want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestAdminStopSession_ValidRequest(t *testing.T) {
	// Valid request with nil DB → 202 (best-effort break-glass).
	srv := NewServer(nil, mustAccountKP(t), "nats://test", 0, "token")
	body := `{"userId":"u1","projectId":"p1","sessionId":"s1"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/sessions/stop",
		bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer token")
	srv.AdminMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d; want 202", rec.Code)
	}
}

// ---- Unknown route on admin mux ----

func TestAdminMux_UnknownRoute(t *testing.T) {
	srv := NewServer(nil, mustAccountKP(t), "nats://test", 0, "token")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/unknown", nil)
	req.Header.Set("Authorization", "Bearer token")
	srv.AdminMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
	}
}

// ---- AdminUserResponse JSON shape ----

func TestAdminUserResponse_JSONShape(t *testing.T) {
	resp := AdminUserResponse{ID: "u1", Email: "a@b.com", Name: "Alice"}
	b, _ := json.Marshal(resp)
	body := string(b)
	for _, field := range []string{`"id"`, `"email"`, `"name"`} {
		if !strings.Contains(body, field) {
			t.Errorf("AdminUserResponse JSON missing field %s", field)
		}
	}
	// password_hash must NOT appear in admin user response.
	if strings.Contains(body, "password") {
		t.Error("AdminUserResponse must not expose password fields")
	}
}
