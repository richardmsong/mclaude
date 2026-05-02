//go:build integration

// Integration tests for ADR-0065 prerequisite changes (CP side).
// Require Docker (Postgres + NATS via testcontainers).
// Run with: go test -tags integration ./...

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nkeys"
)

// ---- handleGetProjectHTTP includes importRef (ADR-0065 item 1) ----

// TestIntegration_HandleGetProjectHTTP_ImportRefNull verifies that
// GET /api/users/{uslug}/projects/{pslug} returns importRef: null (omitted)
// when the project has no import pending.  Relies on the SQL query in
// handleGetProjectHTTP selecting import_ref.
func TestIntegration_HandleGetProjectHTTP_ImportRefNull(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	// Create a user and project.
	userID := "adr0065-get-proj-u1"
	_, err := db.CreateUser(ctx, userID, "adr0065projnull@example.com", "ADR0065ProjNull", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	proj, err := db.CreateProject(ctx, "adr0065-proj-p1", userID, "Test Project Null Import", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	accountKP, _ := nkeys.CreateAccount()
	srv := NewServer(db, accountKP, "nats://localhost:4222", "", 8*time.Hour, "admin")

	// Issue a JWT for the user so the auth middleware provides userID.
	jwt, _, err := IssueUserJWTLegacy(userID, proj.Slug, accountKP, int64(8*time.Hour.Seconds()))
	if err != nil {
		t.Fatalf("IssueUserJWTLegacy: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	url := "/api/users/adr0065projnull-example-com/projects/" + proj.Slug
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	var resp map[string]interface{}
	if err := json.NewDecoder(bytes.NewBufferString(body)).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// importRef must be absent (omitempty + nil) when no import is pending.
	if _, ok := resp["importRef"]; ok {
		t.Errorf("importRef should be absent in JSON when nil; got body: %s", body)
	}

	// Core fields must be present.
	if resp["slug"] != proj.Slug {
		t.Errorf("slug = %q; want %q", resp["slug"], proj.Slug)
	}
}

// TestIntegration_HandleGetProjectHTTP_ImportRefNonNull verifies that
// GET /api/users/{uslug}/projects/{pslug} returns importRef as a non-null string
// when the project has an active import reference set.
func TestIntegration_HandleGetProjectHTTP_ImportRefNonNull(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	userID := "adr0065-get-proj-u2"
	_, err := db.CreateUser(ctx, userID, "adr0065projref@example.com", "ADR0065ProjRef", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	proj, err := db.CreateProject(ctx, "adr0065-proj-p2", userID, "Test Project With Import", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Set an import_ref to simulate an in-progress import.
	importRef := "adr0065-import-ref-abc123"
	if err := db.SetProjectImportRef(ctx, proj.ID, importRef); err != nil {
		t.Fatalf("SetProjectImportRef: %v", err)
	}

	accountKP, _ := nkeys.CreateAccount()
	srv := NewServer(db, accountKP, "nats://localhost:4222", "", 8*time.Hour, "admin")

	jwt, _, err := IssueUserJWTLegacy(userID, proj.Slug, accountKP, int64(8*time.Hour.Seconds()))
	if err != nil {
		t.Fatalf("IssueUserJWTLegacy: %v", err)
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	url := "/api/users/adr0065projref-example-com/projects/" + proj.Slug
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	var resp map[string]interface{}
	if err := json.NewDecoder(bytes.NewBufferString(body)).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// importRef must be present and equal to what was set.
	gotRef, ok := resp["importRef"]
	if !ok {
		t.Fatalf("importRef missing from response body: %s", body)
	}
	if gotRef != importRef {
		t.Errorf("importRef = %q; want %q", gotRef, importRef)
	}
}

// ---- handleCLIDeviceCodeVerify fallback chain (ADR-0065 item 4) ----

// setupDeviceCodeEntry creates a fresh entry in the global CLI device code store
// and returns the device code and user code.  Cleans up after the test.
func setupDeviceCodeEntry(t *testing.T, publicKey string) (deviceCode, userCode string) {
	t.Helper()
	dc, err := generateCLIDeviceCode()
	if err != nil {
		t.Fatalf("generateCLIDeviceCode: %v", err)
	}
	uc, err := generateUserCode()
	if err != nil {
		t.Fatalf("generateUserCode: %v", err)
	}
	globalCLIDeviceCodeStore.mu.Lock()
	globalCLIDeviceCodeStore.codes[dc] = &cliDeviceCodeEntry{
		UserCode:  uc,
		ExpiresAt: time.Now().Add(15 * time.Minute),
		PublicKey: publicKey,
	}
	globalCLIDeviceCodeStore.mu.Unlock()
	t.Cleanup(func() {
		globalCLIDeviceCodeStore.mu.Lock()
		delete(globalCLIDeviceCodeStore.codes, dc)
		globalCLIDeviceCodeStore.mu.Unlock()
	})
	return dc, uc
}

// submitVerifyForm posts the device-code verify form to the handler.
func submitVerifyForm(srv *Server, userCode, email, password string) *httptest.ResponseRecorder {
	form := "user_code=" + userCode + "&email=" + email + "&password=" + password
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/device-code/verify",
		bytes.NewBufferString(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.handleCLIDeviceCodeVerify(rec, req)
	return rec
}

// TestIntegration_CLIDeviceCodeVerify_PublicKeyFromEntry verifies ADR-0065 item 4:
// when the entry has PublicKey != "", the JWT is issued using that key (the
// CLI-generated NKey public key), not a CP-generated one.
func TestIntegration_CLIDeviceCodeVerify_PublicKeyFromEntry(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	// Create a test user with a known password.
	userID := "adr0065-verify-u1"
	email := "adr0065verify1@example.com"
	password := "test-password-verify-1"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	_, err = db.CreateUser(ctx, userID, email, "ADR0065Verify1", hash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Generate a user NKey pair (simulating CLI generating its own key).
	userKP, _, err := GenerateUserNKey()
	if err != nil {
		t.Fatalf("GenerateUserNKey: %v", err)
	}

	// Create a device code entry with the CLI's NKey public key.
	_, userCode := setupDeviceCodeEntry(t, userKP.PublicKey)

	accountKP, _ := nkeys.CreateAccount()
	accountPub, _ := accountKP.PublicKey()
	srv := NewServer(db, accountKP, "nats://localhost:4222", "", 8*time.Hour, "admin")

	// Submit the verify form.
	rec := submitVerifyForm(srv, userCode, email, password)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status = %d; body: %s", rec.Code, rec.Body.String())
	}

	// The device code entry should now be completed with a JWT.
	// Find the entry by user code.
	var completedEntry *cliDeviceCodeEntry
	globalCLIDeviceCodeStore.mu.RLock()
	for _, e := range globalCLIDeviceCodeStore.codes {
		if e.UserCode == userCode && e.Completed {
			completedEntry = e
			break
		}
	}
	globalCLIDeviceCodeStore.mu.RUnlock()

	if completedEntry == nil {
		t.Fatal("device code entry not marked as completed after successful verify")
	}
	if completedEntry.JWT == "" {
		t.Fatal("device code entry has empty JWT after successful verify")
	}
	if completedEntry.UserID != userID {
		t.Errorf("entry.UserID = %q; want %q", completedEntry.UserID, userID)
	}

	// The JWT must be issued for the CLI's NKey public key, not a CP-generated one.
	claims, err := DecodeUserJWT(completedEntry.JWT, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}
	if claims.Subject != userKP.PublicKey {
		t.Errorf("JWT subject = %q; want CLI's NKey public key %q",
			claims.Subject, userKP.PublicKey)
	}
}

// TestIntegration_CLIDeviceCodeVerify_FallbackToNKeyPublic verifies ADR-0065 item 4:
// when the entry has PublicKey == "" but user.NKeyPublic is set, the JWT is issued
// using user.NKeyPublic (NKey registered from a previous login).
func TestIntegration_CLIDeviceCodeVerify_FallbackToNKeyPublic(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	userID := "adr0065-verify-u2"
	email := "adr0065verify2@example.com"
	password := "test-password-verify-2"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	_, err = db.CreateUser(ctx, userID, email, "ADR0065Verify2", hash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Simulate a previously registered NKey (from a prior login).
	storedKP, _, err := GenerateUserNKey()
	if err != nil {
		t.Fatalf("GenerateUserNKey: %v", err)
	}
	if err := db.SetUserNKeyPublic(ctx, userID, storedKP.PublicKey); err != nil {
		t.Fatalf("SetUserNKeyPublic: %v", err)
	}

	// Create a device code entry WITHOUT a public key (browser-initiated flow).
	_, userCode := setupDeviceCodeEntry(t, "" /* no publicKey in entry */)

	accountKP, _ := nkeys.CreateAccount()
	accountPub, _ := accountKP.PublicKey()
	srv := NewServer(db, accountKP, "nats://localhost:4222", "", 8*time.Hour, "admin")

	rec := submitVerifyForm(srv, userCode, email, password)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status = %d; body: %s", rec.Code, rec.Body.String())
	}

	var completedEntry *cliDeviceCodeEntry
	globalCLIDeviceCodeStore.mu.RLock()
	for _, e := range globalCLIDeviceCodeStore.codes {
		if e.UserCode == userCode && e.Completed {
			completedEntry = e
			break
		}
	}
	globalCLIDeviceCodeStore.mu.RUnlock()

	if completedEntry == nil {
		t.Fatal("device code entry not marked as completed after successful verify")
	}
	if completedEntry.JWT == "" {
		t.Fatal("device code entry has empty JWT — fallback to user.NKeyPublic should have issued JWT")
	}

	// The JWT must be issued using the stored NKeyPublic.
	claims, err := DecodeUserJWT(completedEntry.JWT, accountPub)
	if err != nil {
		t.Fatalf("DecodeUserJWT: %v", err)
	}
	if claims.Subject != storedKP.PublicKey {
		t.Errorf("JWT subject = %q; want stored NKeyPublic %q",
			claims.Subject, storedKP.PublicKey)
	}
}

// TestIntegration_CLIDeviceCodeVerify_NoPublicKey_Returns400 verifies ADR-0065 item 4:
// when the entry has PublicKey == "" AND user.NKeyPublic is nil, the handler
// returns HTTP 400 (cannot issue a JWT without any public key).
func TestIntegration_CLIDeviceCodeVerify_NoPublicKey_Returns400(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	userID := "adr0065-verify-u3"
	email := "adr0065verify3@example.com"
	password := "test-password-verify-3"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	_, err = db.CreateUser(ctx, userID, email, "ADR0065Verify3", hash)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { db.DeleteUser(ctx, userID) }) //nolint:errcheck

	// Do NOT call SetUserNKeyPublic — user.NKeyPublic remains nil.
	// Create a device code entry WITHOUT a public key.
	_, userCode := setupDeviceCodeEntry(t, "" /* no publicKey in entry */)

	accountKP, _ := nkeys.CreateAccount()
	srv := NewServer(db, accountKP, "nats://localhost:4222", "", 8*time.Hour, "admin")

	rec := submitVerifyForm(srv, userCode, email, password)

	// Must return 400: no public key available for JWT issuance.
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 when no public key available; body: %s",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "public key") {
		t.Errorf("body should mention missing public key; got: %s", rec.Body.String())
	}
}

// TestIntegration_AdminCreateUser_SlugInResponse verifies that POST /admin/users
// returns the user's slug in the response body (ADR-0065: TestMain needs the slug
// to construct /api/users/{uslug}/... paths).
func TestIntegration_AdminCreateUser_SlugInResponse(t *testing.T) {
	ctx := context.Background()
	db := mustConnectDB(t, ctx)

	accountKP, _ := nkeys.CreateAccount()
	srv := NewServer(db, accountKP, "nats://localhost:4222", "", 8*time.Hour, "test-admin-token")

	email := "adr0065-admin-slug@example.com"
	body := `{"id":"adr0065-admin-slug-u1","email":"` + email + `","name":"ADR0065AdminSlug"}`

	t.Cleanup(func() { db.DeleteUser(ctx, "adr0065-admin-slug-u1") }) //nolint:errcheck

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/users",
		bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer test-admin-token")
	req.Header.Set("Content-Type", "application/json")
	srv.AdminMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d; want 201; body: %s", rec.Code, rec.Body.String())
	}

	var resp AdminUserResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Slug must be derived from the full email (ADR-0062).
	// adr0065-admin-slug@example.com → adr0065-admin-slug-example-com
	expectedSlug := "adr0065-admin-slug-example-com"
	if resp.Slug != expectedSlug {
		t.Errorf("response slug = %q; want %q", resp.Slug, expectedSlug)
	}
	if resp.ID != "adr0065-admin-slug-u1" {
		t.Errorf("response id = %q; want adr0065-admin-slug-u1", resp.ID)
	}
	if resp.Email != email {
		t.Errorf("response email = %q; want %q", resp.Email, email)
	}
}
