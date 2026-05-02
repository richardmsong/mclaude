package hostauth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"

	"mclaude.io/common/pkg/hostauth"
)

// nopLogger returns a zerolog logger that discards all output.
func nopLogger() zerolog.Logger {
	return zerolog.Nop()
}

// newTestCredsFile generates a fresh NKey pair, writes a .creds file containing
// the provided jwt and returns both the file path and the key pair.
func newTestCredsFile(t *testing.T, jwt string) (credsPath string, kp nkeys.KeyPair) {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create NKey: %v", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		t.Fatalf("get seed: %v", err)
	}

	content := "-----BEGIN NATS USER JWT-----\n" +
		jwt + "\n" +
		"------END NATS USER JWT------\n" +
		"\n" +
		"-----BEGIN USER NKEY SEED-----\n" +
		string(seed) + "\n" +
		"------END USER NKEY SEED------\n"

	path := filepath.Join(t.TempDir(), "nats.creds")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write creds file: %v", err)
	}
	return path, kp
}

// newTestSeedFile generates a fresh NKey pair and writes only the seed to a file.
func newTestSeedFile(t *testing.T) (seedPath string, kp nkeys.KeyPair) {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create NKey: %v", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		t.Fatalf("get seed: %v", err)
	}

	// Write raw seed string (no decoration) — ParseDecoratedUserNKey handles both.
	path := filepath.Join(t.TempDir(), "nkey_seed")
	if err := os.WriteFile(path, seed, 0600); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	return path, kp
}

// --------------------------------------------------------------------------
// NewHostAuthFromCredsFile tests
// --------------------------------------------------------------------------

func TestNewHostAuthFromCredsFile_Valid(t *testing.T) {
	fakeJWT := "TEST_JWT_FOR_UNIT_TEST_NOT_A_REAL_TOKEN"
	credsPath, _ := newTestCredsFile(t, fakeJWT)

	ha, err := hostauth.NewHostAuthFromCredsFile(credsPath, "https://cp.example.com", nopLogger())
	if err != nil {
		t.Fatalf("NewHostAuthFromCredsFile: %v", err)
	}

	// Verify the JWT was extracted (read via JWTFunc).
	storedJWT, jwtErr := ha.JWTFunc()()
	if jwtErr != nil {
		t.Fatalf("JWTFunc(): %v", jwtErr)
	}
	if storedJWT != fakeJWT {
		t.Errorf("currentJWT = %q, want %q", storedJWT, fakeJWT)
	}
}

func TestNewHostAuthFromCredsFile_InvalidPath(t *testing.T) {
	_, err := hostauth.NewHostAuthFromCredsFile("/nonexistent/path/nats.creds", "", nopLogger())
	if err == nil {
		t.Error("expected error for nonexistent creds file")
	}
}

