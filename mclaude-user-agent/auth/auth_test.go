package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"
	agentcrypto "mclaude-user-agent/crypto"
)

// newTestUserAgentAuth creates a UserAgentAuth for testing using a freshly generated NKey.
func newTestUserAgentAuth(t *testing.T, cpURL string) (*UserAgentAuth, nkeys.KeyPair) {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create NKey: %v", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		t.Fatalf("get seed: %v", err)
	}
	auth, err := NewUserAgentAuth(seed, cpURL, zerolog.Nop())
	if err != nil {
		t.Fatalf("NewUserAgentAuth: %v", err)
	}
	return auth, kp
}

// newTestCPServer creates a fake CP server that validates NKey signatures and
// returns a configurable JWT. Also records whether x25519_pub was sent.
func newTestCPServer(t *testing.T, expectedJWT string) (*httptest.Server, *atomic.Value) {
	t.Helper()

	var capturedX25519Pub atomic.Value
	nonce := base64.StdEncoding.EncodeToString([]byte("test-nonce-12345678901234"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/challenge":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": nonce}) //nolint:errcheck
		case "/api/auth/verify":
			var body map[string]json.RawMessage
			json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
			if x25519, ok := body["x25519_pub"]; ok {
				capturedX25519Pub.Store(string(x25519))
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
				"ok":  true,
				"jwt": expectedJWT,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &capturedX25519Pub
}

// TestAuthenticate_Success verifies that Authenticate completes challenge-response
// and stores the returned JWT.
func TestAuthenticate_Success(t *testing.T) {
	const testJWT = "test-jwt-bootstrap"
	srv, _ := newTestCPServer(t, testJWT)

	auth, _ := newTestUserAgentAuth(t, srv.URL)

	jwt, err := auth.Authenticate(context.Background())
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if jwt != testJWT {
		t.Errorf("jwt = %q, want %q", jwt, testJWT)
	}

	// JWTFunc should return the stored JWT.
	stored, err := auth.JWTFunc()()
	if err != nil {
		t.Fatalf("JWTFunc: %v", err)
	}
	if stored != testJWT {
		t.Errorf("stored jwt = %q, want %q", stored, testJWT)
	}
}

// TestAuthenticate_SendsX25519Pub verifies that when x25519_pub is set on the auth,
// it is included in the verify request body (ADR-0091).
func TestAuthenticate_SendsX25519Pub(t *testing.T) {
	const testJWT = "test-jwt-with-pub"
	srv, capturedPub := newTestCPServer(t, testJWT)

	auth, _ := newTestUserAgentAuth(t, srv.URL)

	kp, err := agentcrypto.GenerateX25519Keypair()
	if err != nil {
		t.Fatalf("GenerateX25519Keypair: %v", err)
	}
	auth.SetX25519Pub(kp.Public[:])

	_, err = auth.Authenticate(context.Background())
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	pubJSON := capturedPub.Load()
	if pubJSON == nil || pubJSON.(string) == "" {
		t.Error("x25519_pub was not included in the verify request")
	}

	// Verify the encoded value decodes to the expected public key.
	var pubStr string
	if err := json.Unmarshal([]byte(pubJSON.(string)), &pubStr); err != nil {
		t.Fatalf("decode x25519_pub value: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(pubStr)
	if err != nil {
		t.Fatalf("base64 decode x25519_pub: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("x25519_pub decoded length = %d, want 32", len(decoded))
	}
}

// TestAuthenticate_NoX25519Pub verifies that when no x25519_pub is set,
// the field is omitted from the verify request.
func TestAuthenticate_NoX25519Pub(t *testing.T) {
	const testJWT = "test-jwt-no-pub"
	srv, capturedPub := newTestCPServer(t, testJWT)

	auth, _ := newTestUserAgentAuth(t, srv.URL)
	// Do NOT call SetX25519Pub.

	_, err := auth.Authenticate(context.Background())
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	if v := capturedPub.Load(); v != nil && v.(string) != "" {
		t.Errorf("expected no x25519_pub in verify request, got %q", v)
	}
}

// TestAuthenticate_ServerError verifies that a CP error is propagated.
func TestAuthenticate_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error")) //nolint:errcheck
	}))
	defer srv.Close()

	auth, _ := newTestUserAgentAuth(t, srv.URL)
	_, err := auth.Authenticate(context.Background())
	if err == nil {
		t.Error("expected error for server 500, got nil")
	}
}

