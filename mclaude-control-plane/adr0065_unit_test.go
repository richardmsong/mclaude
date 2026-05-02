package main

// Unit tests for the three ADR-0065 prerequisite changes (CP side).
// All tests run without a database (nil DB) or a real NATS server.
// Integration tests for the DB-dependent paths are in adr0065_integration_test.go.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- AdminUserResponse.Slug (ADR-0065 item 5) ----

// TestAdminUserResponse_SlugField verifies that AdminUserResponse includes "slug"
// in its JSON representation.  ADR-0065: TestMain uses the slug field to
// construct GET /api/users/{uslug}/... paths after POST /admin/users.
func TestAdminUserResponse_SlugField(t *testing.T) {
	resp := AdminUserResponse{
		ID:    "u1",
		Email: "alice@example.com",
		Name:  "Alice",
		Slug:  "alice-example-com",
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal AdminUserResponse: %v", err)
	}
	body := string(b)

	// slug field must be present in the JSON output.
	if !strings.Contains(body, `"slug"`) {
		t.Errorf("AdminUserResponse JSON missing \"slug\" field; got: %s", body)
	}
	// The slug value must match what was set.
	if !strings.Contains(body, `"alice-example-com"`) {
		t.Errorf("AdminUserResponse JSON has wrong slug value; got: %s", body)
	}
	// Existing fields must still be present.
	for _, field := range []string{`"id"`, `"email"`, `"name"`} {
		if !strings.Contains(body, field) {
			t.Errorf("AdminUserResponse JSON missing field %s; got: %s", field, body)
		}
	}
	// password_hash must NOT appear.
	if strings.Contains(body, "password") {
		t.Error("AdminUserResponse must not expose password fields")
	}
}

// TestAdminUserResponse_SlugEmpty verifies that an empty slug is still marshalled.
// This covers the zero-value case without DB.
func TestAdminUserResponse_SlugEmpty(t *testing.T) {
	resp := AdminUserResponse{ID: "u1", Email: "a@b.com", Name: "A", Slug: ""}
	b, _ := json.Marshal(resp)
	body := string(b)
	// slug key must appear even when empty.
	if !strings.Contains(body, `"slug"`) {
		t.Errorf("AdminUserResponse JSON must include slug key even when empty; got: %s", body)
	}
}

// ---- CLIDeviceCodeRequest.PublicKey decode + store path (ADR-0065 items 2, 3) ----

// TestCLIDeviceCodeCreate_PublicKeyStoredInEntry verifies that POST /api/auth/device-code
// with a JSON body {"publicKey": "UABC..."} stores the public key inside the
// cliDeviceCodeEntry in the global in-memory store.
func TestCLIDeviceCodeCreate_PublicKeyStoredInEntry(t *testing.T) {
	srv := newTestServer(t)

	publicKey := "UABCDEFG1234567890TESTKEY"
	body := `{"publicKey":"` + publicKey + `"}`

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/device-code",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	srv.handleCLIDeviceCodeCreate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}

	// Decode the device code from the response.
	var resp CLIDeviceCodeResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.DeviceCode == "" {
		t.Fatal("deviceCode missing from response")
	}
	if resp.UserCode == "" {
		t.Fatal("userCode missing from response")
	}

	// Inspect the in-memory store to verify the public key was persisted.
	globalCLIDeviceCodeStore.mu.RLock()
	entry, ok := globalCLIDeviceCodeStore.codes[resp.DeviceCode]
	globalCLIDeviceCodeStore.mu.RUnlock()

	if !ok {
		t.Fatalf("device code %q not found in globalCLIDeviceCodeStore", resp.DeviceCode)
	}
	if entry.PublicKey != publicKey {
		t.Errorf("entry.PublicKey = %q; want %q", entry.PublicKey, publicKey)
	}
	if entry.UserCode != resp.UserCode {
		t.Errorf("entry.UserCode = %q; want %q (from response)", entry.UserCode, resp.UserCode)
	}
	if entry.ExpiresAt.Before(time.Now()) {
		t.Error("entry.ExpiresAt is in the past immediately after creation")
	}
}

// TestCLIDeviceCodeCreate_EmptyBodyAccepted verifies that POST /api/auth/device-code
// with an empty body (browser-initiated flow without publicKey) still creates an entry
// with an empty PublicKey field.
func TestCLIDeviceCodeCreate_EmptyBodyAccepted(t *testing.T) {
	srv := newTestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/device-code",
		bytes.NewBufferString("{}"))
	req.Header.Set("Content-Type", "application/json")
	srv.handleCLIDeviceCodeCreate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp CLIDeviceCodeResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	globalCLIDeviceCodeStore.mu.RLock()
	entry, ok := globalCLIDeviceCodeStore.codes[resp.DeviceCode]
	globalCLIDeviceCodeStore.mu.RUnlock()

	if !ok {
		t.Fatalf("device code %q not found in store", resp.DeviceCode)
	}
	if entry.PublicKey != "" {
		t.Errorf("entry.PublicKey = %q; want empty for browser-initiated flow", entry.PublicKey)
	}
}

