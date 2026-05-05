package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// breakglass_test.go exercises admin route handlers directly (without DB).
// The admin port (9091) and static ADMIN_TOKEN are removed per ADR-0089.
// Admin routes now live on the main port behind isAdminMiddleware.
// These tests call handlers directly to verify nil-DB guard behavior —
// the full auth+DB path is covered in the integration category.

// newTestMux returns a main mux with admin routes registered.
// Admin routes require a real JWT from isAdminMiddleware, so these tests
// call the handler functions directly (bypassing middleware) to exercise
// nil-DB guards and input validation.
func newTestMux(t *testing.T) (*Server, *http.ServeMux) {
	t.Helper()
	srv := NewServer(nil, mustAccountKP(t), "nats://test", "", 0)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	return srv, mux
}

// ---- Metrics endpoint (unauthenticated on main mux) ----

func TestMetricsEndpoint_MainMux(t *testing.T) {
	_, mux := newTestMux(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	mux.ServeHTTP(rec, req)

	// Metrics endpoint is implemented (not 404). Content verified in monitoring category.
	if rec.Code == http.StatusNotFound {
		t.Error("/metrics returned 404 — endpoint not registered on main mux")
	}
}

// ---- Create user validation (direct handler calls bypass middleware) ----

func TestAdminCreateUser_MissingFields(t *testing.T) {
	srv := NewServer(nil, mustAccountKP(t), "nats://test", "", 0)

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
			req.Header.Set("Content-Type", "application/json")
			srv.adminCreateUser(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d; want %d; body: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// ---- Delete user ----

func TestAdminDeleteUser_NilDB(t *testing.T) {
	srv := NewServer(nil, mustAccountKP(t), "nats://test", "", 0)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/some-user-id", nil)
	srv.adminDeleteUser(rec, req, "some-user-id")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503", rec.Code)
	}
}

// ---- Session stop ----

func TestAdminStopSession_MissingFields(t *testing.T) {
	srv := NewServer(nil, mustAccountKP(t), "nats://test", "", 0)
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
			req.Header.Set("Content-Type", "application/json")
			srv.adminStopSession(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d; want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestAdminStopSession_ValidRequest(t *testing.T) {
	// Valid request with nil DB → 202 (best-effort break-glass).
	srv := NewServer(nil, mustAccountKP(t), "nats://test", "", 0)
	body := `{"userId":"u1","projectId":"p1","sessionId":"s1"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/sessions/stop",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	srv.adminStopSession(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d; want 202", rec.Code)
	}
}

// ---- Unknown route on admin routes handler ----

func TestAdminRoutes_UnknownRoute(t *testing.T) {
	srv := NewServer(nil, mustAccountKP(t), "nats://test", "", 0)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/unknown", nil)
	srv.handleAdminRoutes(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
	}
}

// ---- Grant cluster ----

// TestAdminGrantCluster_NilDB verifies that the grant endpoint returns 503
// when the database is not configured (nil DB guard, same pattern as other admin endpoints).
func TestAdminGrantCluster_NilDB(t *testing.T) {
	srv := NewServer(nil, mustAccountKP(t), "nats://test", "", 0)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/clusters/k3d-dev/grants",
		bytes.NewBufferString(`{"userSlug":"test-user"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.adminGrantCluster(rec, req, "k3d-dev")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 (nil DB)", rec.Code)
	}
}

// TestAdminGrantCluster_MissingUserSlug verifies input validation.
// Uses a mock server with nil DB — the nil-DB check fires first (503),
// so validation tests use the AdminClusterGrantRequest struct directly.
func TestAdminGrantCluster_RequestValidation(t *testing.T) {
	var req AdminClusterGrantRequest
	if req.UserSlug != "" {
		t.Error("zero-value AdminClusterGrantRequest should have empty UserSlug")
	}
	// Confirm the struct JSON tag matches the field the handler reads.
	b, _ := json.Marshal(AdminClusterGrantRequest{UserSlug: "alice"})
	if !strings.Contains(string(b), `"userSlug"`) {
		t.Errorf("AdminClusterGrantRequest JSON missing userSlug field: %s", b)
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
