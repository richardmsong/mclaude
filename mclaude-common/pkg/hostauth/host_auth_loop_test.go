// host_auth_loop_test.go tests StartRefreshLoop via the internal
// startRefreshLoopWithInterval helper so short TTLs can be used without
// modifying the public API or the production constants.
package hostauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"
)

// nopLoggerInternal returns a zerolog logger that discards all output.
func nopLoggerInternal() zerolog.Logger {
	return zerolog.Nop()
}

// newTestSeedFileInternal generates a fresh NKey pair and writes only the seed to a file.
func newTestSeedFileInternal(t *testing.T) (seedPath string, kp nkeys.KeyPair) {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create NKey: %v", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		t.Fatalf("get seed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "nkey_seed")
	if err := os.WriteFile(path, seed, 0600); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	return path, kp
}

// TestStartRefreshLoop_RefreshesBeforeTTL verifies that the refresh loop calls
// Refresh at least once within a short interval (simulating TTL expiry) and
// that the cached JWT is updated in-memory.
func TestStartRefreshLoop_RefreshesBeforeTTL(t *testing.T) {
	const refreshedJWT = "refreshed-loop-jwt"
	var refreshCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/challenge":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": "loop-nonce"})
		case "/api/auth/verify":
			refreshCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "jwt": refreshedJWT})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	seedPath, _ := newTestSeedFileInternal(t)
	ha, err := NewHostAuthFromSeed(seedPath, srv.URL, nopLoggerInternal())
	if err != nil {
		t.Fatalf("NewHostAuthFromSeed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a short interval (100ms) to exercise the refresh path quickly.
	ha.startRefreshLoopWithInterval(ctx, 100*time.Millisecond)

	// Wait up to 2 seconds for at least one refresh to occur.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if refreshCount.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if refreshCount.Load() < 1 {
		t.Fatal("refresh loop did not call Refresh within 2 seconds")
	}

	// Verify the cached JWT was updated in-memory via JWTFunc.
	stored, jwtErr := ha.JWTFunc()()
	if jwtErr != nil {
		t.Fatalf("JWTFunc(): %v", jwtErr)
	}
	if stored != refreshedJWT {
		t.Errorf("stored JWT = %q, want %q", stored, refreshedJWT)
	}
}

// TestStartRefreshLoop_StopsOnContextCancel verifies that the refresh loop
// goroutine exits cleanly when the context is cancelled (no goroutine leak).
func TestStartRefreshLoop_StopsOnContextCancel(t *testing.T) {
	var refreshCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/challenge":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": "cancel-nonce"})
		case "/api/auth/verify":
			refreshCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "jwt": "cancel-test-jwt"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	seedPath, _ := newTestSeedFileInternal(t)
	ha, err := NewHostAuthFromSeed(seedPath, srv.URL, nopLoggerInternal())
	if err != nil {
		t.Fatalf("NewHostAuthFromSeed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Use a long interval (10s) so the loop only fires if the ticker fires — we
	// cancel before the first tick to verify the goroutine exits without firing.
	ha.startRefreshLoopWithInterval(ctx, 10*time.Second)

	// Cancel immediately.
	cancel()

	// Wait a short time and confirm no refresh occurred (goroutine exited).
	time.Sleep(150 * time.Millisecond)

	if refreshCount.Load() != 0 {
		t.Errorf("expected no refreshes after immediate cancel, got %d", refreshCount.Load())
	}
}

// TestStartRefreshLoop_NoCPURL verifies that StartRefreshLoop is a no-op when
// cpURL is empty (it logs a warning and returns without starting a goroutine).
// We verify this by ensuring no panic occurs and no refresh is attempted.
func TestStartRefreshLoop_NoCPURL(t *testing.T) {
	seedPath, _ := newTestSeedFileInternal(t)
	ha, err := NewHostAuthFromSeed(seedPath, "", nopLoggerInternal())
	if err != nil {
		t.Fatalf("NewHostAuthFromSeed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Should not panic and should return immediately without starting a goroutine.
	ha.StartRefreshLoop(ctx)

	// Verify no JWT was set (still empty) since no refresh happened.
	stored, jwtErr := ha.JWTFunc()()
	if jwtErr != nil {
		t.Fatalf("JWTFunc(): %v", jwtErr)
	}
	if stored != "" {
		t.Errorf("expected empty JWT when cpURL is empty, got %q", stored)
	}
}
