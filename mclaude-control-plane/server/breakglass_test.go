package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// breakglass_test.go exercises the main mux.
// Admin and SCIM routes have been removed per ADR-0101.

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
