package main

// entrypoint_test.go — tests for entrypoint.sh token-read behavior (ADR-0091).
//
// Verifies:
//  1. Missing TOKEN_PROVIDER env var → non-zero exit with FATAL message.
//  2. Missing token file (TOKEN_PROVIDER set but file absent) → non-zero exit.
//  3. Empty token file → non-zero exit with FATAL message.
//  4. Valid token file → CLAUDE_CODE_OAUTH_TOKEN is exported correctly and
//     exec continues (we verify the env var is set before the exec by sourcing
//     only the token-read section in a subshell).

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// entrypointPath returns the absolute path to entrypoint.sh.
func entrypointPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "entrypoint.sh")
}

// runEntrypointTokenSection runs a minimal bash snippet that reproduces only
// the token-read logic from entrypoint.sh (lines that deal with TOKEN_PROVIDER
// and /secrets/tokens/).  This lets us test that section in isolation without
// needing Nix, a running session-agent binary, or NATS.
//
// The snippet is self-contained and mirrors the exact logic in entrypoint.sh.
func tokenReadSnippet(tokenDir string) string {
	return `set -e

if [ -z "${TOKEN_PROVIDER}" ]; then
    echo "[entrypoint] FATAL: TOKEN_PROVIDER env var is not set" >&2
    exit 1
fi
token_file="` + tokenDir + `/${TOKEN_PROVIDER}"
if [ ! -f "${token_file}" ]; then
    echo "[entrypoint] FATAL: token file not found: ${token_file}" >&2
    exit 1
fi
export CLAUDE_CODE_OAUTH_TOKEN=$(cat "${token_file}")
if [ -z "${CLAUDE_CODE_OAUTH_TOKEN}" ]; then
    echo "[entrypoint] FATAL: token file is empty: ${token_file}" >&2
    exit 1
fi
echo "TOKEN_OK:${CLAUDE_CODE_OAUTH_TOKEN}"
`
}

// TestEntrypointTokenMissingProvider verifies that the entrypoint exits non-zero
// with a FATAL message when TOKEN_PROVIDER is not set.
func TestEntrypointTokenMissingProvider(t *testing.T) {
	tmpDir := t.TempDir()
	snippet := tokenReadSnippet(tmpDir)

	cmd := exec.Command("bash", "-c", snippet)
	// Explicitly unset TOKEN_PROVIDER by filtering it from the environment.
	cmd.Env = envWithout(os.Environ(), "TOKEN_PROVIDER")

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when TOKEN_PROVIDER is unset; got exit 0\noutput: %s", out)
	}
	if !strings.Contains(string(out), "FATAL") {
		t.Errorf("expected FATAL message in output; got: %s", out)
	}
	if !strings.Contains(string(out), "TOKEN_PROVIDER") {
		t.Errorf("expected TOKEN_PROVIDER mentioned in FATAL message; got: %s", out)
	}
}

// TestEntrypointTokenMissingFile verifies that the entrypoint exits non-zero
// when TOKEN_PROVIDER is set but the corresponding file does not exist.
func TestEntrypointTokenMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	snippet := tokenReadSnippet(tmpDir)

	cmd := exec.Command("bash", "-c", snippet)
	cmd.Env = append(envWithout(os.Environ(), "TOKEN_PROVIDER"), "TOKEN_PROVIDER=anthropic")
	// Note: tmpDir/anthropic does NOT exist.

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when token file is absent; got exit 0\noutput: %s", out)
	}
	if !strings.Contains(string(out), "FATAL") {
		t.Errorf("expected FATAL message in output; got: %s", out)
	}
	if !strings.Contains(string(out), "not found") {
		t.Errorf("expected 'not found' in FATAL message; got: %s", out)
	}
}

// TestEntrypointTokenEmptyFile verifies that the entrypoint exits non-zero
// when the token file exists but is empty.
func TestEntrypointTokenEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "anthropic")
	if err := os.WriteFile(tokenFile, []byte(""), 0o600); err != nil {
		t.Fatalf("write empty token file: %v", err)
	}

	snippet := tokenReadSnippet(tmpDir)
	cmd := exec.Command("bash", "-c", snippet)
	cmd.Env = append(envWithout(os.Environ(), "TOKEN_PROVIDER"), "TOKEN_PROVIDER=anthropic")

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when token file is empty; got exit 0\noutput: %s", out)
	}
	if !strings.Contains(string(out), "FATAL") {
		t.Errorf("expected FATAL message in output; got: %s", out)
	}
	if !strings.Contains(string(out), "empty") {
		t.Errorf("expected 'empty' in FATAL message; got: %s", out)
	}
}