func TestNewHostAuthFromCredsFile_InvalidContent(t *testing.T) {
	badFile := filepath.Join(t.TempDir(), "bad.creds")
	if err := os.WriteFile(badFile, []byte("garbage-not-a-creds-file"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := hostauth.NewHostAuthFromCredsFile(badFile, "", nopLogger())
	if err == nil {
		t.Error("expected error for invalid creds content")
	}
}

// --------------------------------------------------------------------------
// NewHostAuthFromSeed tests
// --------------------------------------------------------------------------

func TestNewHostAuthFromSeed_Valid(t *testing.T) {
	seedPath, _ := newTestSeedFile(t)

	ha, err := hostauth.NewHostAuthFromSeed(seedPath, "https://cp.example.com", nopLogger())
	if err != nil {
		t.Fatalf("NewHostAuthFromSeed: %v", err)
	}

	// When loaded from seed only, the initial JWT must be empty.
	jwt, jwtErr := ha.JWTFunc()()
	if jwtErr != nil {
		t.Fatalf("JWTFunc(): %v", jwtErr)
	}
	if jwt != "" {
		t.Errorf("expected empty initial JWT for seed-only constructor, got %q", jwt)
	}
}

func TestNewHostAuthFromSeed_InvalidPath(t *testing.T) {
	_, err := hostauth.NewHostAuthFromSeed("/nonexistent/seed", "", nopLogger())
	if err == nil {
		t.Error("expected error for nonexistent seed file")
	}
}

func TestNewHostAuthFromSeed_InvalidContent(t *testing.T) {
	badFile := filepath.Join(t.TempDir(), "bad_seed")
	if err := os.WriteFile(badFile, []byte("not-a-valid-nkey-seed"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := hostauth.NewHostAuthFromSeed(badFile, "", nopLogger())
	if err == nil {
		t.Error("expected error for invalid seed content")
	}
}

// --------------------------------------------------------------------------
// PublicKey tests
// --------------------------------------------------------------------------

func TestPublicKey_Format(t *testing.T) {
	credsPath, _ := newTestCredsFile(t, "test-jwt")
	ha, err := hostauth.NewHostAuthFromCredsFile(credsPath, "", nopLogger())
	if err != nil {
		t.Fatalf("NewHostAuthFromCredsFile: %v", err)
	}

	pub := ha.PublicKey()
	if pub == "" {
		t.Error("PublicKey() returned empty string")
	}
	// NKey user public keys start with "U" (user type).
	if !strings.HasPrefix(pub, "U") {
		t.Errorf("PublicKey() = %q — expected NKey user key starting with 'U'", pub)
	}
}

func TestPublicKey_FromSeed(t *testing.T) {
	seedPath, kp := newTestSeedFile(t)
	ha, err := hostauth.NewHostAuthFromSeed(seedPath, "", nopLogger())
	if err != nil {
		t.Fatalf("NewHostAuthFromSeed: %v", err)
	}

	pub := ha.PublicKey()
	expectedPub, err := kp.PublicKey()
	if err != nil {
		t.Fatalf("kp.PublicKey(): %v", err)
	}
	if pub != expectedPub {
		t.Errorf("PublicKey() = %q, want %q", pub, expectedPub)
	}
}

// --------------------------------------------------------------------------
// Refresh tests
// --------------------------------------------------------------------------

func TestRefresh_FullFlow(t *testing.T) {
	// The CP returns a base64-encoded nonce as the challenge (ADR-0075).
	rawNonce := []byte("test-challenge-nonce-42")
	nonce := base64.StdEncoding.EncodeToString(rawNonce)
	challengeCalled := false
	verifyCalled := false
	var receivedPubKey string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/challenge":
			challengeCalled = true
			var req struct {
				NKeyPublic string `json:"nkey_public"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			receivedPubKey = req.NKeyPublic
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": nonce})

		case "/api/auth/verify":
			verifyCalled = true
			var req struct {
				NKeyPublic string `json:"nkey_public"`
				Challenge  string `json:"challenge"`
				Signature  []byte `json:"signature"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Challenge != nonce {
				http.Error(w, "wrong nonce", http.StatusBadRequest)
				return
			}
			if len(req.Signature) == 0 {
				http.Error(w, "missing signature", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":  true,
				"jwt": "TEST_JWT_PLACEHOLDER_DO_NOT_USE_IN_PRODUCTION",
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	credsPath, _ := newTestCredsFile(t, "initial-jwt")
	ha, err := hostauth.NewHostAuthFromCredsFile(credsPath, srv.URL, nopLogger())
	if err != nil {
		t.Fatalf("NewHostAuthFromCredsFile: %v", err)
	}

	jwt, err := ha.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !challengeCalled {
		t.Error("challenge endpoint was not called")
	}
	if !verifyCalled {
		t.Error("verify endpoint was not called")
	}
	if receivedPubKey == "" {
		t.Error("no public key received at challenge endpoint")
	}
	if jwt == "" {
		t.Error("empty JWT returned from Refresh")
	}
}

func TestRefresh_UpdatesStoredJWT(t *testing.T) {
	newJWT := "refreshed-jwt-value"
	// Challenge must be a valid base64-encoded nonce (ADR-0075).
	b64Challenge := base64.StdEncoding.EncodeToString([]byte("nonce-1"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/challenge":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": b64Challenge})
		case "/api/auth/verify":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "jwt": newJWT})
		}
	}))
	defer srv.Close()

	credsPath, _ := newTestCredsFile(t, "old-jwt")
	ha, err := hostauth.NewHostAuthFromCredsFile(credsPath, srv.URL, nopLogger())
	if err != nil {
		t.Fatalf("NewHostAuthFromCredsFile: %v", err)
	}

	if _, err := ha.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Verify stored JWT updated via JWTFunc.
	stored, jwtErr := ha.JWTFunc()()
	if jwtErr != nil {
		t.Fatalf("JWTFunc(): %v", jwtErr)
	}
	if stored != newJWT {
		t.Errorf("stored JWT = %q, want %q", stored, newJWT)
	}
}

func TestRefresh_ErrorWhenNoCPURL(t *testing.T) {
	credsPath, _ := newTestCredsFile(t, "test-jwt")
	ha, err := hostauth.NewHostAuthFromCredsFile(credsPath, "", nopLogger())
	if err != nil {
		t.Fatalf("NewHostAuthFromCredsFile: %v", err)
	}
	_, err = ha.Refresh(context.Background())
	if err == nil {
		t.Error("Refresh with empty cpURL should return error")
	}
}

func TestRefresh_404ReturnsErrNotRegistered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	seedPath, _ := newTestSeedFile(t)
	ha, err := hostauth.NewHostAuthFromSeed(seedPath, srv.URL, nopLogger())
	if err != nil {
		t.Fatalf("NewHostAuthFromSeed: %v", err)
	}

	_, err = ha.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected error from Refresh when server returns 404")
	}
	if !errors.Is(err, hostauth.ErrNotRegistered) {
		t.Errorf("expected ErrNotRegistered, got %v", err)
	}
}