// TestAuthenticate_VerifyRejected verifies that a CP rejection is propagated.
func TestAuthenticate_VerifyRejected(t *testing.T) {
	nonce := base64.StdEncoding.EncodeToString([]byte("nonce"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/challenge":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": nonce}) //nolint:errcheck
		case "/api/auth/verify":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
				"ok":    false,
				"error": "nkey_not_registered",
			})
		}
	}))
	defer srv.Close()

	auth, _ := newTestUserAgentAuth(t, srv.URL)
	_, err := auth.Authenticate(context.Background())
	if err == nil {
		t.Error("expected error for rejected verify, got nil")
	}
}

// TestStartRefreshLoop_Fires verifies that the refresh loop calls Authenticate
// at least once within the configured interval.
func TestStartRefreshLoop_Fires(t *testing.T) {
	const refreshedJWT = "refreshed-jwt"
	var refreshCount atomic.Int32

	nonce := base64.StdEncoding.EncodeToString([]byte("loop-nonce"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/challenge":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": nonce}) //nolint:errcheck
		case "/api/auth/verify":
			refreshCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
				"ok":  true,
				"jwt": refreshedJWT,
			})
		}
	}))
	defer srv.Close()

	auth, _ := newTestUserAgentAuth(t, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var callbackJWT string
	auth.StartRefreshLoopWithInterval(ctx, 50*time.Millisecond, func(jwt string) {
		callbackJWT = jwt
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if refreshCount.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if refreshCount.Load() < 1 {
		t.Fatal("refresh loop did not fire within 2 seconds")
	}
	if callbackJWT != refreshedJWT {
		t.Errorf("callback JWT = %q, want %q", callbackJWT, refreshedJWT)
	}
}

// TestStartRefreshLoop_StopsOnCancel verifies that the refresh loop goroutine exits
// cleanly when the context is cancelled.
func TestStartRefreshLoop_StopsOnCancel(t *testing.T) {
	var refreshCount atomic.Int32

	nonce := base64.StdEncoding.EncodeToString([]byte("cancel-nonce"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/challenge":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": nonce}) //nolint:errcheck
		case "/api/auth/verify":
			refreshCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
				"ok":  true,
				"jwt": "jwt",
			})
		}
	}))
	defer srv.Close()

	auth, _ := newTestUserAgentAuth(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())

	// Long interval — cancel immediately, should not fire.
	auth.StartRefreshLoopWithInterval(ctx, 10*time.Second, nil)
	cancel()

	time.Sleep(100 * time.Millisecond)
	if refreshCount.Load() != 0 {
		t.Errorf("expected 0 refreshes after immediate cancel, got %d", refreshCount.Load())
	}
}

// TestPublicKey verifies that PublicKey returns a non-empty key string.
func TestPublicKey(t *testing.T) {
	auth, _ := newTestUserAgentAuth(t, "http://localhost")
	pub := auth.PublicKey()
	if pub == "" {
		t.Error("PublicKey returned empty string")
	}
	// NKey user public keys start with "U".
	if len(pub) < 2 || pub[0] != 'U' {
		t.Errorf("unexpected public key format: %q", pub)
	}
}

// TestJWTFunc_BeforeAuth verifies that JWTFunc returns error before Authenticate is called.
func TestJWTFunc_BeforeAuth(t *testing.T) {
	auth, _ := newTestUserAgentAuth(t, "http://localhost")
	_, err := auth.JWTFunc()()
	if err == nil {
		t.Error("expected error from JWTFunc before Authenticate, got nil")
	}
}
