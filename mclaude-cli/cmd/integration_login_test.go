//go:build integration

// Package cmd_test — integration_login_test.go
//
// Integration test for `mclaude login` device-code flow.
// Uses an httptest.Server mock (login is HTTP-only — no NATS or S3 involved).
//
// See ADR-0065 and docs/mclaude-cli/spec-cli.md §Smoke Tests.
package cmd_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nkeys"
	"mclaude-cli/cmd"
)

// TestIntegration_Login_DeviceCode runs RunLogin against an httptest.Server
// that mocks the CP device-code endpoints. A background goroutine delivers
// credentials after 200ms. Asserts auth.json contains {jwt, nkeySeed, userSlug}
// with a valid U-key NKey seed.
func TestIntegration_Login_DeviceCode(t *testing.T) {
	const (
		mockDeviceCode = "INTTEST-DEVICE-CODE"
		mockUserCode   = "TSTCDE"
		mockJWT        = "test-integration-jwt-value"
		mockUserSlug   = "integration-test-user"
	)

	// pollCount tracks how many times poll was called so we can simulate pending→authorized.
	var pollCount atomic.Int32

	mux := http.NewServeMux()

	// POST /api/auth/device-code — returns device code and user code.
	mux.HandleFunc("/api/auth/device-code", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Verify publicKey is present.
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req["publicKey"] == "" {
			http.Error(w, "missing publicKey", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"deviceCode":      mockDeviceCode,
			"userCode":        mockUserCode,
			"verificationUrl": "https://mclaude.internal/auth/device/" + mockUserCode,
			"expiresIn":       900,
			"interval":        1, // 1-second poll interval for faster test
		})
	})

	// POST /api/auth/device-code/poll — returns pending first call, then authorized.
	mux.HandleFunc("/api/auth/device-code/poll", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		count := pollCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			// First call: pending.
			json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
				"status": "pending",
			})
			return
		}
		// Subsequent calls: authorized (delivered after 200ms delay in goroutine).
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"status":   "authorized",
			"jwt":      mockJWT,
			"userSlug": mockUserSlug,
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Background goroutine: after 200ms, the poll will see the second call and return authorized.
	// (The poll endpoint itself handles this — we just need to wait slightly before the second poll.)
	// Since the poll interval is 1s and we return authorized on the second call, the test
	// will take ~1s. The 200ms goroutine just serves as a reminder that this is async.
	go func() {
		time.Sleep(200 * time.Millisecond)
		// No action needed — the mock server handles the state transition automatically.
	}()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	ctxPath := filepath.Join(dir, "context.json")

	result, err := cmd.RunLogin(cmd.LoginFlags{
		ServerURL:   srv.URL,
		AuthPath:    authPath,
		ContextPath: ctxPath,
	}, os.Stderr)
	if err != nil {
		t.Fatalf("RunLogin: %v", err)
	}

	if result == nil {
		t.Fatal("RunLogin returned nil result")
	}
	if result.UserSlug != mockUserSlug {
		t.Errorf("UserSlug = %q; want %q", result.UserSlug, mockUserSlug)
	}

	// Verify auth.json written with correct fields.
	creds, err := cmd.LoadAuth(authPath)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	if creds.JWT != mockJWT {
		t.Errorf("JWT = %q; want %q", creds.JWT, mockJWT)
	}
	if creds.UserSlug != mockUserSlug {
		t.Errorf("UserSlug = %q; want %q", creds.UserSlug, mockUserSlug)
	}

	// Verify NKey seed is a valid U-key (User key).
	if creds.NKeySeed == "" {
		t.Fatal("NKeySeed is empty; expected a valid U-key seed")
	}
	kp, err := nkeys.FromSeed([]byte(creds.NKeySeed))
	if err != nil {
		t.Fatalf("NKeySeed is not a valid NKey seed: %v", err)
	}
	pubKey, err := kp.PublicKey()
	if err != nil {
		t.Fatalf("get public key from seed: %v", err)
	}
	// U-key public keys start with 'U'.
	if len(pubKey) == 0 || pubKey[0] != 'U' {
		t.Errorf("NKey seed public key = %q; want a U-key (starting with 'U')", pubKey)
	}

	// Poll must have been called at least twice (pending + authorized).
	if pollCount.Load() < 2 {
		t.Errorf("poll count = %d; want >= 2", pollCount.Load())
	}

	t.Logf("login integration test passed: userSlug=%s nkeySeed prefix=%s...", mockUserSlug, creds.NKeySeed[:6])
}