func TestRefresh_SignatureCorrectness(t *testing.T) {
	// Verify that the signature produced during Refresh is cryptographically valid.
	// The challenge is a base64-encoded nonce; the signature must cover the raw
	// decoded bytes (ADR-0075).
	rawNonce := []byte("verify-sig-nonce-raw-bytes")
	b64Challenge := base64.StdEncoding.EncodeToString(rawNonce)

	var capturedPubKey string
	var capturedSig []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/challenge":
			var req struct {
				NKeyPublic string `json:"nkey_public"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			capturedPubKey = req.NKeyPublic
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": b64Challenge})
		case "/api/auth/verify":
			var req struct {
				NKeyPublic string `json:"nkey_public"`
				Challenge  string `json:"challenge"`
				Signature  []byte `json:"signature"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			capturedSig = req.Signature
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "jwt": "sig-test-jwt"})
		}
	}))
	defer srv.Close()

	credsPath, _ := newTestCredsFile(t, "initial-jwt")
	ha, err := hostauth.NewHostAuthFromCredsFile(credsPath, srv.URL, nopLogger())
	if err != nil {
		t.Fatalf("NewHostAuthFromCredsFile: %v", err)
	}

	if _, err := ha.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Verify the captured signature over the raw decoded nonce bytes (not the
	// base64 string). This matches how the control-plane verifies signatures.
	verifier, err := nkeys.FromPublicKey(capturedPubKey)
	if err != nil {
		t.Fatalf("FromPublicKey(%q): %v", capturedPubKey, err)
	}
	if err := verifier.Verify(rawNonce, capturedSig); err != nil {
		t.Errorf("signature verification failed: %v", err)
	}
}

// --------------------------------------------------------------------------
// SignFunc tests
// --------------------------------------------------------------------------

func TestSignFunc_ProducesSignature(t *testing.T) {
	credsPath, _ := newTestCredsFile(t, "test-jwt")
	ha, err := hostauth.NewHostAuthFromCredsFile(credsPath, "", nopLogger())
	if err != nil {
		t.Fatalf("NewHostAuthFromCredsFile: %v", err)
	}

	nonce := []byte("test-nonce-bytes")
	sig, err := ha.SignFunc()(nonce)
	if err != nil {
		t.Fatalf("SignFunc(): %v", err)
	}
	if len(sig) == 0 {
		t.Error("SignFunc() returned empty signature")
	}
}