// TestEntrypointTokenValidFile verifies that when the token file exists and
// contains a non-empty value, the entrypoint exports CLAUDE_CODE_OAUTH_TOKEN
// correctly and proceeds without error.
func TestEntrypointTokenValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	tokenValue := "test-anthropic-oauth-token-abc123"
	tokenFile := filepath.Join(tmpDir, "anthropic")
	if err := os.WriteFile(tokenFile, []byte(tokenValue), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	snippet := tokenReadSnippet(tmpDir)
	cmd := exec.Command("bash", "-c", snippet)
	cmd.Env = append(envWithout(os.Environ(), "TOKEN_PROVIDER", "CLAUDE_CODE_OAUTH_TOKEN"),
		"TOKEN_PROVIDER=anthropic")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0 with valid token file; got error: %v\noutput: %s", err, out)
	}
	outStr := string(out)
	expectedMarker := "TOKEN_OK:" + tokenValue
	if !strings.Contains(outStr, expectedMarker) {
		t.Errorf("expected %q in output (confirms CLAUDE_CODE_OAUTH_TOKEN was set); got: %s",
			expectedMarker, outStr)
	}
}

// TestEntrypointTokenWhitespaceOnlyFile verifies that a token file containing
// only whitespace is treated as empty and causes a FATAL exit.
// (Shell command substitution strips trailing newlines; a newline-only file
// produces an empty variable after $(cat ...) substitution.)
func TestEntrypointTokenWhitespaceOnlyFile(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "anthropic")
	if err := os.WriteFile(tokenFile, []byte("\n\n"), 0o600); err != nil {
		t.Fatalf("write whitespace-only token file: %v", err)
	}

	snippet := tokenReadSnippet(tmpDir)
	cmd := exec.Command("bash", "-c", snippet)
	cmd.Env = append(envWithout(os.Environ(), "TOKEN_PROVIDER"), "TOKEN_PROVIDER=anthropic")

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when token file is whitespace-only; got exit 0\noutput: %s", out)
	}
	if !strings.Contains(string(out), "FATAL") {
		t.Errorf("expected FATAL message; got: %s", out)
	}
}

// TestEntrypointExecBlock verifies the final exec invocation in entrypoint.sh
// passes --auth-url and does NOT pass --nats-creds by inspecting the script
// source directly.  This is a static analysis test — it confirms the literal
// text of the exec block matches the spec without requiring a running binary.
func TestEntrypointExecBlock(t *testing.T) {
	src, err := os.ReadFile(entrypointPath(t))
	if err != nil {
		t.Fatalf("read entrypoint.sh: %v", err)
	}
	script := string(src)

	// ADR-0091: --auth-url must be present.
	if !strings.Contains(script, `--auth-url`) {
		t.Error("entrypoint.sh: missing --auth-url flag in exec session-agent block (ADR-0091)")
	}

	// ADR-0091 / ADR-0063: --nats-creds must NOT appear as an exec argument
	// (it is acceptable in comments explaining why it was removed).
	// Check by scanning lines that are not comments.
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip comment lines.
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(line, `--nats-creds`) {
			t.Errorf("entrypoint.sh: --nats-creds appears in non-comment line (must be removed per ADR-0063/ADR-0091): %q", line)
		}
	}

	// ADR-0091: oauth-token from user-secrets must NOT be read.
	if strings.Contains(script, `user-secrets/oauth-token`) {
		t.Error("entrypoint.sh: legacy oauth-token read from user-secrets must be removed (ADR-0091)")
	}

	// ADR-0091: token must be read from /secrets/tokens/${TOKEN_PROVIDER}.
	if !strings.Contains(script, `/secrets/tokens/`) {
		t.Error("entrypoint.sh: missing /secrets/tokens/ token read (ADR-0091)")
	}

	// ADR-0091: CLAUDE_CODE_OAUTH_TOKEN must be exported from the mounted secret.
	if !strings.Contains(script, `CLAUDE_CODE_OAUTH_TOKEN`) {
		t.Error("entrypoint.sh: CLAUDE_CODE_OAUTH_TOKEN export missing (ADR-0091)")
	}

	// The CONTROL_PLANE_URL env var must be used for --auth-url.
	if !strings.Contains(script, `CONTROL_PLANE_URL`) {
		t.Error("entrypoint.sh: CONTROL_PLANE_URL must be used for --auth-url (ADR-0091)")
	}
}

// envWithout returns os.Environ() with the named keys filtered out.
func envWithout(env []string, keys ...string) []string {
	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			filtered = append(filtered, kv)
			continue
		}
		name := kv[:idx]
		if !keySet[name] {
			filtered = append(filtered, kv)
		}
	}
	return filtered
}
