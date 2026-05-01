package cmd_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mclaude-cli/cmd"
)

// ── Device-code flow happy path ───────────────────────────────────────────────

// TestLoginDeviceCodeFlow exercises the full happy-path device-code flow against
// a local HTTP test server that simulates the control-plane endpoints.
func TestLoginDeviceCodeFlow(t *testing.T) {
	deviceCode := "TESTDEVCODE"
	userCode := "AB12CD"
	userSlug := "alice-test"
	testToken := "test-nats-token-placeholder"

	// Count how many times poll was called.
	pollCount := 0

	mux := http.NewServeMux()

	// POST /api/auth/device-code
	mux.HandleFunc("/api/auth/device-code", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Verify request contains publicKey.
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req["publicKey"] == "" {
			http.Error(w, "missing publicKey", http.StatusBadRequest)
			return
		}
		resp := map[string]interface{}{
			"deviceCode":      deviceCode,
			"userCode":        userCode,
			"verificationUrl": "https://mclaude.internal/auth/device/" + userCode,
			"expiresIn":       900,
			"interval":        1,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// POST /api/auth/device-code/poll — returns pending twice, then success.
	mux.HandleFunc("/api/auth/device-code/poll", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		pollCount++
		var req map[string]string
		json.NewDecoder(r.Body).Decode(&req)
		if req["deviceCode"] != deviceCode {
			http.Error(w, "invalid device code", http.StatusBadRequest)
			return
		}
		if pollCount < 2 {
			// Simulate pending (202).
			w.WriteHeader(http.StatusAccepted)
			return
		}
		// Return success.
		resp := map[string]interface{}{
			"jwt":      testToken,
			"userSlug": userSlug,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	ctxPath := filepath.Join(dir, "context.json")

	var out bytes.Buffer
	result, err := cmd.RunLogin(cmd.LoginFlags{
		ServerURL:   srv.URL,
		AuthPath:    authPath,
		ContextPath: ctxPath,
	}, &out)
	if err != nil {
		t.Fatalf("RunLogin: %v", err)
	}

	if result.UserSlug != userSlug {
		t.Errorf("UserSlug = %q; want %q", result.UserSlug, userSlug)
	}
	if result.AuthPath != authPath {
		t.Errorf("AuthPath = %q; want %q", result.AuthPath, authPath)
	}

	// Credentials file must exist with correct content.
	creds, err := cmd.LoadAuth(authPath)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	if creds.JWT != testToken {
		t.Errorf("JWT = %q; want test token placeholder value", creds.JWT)
	}
	if creds.NKeySeed == "" {
		t.Error("NKeySeed is empty; expect non-empty local seed")
	}
	if creds.UserSlug != userSlug {
		t.Errorf("UserSlug = %q; want %q", creds.UserSlug, userSlug)
	}

	// Poll must have been called at least twice (pending + success).
	if pollCount < 2 {
		t.Errorf("poll count = %d; want ≥ 2 (at least one pending + success)", pollCount)
	}

	// Output must show the verification URL.
	if !strings.Contains(out.String(), userCode) {
		t.Errorf("output = %q; want user code %q", out.String(), userCode)
	}

	// Output must show success message.
	if !strings.Contains(out.String(), userSlug) {
		t.Errorf("output = %q; want user slug %q", out.String(), userSlug)
	}
}

// TestLoginNKeyNotSentToServer verifies that the NKey seed is generated locally
// and NOT sent to the server (only the public key is sent).
func TestLoginNKeyNotSentToServer(t *testing.T) {
	var capturedBody []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/device-code", func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		buf.ReadFrom(r.Body)
		capturedBody = buf.Bytes()

		// Respond with a very short TTL device code.
		json.NewEncoder(w).Encode(map[string]interface{}{
			"deviceCode":      "TESTCODE",
			"userCode":        "XY99ZZ",
			"verificationUrl": "https://example.com/verify",
			"expiresIn":       1, // 1-second TTL so test doesn't hang
			"interval":        1,
		})
	})
	mux.HandleFunc("/api/auth/device-code/poll", func(w http.ResponseWriter, r *http.Request) {
		// Return an error to short-circuit the flow.
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "code_expired",
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	// RunLogin will fail (code expired), but we only care about what was sent
	// to the device-code endpoint.
	cmd.RunLogin(cmd.LoginFlags{ //nolint:errcheck
		ServerURL:   srv.URL,
		AuthPath:    filepath.Join(dir, "auth.json"),
		ContextPath: filepath.Join(dir, "context.json"),
	}, io.Discard)

	// The captured request body must contain publicKey but NOT seed.
	if len(capturedBody) == 0 {
		t.Fatal("device-code endpoint received no request body")
	}
	var reqBody map[string]string
	if err := json.Unmarshal(capturedBody, &reqBody); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if reqBody["publicKey"] == "" {
		t.Error("device-code request missing publicKey")
	}
	if reqBody["seed"] != "" || reqBody["nkeySeed"] != "" {
		t.Error("device-code request contains seed — NKey seed must NEVER leave the machine")
	}
}

// TestLoginAuthFileModeIs0600 verifies the credential file is written 0600.
func TestLoginAuthFileModeIs0600(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/device-code", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"deviceCode":      "CODE",
			"userCode":        "AB1234",
			"verificationUrl": "https://example.com/v",
			"expiresIn":       900,
			"interval":        1,
		})
	})
	mux.HandleFunc("/api/auth/device-code/poll", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jwt":      "fake-jwt-token",
			"userSlug": "alice-test",
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")

	if _, err := cmd.RunLogin(cmd.LoginFlags{
		ServerURL:   srv.URL,
		AuthPath:    authPath,
		ContextPath: filepath.Join(dir, "context.json"),
	}, new(bytes.Buffer)); err != nil {
		t.Fatalf("RunLogin: %v", err)
	}

	info, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("stat auth file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("auth file mode = %o; want 0600", info.Mode().Perm())
	}
}

// TestLoginServerError handles the case where the server returns an error.
func TestLoginServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/device-code", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	_, err := cmd.RunLogin(cmd.LoginFlags{
		ServerURL:   srv.URL,
		AuthPath:    filepath.Join(dir, "auth.json"),
		ContextPath: filepath.Join(dir, "context.json"),
	}, new(bytes.Buffer))
	if err == nil {
		t.Fatal("RunLogin: expected error for server 500; got nil")
	}
	if !strings.Contains(err.Error(), "device code") {
		t.Errorf("error %q; want 'device code' mention", err.Error())
	}
}