func TestSignFunc_SignatureVerifiable(t *testing.T) {
	credsPath, _ := newTestCredsFile(t, "test-jwt")
	ha, err := hostauth.NewHostAuthFromCredsFile(credsPath, "", nopLogger())
	if err != nil {
		t.Fatalf("NewHostAuthFromCredsFile: %v", err)
	}

	nonce := []byte("nonce-to-verify")
	sig, err := ha.SignFunc()(nonce)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	pub := ha.PublicKey()
	verifier, err := nkeys.FromPublicKey(pub)
	if err != nil {
		t.Fatalf("FromPublicKey: %v", err)
	}
	if err := verifier.Verify(nonce, sig); err != nil {
		t.Errorf("signature verification failed: %v", err)
	}
}

// --------------------------------------------------------------------------
// TestRefresh_SignsRawNonceNotBase64 — ADR-0075 requirement
// --------------------------------------------------------------------------

// TestRefresh_SignsRawNonceNotBase64 verifies that Refresh() base64-decodes the
// challenge string to raw bytes before signing, matching what the control-plane
// verifies. The mock CP verifies via nkeys.FromPublicKey(pub).Verify(rawNonce, sig).
func TestRefresh_SignsRawNonceNotBase64(t *testing.T) {
	// rawNonce is what the CP would generate and verify against.
	rawNonce := []byte("abcdefghijklmnopqrstuvwxyz123456") // 32 raw bytes
	b64Challenge := base64.StdEncoding.EncodeToString(rawNonce)

	var capturedPubKey string
	var capturedSig []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/challenge":
			var req struct {
				NKeyPublic string `json:"nkey_public"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			capturedPubKey = req.NKeyPublic
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": b64Challenge})

		case "/api/auth/verify":
			var req struct {
				NKeyPublic string `json:"nkey_public"`
				Challenge  string `json:"challenge"`
				Signature  []byte `json:"signature"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			capturedSig = req.Signature
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "jwt": "raw-nonce-test-jwt"})

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	credsPath, _ := newTestCredsFile(t, "initial-jwt")
	ha, err := hostauth.NewHostAuthFromCredsFile(credsPath, srv.URL, nopLogger())
	if err != nil {
		t.Fatalf("NewHostAuthFromCredsFile: %v", err)
	}

	if _, err := ha.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Verify signature over raw decoded nonce bytes — exactly what the CP does.
	verifier, err := nkeys.FromPublicKey(capturedPubKey)
	if err != nil {
		t.Fatalf("nkeys.FromPublicKey(%q): %v", capturedPubKey, err)
	}
	if err := verifier.Verify(rawNonce, capturedSig); err != nil {
		t.Errorf("signature over raw nonce bytes failed: %v — Refresh() must sign the decoded bytes, not the base64 string (ADR-0075)", err)
	}

	// Also assert that signing over the base64 string would NOT verify — confirming
	// the old bug is gone and the test is actually discriminating.
	if err := verifier.Verify([]byte(b64Challenge), capturedSig); err == nil {
		t.Error("signature unexpectedly verified over base64 string — test is not discriminating; fix the assertion")
	}
}

// --------------------------------------------------------------------------
// ErrNotRegistered sentinel test
// --------------------------------------------------------------------------

func TestErrNotRegistered_IsErrors(t *testing.T) {
	// Ensure ErrNotRegistered is exported and can be used with errors.Is.
	wrapped := errors.New("wrapped: " + hostauth.ErrNotRegistered.Error())
	if errors.Is(wrapped, hostauth.ErrNotRegistered) {
		t.Error("wrapped bare error should not match ErrNotRegistered via Is")
	}
	// Direct comparison works.
	if !errors.Is(hostauth.ErrNotRegistered, hostauth.ErrNotRegistered) {
		t.Error("ErrNotRegistered should equal itself")
	}
}
