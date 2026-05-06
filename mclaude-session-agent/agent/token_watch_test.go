package agent

// token_watch_test.go — unit tests for ADR-0092 token_expired status reporting
// and Secret mount watcher recovery.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	testutil "mclaude-session-agent/testutil"
)

// ---------------------------------------------------------------------------
// isAnthropicAuthError unit tests
// ---------------------------------------------------------------------------

// TestIsAnthropicAuthError_Patterns verifies that the patterns expected from
// Claude Code's error output are recognised as auth errors.
func TestIsAnthropicAuthError_Patterns(t *testing.T) {
	authErrors := []string{
		"Authentication failed: 401 Unauthorized — invalid x-api-key",
		"401",
		"403",
		"authentication_error: invalid credentials",
		"AUTHENTICATION ERROR: token expired",
		"invalid x-api-key",
		"invalid_api_key: bad key",
		"permission_error from Anthropic",
		"access_denied: 403 Forbidden",
		"Unauthorized access",
		"Forbidden resource",
	}
	for _, msg := range authErrors {
		if !isAnthropicAuthError(msg) {
			t.Errorf("isAnthropicAuthError(%q) = false, want true", msg)
		}
	}
}

// TestIsAnthropicAuthError_NonAuth verifies that normal error messages are NOT
// misclassified as auth errors.
func TestIsAnthropicAuthError_NonAuth(t *testing.T) {
	nonAuthErrors := []string{
		"context window exceeded",
		"rate limit exceeded",
		"model overloaded",
		"timeout after 30s",
		"network error: connection refused",
		"",
	}
	for _, msg := range nonAuthErrors {
		if isAnthropicAuthError(msg) {
			t.Errorf("isAnthropicAuthError(%q) = true, want false", msg)
		}
	}
}

// ---------------------------------------------------------------------------
// token_expired status reporting (via session.handleSideEffect)
// ---------------------------------------------------------------------------

// TestTokenExpiredStatusOnAuthError verifies that when mock-claude emits a
// result event with is_error:true and an auth error message, the session KV
// status is set to token_expired (ADR-0092).
func TestTokenExpiredStatusOnAuthError(t *testing.T) {
	mockClaude := testutil.MockClaudePath(t)
	transcript := testutil.TranscriptPath("auth_error.jsonl")

	st := SessionState{
		ID:          "sess-auth-err",
		Slug:        "sess-auth-err",
		UserSlug:    "test-user",
		HostSlug:    "test-host",
		ProjectSlug: "test-proj",
		ProjectID:   "test-proj",
		State:       StateIdle,
		CreatedAt:   time.Now(),
	}
	sess := newSession(st, "test-user")
	sess.extraEnv = []string{"MOCK_TRANSCRIPT=" + transcript}

	pc := &publishCapture{}
	kc := &kvCapture{}

	if err := sess.start(mockClaude, false, pc.publish, kc.write); err != nil {
		t.Fatalf("session.start: %v", err)
	}
	t.Cleanup(func() {
		sess.stop()
		sess.waitDone()
	})

	// Wait for init event to confirm startup.
	if !pc.waitForN(2, 5*time.Second) {
		t.Fatalf("startup events not received (got %d)", len(pc.messages()))
	}

	// Send a user message to trigger the auth-error turn.
	userMsg := []byte(`{"type":"user","message":{"role":"user","content":"hello"}}`)
	sess.sendInput(userMsg)

	// Wait for KV to reflect token_expired status.
	if !kc.waitFor(func(states []SessionState) bool {
		for _, s := range states {
			if s.State == StatusTokenExpired {
				return true
			}
		}
		return false
	}, 10*time.Second) {
		// Collect all states seen for diagnostic output.
		var seen []string
		for _, s := range kc.all() {
			seen = append(seen, s.State)
		}
		t.Errorf("KV never reached token_expired status. States seen: %v", seen)
	}
}

// ---------------------------------------------------------------------------
// readTokenEnv tests
// ---------------------------------------------------------------------------

// TestReadTokenEnv_ReadsFile verifies that readTokenEnv reads a token from the
// expected file path and returns the correct env string.
func TestReadTokenEnv_ReadsFile(t *testing.T) {
	// Override TOKEN_PROVIDER and build a temp secrets dir.
	tmpDir := t.TempDir()
	t.Setenv("TOKEN_PROVIDER", "testprovider")

	// We need to override tokenSecretsDir for testing. Since it's a package-level
	// const, we patch it via readTokenEnvFromDir (a helper that takes the dir).
	tokenFile := filepath.Join(tmpDir, "testprovider")
	if err := os.WriteFile(tokenFile, []byte("sk-ant-oat01-testtoken\n"), 0600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	got := readTokenEnvFromDir(tmpDir)
	want := "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-testtoken"
	if got != want {
		t.Errorf("readTokenEnvFromDir = %q, want %q", got, want)
	}
}

// TestReadTokenEnv_MissingFile verifies that readTokenEnvFromDir returns ""
// when the token file does not exist (BYOH / dev mode without a Secret mount).
func TestReadTokenEnv_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TOKEN_PROVIDER", "anthropic")

	got := readTokenEnvFromDir(tmpDir)
	if got != "" {
		t.Errorf("readTokenEnvFromDir (missing file) = %q, want empty string", got)
	}
}

// TestReadTokenEnv_EmptyFile verifies that readTokenEnvFromDir returns ""
// for an empty file (e.g. Secret not yet populated).
func TestReadTokenEnv_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TOKEN_PROVIDER", "anthropic")
	if err := os.WriteFile(filepath.Join(tmpDir, "anthropic"), []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	got := readTokenEnvFromDir(tmpDir)
	if got != "" {
		t.Errorf("readTokenEnvFromDir (empty file) = %q, want empty string", got)
	}
}

// ---------------------------------------------------------------------------
// latestMtime tests
// ---------------------------------------------------------------------------

// TestLatestMtime_MissingDir verifies that latestMtime returns zero for a
// non-existent directory (no panic, graceful fallback).
func TestLatestMtime_MissingDir(t *testing.T) {
	got := latestMtime("/nonexistent/path/xyz")
	if !got.IsZero() {
		t.Errorf("latestMtime(missing dir) = %v, want zero time", got)
	}
}

// TestLatestMtime_UpdatedFile verifies that latestMtime detects a file update.
func TestLatestMtime_UpdatedFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "token")
	if err := os.WriteFile(f, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	before := latestMtime(dir)

	// Ensure clock advances (on fast filesystems mtime resolution may be 1s).
	time.Sleep(10 * time.Millisecond)
	// Force mtime update by rewriting the file.
	// Use Chtimes with a future time to avoid filesystem mtime-resolution issues.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(f, future, future); err != nil {
		t.Fatal(err)
	}

	after := latestMtime(dir)
	if !after.After(before) {
		t.Errorf("latestMtime did not advance after file update: before=%v after=%v", before, after)
	}
}