// TestLoginContextUpdated verifies that after login, context.json is updated
// with the user slug.
func TestLoginContextUpdated(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/device-code", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"deviceCode":      "CODE2",
			"userCode":        "CD5678",
			"verificationUrl": "https://example.com/v",
			"expiresIn":       900,
			"interval":        1,
		})
	})
	mux.HandleFunc("/api/auth/device-code/poll", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jwt":      "ctx-jwt-token",
			"userSlug": "bob-test",
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	ctxPath := filepath.Join(dir, "context.json")

	if _, err := cmd.RunLogin(cmd.LoginFlags{
		ServerURL:   srv.URL,
		AuthPath:    filepath.Join(dir, "auth.json"),
		ContextPath: ctxPath,
	}, new(bytes.Buffer)); err != nil {
		t.Fatalf("RunLogin: %v", err)
	}

	// Context file must be updated with user slug.
	data, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("read context: %v", err)
	}
	var ctx map[string]string
	if err := json.Unmarshal(data, &ctx); err != nil {
		t.Fatalf("unmarshal context: %v", err)
	}
	if ctx["userSlug"] != "bob-test" {
		t.Errorf("context userSlug = %q; want bob-test", ctx["userSlug"])
	}
}

// TestLoginDisplaysVerificationURL checks that the output contains the URL and user code.
func TestLoginDisplaysVerificationURL(t *testing.T) {
	expectedURL := "https://mclaude.example.com/auth/device/HELLO1"

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/device-code", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"deviceCode":      "DCODE",
			"userCode":        "HELLO1",
			"verificationUrl": expectedURL,
			"expiresIn":       900,
			"interval":        1,
		})
	})
	mux.HandleFunc("/api/auth/device-code/poll", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jwt":      "display-jwt",
			"userSlug": "carol-test",
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	var out bytes.Buffer
	if _, err := cmd.RunLogin(cmd.LoginFlags{
		ServerURL:   srv.URL,
		AuthPath:    filepath.Join(dir, "auth.json"),
		ContextPath: filepath.Join(dir, "context.json"),
	}, &out); err != nil {
		t.Fatalf("RunLogin: %v", err)
	}

	if !strings.Contains(out.String(), expectedURL) {
		t.Errorf("output = %q; want verification URL %q", out.String(), expectedURL)
	}
	if !strings.Contains(out.String(), "HELLO1") {
		t.Errorf("output = %q; want user code 'HELLO1'", out.String())
	}
}