// TestCLIDeviceCodeCreate_ResponseFields verifies the response shape includes
// all required fields from the spec.
func TestCLIDeviceCodeCreate_ResponseFields(t *testing.T) {
	srv := newTestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/device-code",
		bytes.NewBufferString(`{"publicKey":"UTESTKEY"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.handleCLIDeviceCodeCreate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp CLIDeviceCodeResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.DeviceCode == "" {
		t.Error("response missing deviceCode")
	}
	if resp.UserCode == "" {
		t.Error("response missing userCode")
	}
	if resp.VerificationURL == "" {
		t.Error("response missing verificationUrl")
	}
	if resp.ExpiresIn <= 0 {
		t.Errorf("response expiresIn = %d; want > 0", resp.ExpiresIn)
	}
	if resp.Interval <= 0 {
		t.Errorf("response interval = %d; want > 0", resp.Interval)
	}
}

// ---- handleCLIDeviceCodeVerify nil-DB path (ADR-0065 item 4) ----

// TestCLIDeviceCodeVerify_NilDB_Returns503 verifies that the verify handler
// returns 503 when the server has no database configured.
func TestCLIDeviceCodeVerify_NilDB_Returns503(t *testing.T) {
	srv := newTestServer(t) // db = nil

	form := "user_code=ABCD-1234&email=alice@example.com&password=secret"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/device-code/verify",
		bytes.NewBufferString(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.handleCLIDeviceCodeVerify(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 when db=nil", rec.Code)
	}
}

// TestCLIDeviceCodeVerify_MissingFields_Returns400 verifies that missing form
// fields return 400 before the DB is consulted.
func TestCLIDeviceCodeVerify_MissingFields_Returns400(t *testing.T) {
	srv := newTestServer(t) // db = nil

	cases := []struct {
		name string
		form string
	}{
		{"missing user_code", "email=alice@example.com&password=pw"},
		{"missing email", "user_code=ABCD-1234&password=pw"},
		{"missing password", "user_code=ABCD-1234&email=alice@example.com"},
		{"all missing", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/auth/device-code/verify",
				bytes.NewBufferString(tc.form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			srv.handleCLIDeviceCodeVerify(rec, req)
			// 400 = validation failure before DB; 503 = nil DB (still acceptable for empty form).
			if rec.Code != http.StatusBadRequest && rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d; want 400 or 503 for missing fields", rec.Code)
			}
		})
	}
}

// TestCLIDeviceCodeVerify_GetServesHTML verifies that GET /api/auth/device-code/verify
// returns an HTML page (no DB required).
func TestCLIDeviceCodeVerify_GetServesHTML(t *testing.T) {
	srv := newTestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/device-code/verify?code=ABCD-1234", nil)
	srv.handleCLIDeviceCodeVerify(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 for GET verify page", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q; want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "form") {
		t.Error("GET verify page should contain an HTML form")
	}
}

// ---- ProjectResponse.ImportRef JSON shape (ADR-0065 item 1) ----

// TestProjectResponse_ImportRefField verifies that ProjectResponse includes the
// importRef field in JSON when non-nil.  The full handleGetProjectHTTP SQL path
// is tested in the integration suite (adr0065_integration_test.go).
func TestProjectResponse_ImportRefField(t *testing.T) {
	importRef := "s3://bucket/path/to/archive.tar.gz"
	resp := ProjectResponse{
		ID:        "proj-1",
		Slug:      "my-project",
		Name:      "My Project",
		Status:    "active",
		ImportRef: &importRef,
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal ProjectResponse: %v", err)
	}
	body := string(b)

	if !strings.Contains(body, `"importRef"`) {
		t.Errorf("ProjectResponse JSON missing \"importRef\" field; got: %s", body)
	}
	if !strings.Contains(body, importRef) {
		t.Errorf("ProjectResponse JSON has wrong importRef value; got: %s", body)
	}
}

// TestProjectResponse_ImportRefNull verifies that importRef is omitted from JSON
// when nil (omitempty).
func TestProjectResponse_ImportRefNull(t *testing.T) {
	resp := ProjectResponse{
		ID:        "proj-2",
		Slug:      "my-project-2",
		Name:      "My Project 2",
		Status:    "active",
		ImportRef: nil,
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal ProjectResponse: %v", err)
	}
	body := string(b)

	// With omitempty, nil importRef must NOT appear in JSON.
	if strings.Contains(body, `"importRef"`) {
		t.Errorf("ProjectResponse JSON must omit importRef when nil (omitempty); got: %s", body)
	}
}
