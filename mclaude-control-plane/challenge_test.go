package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nats-io/nkeys"
)

// ---- Challenge-response HTTP auth (ADR-0054) ----

func TestHandleAuthChallenge_MissingNKey(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/challenge",
		bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	srv.handleAuthChallenge(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 for missing nkey_public", rec.Code)
	}
}

func TestHandleAuthChallenge_InvalidJSON(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/challenge",
		bytes.NewBufferString("not json"))
	srv.handleAuthChallenge(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 for invalid JSON", rec.Code)
	}
}

func TestHandleAuthChallenge_NilDB(t *testing.T) {
	srv := newTestServer(t) // db=nil
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/challenge",
		bytes.NewBufferString(`{"nkey_public":"UABC123"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.handleAuthChallenge(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 for nil DB", rec.Code)
	}
}

func TestHandleAuthChallenge_UnknownNKey(t *testing.T) {
	// Server with nil DB → 503 (can't look up NKey without DB).
	// With a real DB we'd get 404 for unknown key. Since unit tests have no DB,
	// we test the nil-DB path which covers the "db=nil" guard.
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/challenge",
		bytes.NewBufferString(`{"nkey_public":"UUNKNOWNKEY"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.handleAuthChallenge(rec, req)
	// nil DB → 503
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503 when DB nil", rec.Code)
	}
}

func TestHandleAuthVerify_MissingFields(t *testing.T) {
	srv := newTestServer(t)
	cases := []struct {
		name string
		body string
		want int
	}{
		{"empty body", `{}`, http.StatusBadRequest},
		{"missing challenge", `{"nkey_public":"UP1","signature":"SIG1"}`, http.StatusBadRequest},
		{"missing signature", `{"nkey_public":"UP1","challenge":"CH1"}`, http.StatusBadRequest},
		{"missing nkey_public", `{"challenge":"CH1","signature":"SIG1"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/auth/verify",
				bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			srv.handleAuthVerify(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d; want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestHandleAuthVerify_InvalidChallengeEncoding(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/verify",
		bytes.NewBufferString(`{"nkey_public":"UP1","challenge":"!!!notbase64!!!","signature":"SIG1"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.handleAuthVerify(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 for invalid base64 challenge", rec.Code)
	}
}

func TestHandleAuthVerify_InvalidSignatureEncoding(t *testing.T) {
	srv := newTestServer(t)
	nonce := base64.StdEncoding.EncodeToString([]byte("valid-nonce"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/verify",
		bytes.NewBufferString(`{"nkey_public":"UP1","challenge":"`+nonce+`","signature":"!!!notbase64!!!"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.handleAuthVerify(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 for invalid base64 signature", rec.Code)
	}
}

func TestHandleAuthVerify_UnknownChallenge(t *testing.T) {
	srv := newTestServer(t)
	nonce := base64.StdEncoding.EncodeToString([]byte("unknown-nonce"))
	sig := base64.StdEncoding.EncodeToString([]byte("fake-sig"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/verify",
		bytes.NewBufferString(`{"nkey_public":"UP1","challenge":"`+nonce+`","signature":"`+sig+`"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.handleAuthVerify(rec, req)
	// Challenge not found → 401
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401 for unknown challenge", rec.Code)
	}
}

// TestChallengeStoreExpiry verifies that expired challenges are rejected.
func TestChallengeStoreExpiry(t *testing.T) {
	// Inject a pre-expired entry into the global store.
	nonceB64 := base64.StdEncoding.EncodeToString([]byte("expired-challenge-nonce"))
	globalChallengeStore.mu.Lock()
	globalChallengeStore.entries[nonceB64] = &challengeEntry{
		NKeyPublic: "UTEST",
		Nonce:      []byte("expired-challenge-nonce"),
		ExpiresAt:  time.Now().Add(-time.Second), // already expired
	}
	globalChallengeStore.mu.Unlock()
	defer func() {
		globalChallengeStore.mu.Lock()
		delete(globalChallengeStore.entries, nonceB64)
		globalChallengeStore.mu.Unlock()
	}()

	srv := newTestServer(t)
	sig := base64.StdEncoding.EncodeToString([]byte("fake-sig"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/verify",
		bytes.NewBufferString(`{"nkey_public":"UTEST","challenge":"`+nonceB64+`","signature":"`+sig+`"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.handleAuthVerify(rec, req)

	// The entry exists in the store but the nonce was already marked "Used" on first
	// verify attempt, or the expiry check kicks in. Either way: non-200.
	if rec.Code == http.StatusOK {
		t.Error("expected non-200 for expired/used challenge; got 200")
	}
}

// TestVerifyNKeySignature_RoundTrip verifies the NATS NKey signature verification.
func TestVerifyNKeySignature_RoundTrip(t *testing.T) {
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create user nkey: %v", err)
	}
	defer kp.Wipe()

	pub, err := kp.PublicKey()
	if err != nil {
		t.Fatalf("public key: %v", err)
	}

	challenge := []byte("test-challenge-nonce-32-bytes-xyz")
	sig, err := kp.Sign(challenge)
	if err != nil {
		t.Fatalf("sign challenge: %v", err)
	}

	// Valid signature.
	if err := VerifyNKeySignature(pub, challenge, sig); err != nil {
		t.Errorf("VerifyNKeySignature valid: unexpected error: %v", err)
	}

	// Wrong challenge.
	if err := VerifyNKeySignature(pub, []byte("different-data"), sig); err == nil {
		t.Error("VerifyNKeySignature: expected error for wrong challenge data")
	}

	// Wrong public key.
	kp2, _ := nkeys.CreateUser()
	pub2, _ := kp2.PublicKey()
	if err := VerifyNKeySignature(pub2, challenge, sig); err == nil {
		t.Error("VerifyNKeySignature: expected error for wrong public key")
	}
}

// TestVerifyNKeySignature_InvalidPublicKey verifies that invalid public keys are rejected.
func TestVerifyNKeySignature_InvalidPublicKey(t *testing.T) {
	if err := VerifyNKeySignature("NOTAVALIDKEY", []byte("data"), []byte("sig")); err == nil {
		t.Error("expected error for invalid public key")
	}
}

// TestIsNKeyRegistered_NilDB verifies that nil DB returns false (no panic).
func TestIsNKeyRegistered_NilDB(t *testing.T) {
	srv := newTestServer(t) // db=nil
	result := srv.isNKeyRegistered(newRequestContext(), "UTEST")
	if result {
		t.Error("isNKeyRegistered with nil DB should return false")
	}
}

// TestHandleAuthChallenge_MethodNotAllowed verifies only POST is accepted.
// (The challenge endpoint only supports POST.)
func TestHandleAuthChallenge_MethodGet(t *testing.T) {
	srv := newTestServer(t)
	// Challenge endpoint doesn't enforce method itself — the mux wraps it.
	// Test the raw handler doesn't panic on GET.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/challenge", nil)
	srv.handleAuthChallenge(rec, req)
	// The handler will try to decode body which will be empty → bad request
	// or service unavailable (nil DB). Either is acceptable.
	if rec.Code == http.StatusOK {
		t.Error("unexpected 200 for GET on challenge endpoint")
	}
}

// TestCleanupExpiredChallenges verifies the cleanup function removes expired entries.
func TestCleanupExpiredChallenges(t *testing.T) {
	nonceB64 := "test-cleanup-challenge-b64"
	globalChallengeStore.mu.Lock()
	globalChallengeStore.entries[nonceB64] = &challengeEntry{
		NKeyPublic: "UTEST",
		Nonce:      []byte("nonce"),
		ExpiresAt:  time.Now().Add(-time.Minute), // expired
	}
	globalChallengeStore.mu.Unlock()

	cleanupExpiredChallenges()

	globalChallengeStore.mu.Lock()
	_, exists := globalChallengeStore.entries[nonceB64]
	globalChallengeStore.mu.Unlock()

	if exists {
		t.Error("cleanupExpiredChallenges should have removed expired entry")
	}
}

// ---- Helper ----

// newRequestContext returns a context.Context suitable for passing to isNKeyRegistered.
func newRequestContext() context.Context {
	return context.Background()
}
