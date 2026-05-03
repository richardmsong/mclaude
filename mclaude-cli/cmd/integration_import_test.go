//go:build integration

// Package cmd_test — integration_import_test.go
//
// Integration tests for `mclaude import` against a real deployed stack.
// TestMain (integration_main_test.go) creates the ephemeral test user and
// acquires NATS credentials before these tests run.
//
// See ADR-0065 and docs/mclaude-cli/spec-cli.md §Smoke Tests.
package cmd_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"mclaude-cli/cmd"
	clicontext "mclaude-cli/context"
)

// seedImportDir creates a temporary directory structure with 2 JSONL session
// files and a memory dir, mimicking ~/.claude/projects/{encoded-cwd}/.
// Returns (claudeProjectsDir, fakeCWD, authPath).
func seedImportDir(t *testing.T) (claudeProjectsDir, fakeCWD, authPath string) {
	t.Helper()
	dir := t.TempDir()

	// Write test credentials from TestMain globals.
	authPath = filepath.Join(dir, "auth.json")
	if err := cmd.SaveAuth(authPath, &cmd.AuthCredentials{
		JWT:      intJWT,
		NKeySeed: intNKeySeed,
		UserSlug: intUserSlug,
		NATSUrl:  intNATSURL,
	}); err != nil {
		t.Fatalf("seedImportDir: SaveAuth: %v", err)
	}

	// Create a fake CWD whose last component will become the project slug.
	// Use "my-project" so slug collision test is deterministic.
	fakeCWD = "/integration/test/my-project"
	encodedCWD := cmd.EncodeCWD(fakeCWD)

	claudeProjectsDir = filepath.Join(dir, "claude-projects")
	projectDir := filepath.Join(claudeProjectsDir, encodedCWD)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("seedImportDir: MkdirAll: %v", err)
	}

	// Write 2 JSONL session files.
	for i := 1; i <= 2; i++ {
		sessFile := filepath.Join(projectDir, fmt.Sprintf("session-%03d.jsonl", i))
		content := `{"type":"system","init":{"model":"claude-opus-4-5"}}` + "\n"
		if err := os.WriteFile(sessFile, []byte(content), 0644); err != nil {
			t.Fatalf("seedImportDir: write session file: %v", err)
		}
	}

	// Write a memory directory.
	memDir := filepath.Join(projectDir, "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatalf("seedImportDir: MkdirAll memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "notes.md"), []byte("# notes\n"), 0644); err != nil {
		t.Fatalf("seedImportDir: write memory file: %v", err)
	}

	return claudeProjectsDir, fakeCWD, authPath
}

// makeImportFlags creates ImportFlags with real credentials for integration tests.
func makeImportFlags(t *testing.T, authPath, claudeProjectsDir, fakeCWD string) cmd.ImportFlags {
	t.Helper()
	serverURL := intServerURL
	if serverURL == "" {
		serverURL = clicontext.DefaultServerURL
	}
	return cmd.ImportFlags{
		HostSlug:          intHSlug,
		ServerURL:         serverURL,
		AuthPath:          authPath,
		ClaudeProjectsDir: claudeProjectsDir,
		CWD:               fakeCWD,
		// ContextPath: leave empty so RunImport falls back to default
		// (HostSlug is provided explicitly via flag).
	}
}

// TestIntegration_Import_HappyPath imports 2 JSONL sessions into the real stack,
// waits for importRef to become null (session-agent unpacked), then asserts
// sessions are visible in NATS KV.
func TestIntegration_Import_HappyPath(t *testing.T) {
	if intJWT == "" {
		t.Skip("integration credentials not available")
	}

	claudeProjectsDir, fakeCWD, authPath := seedImportDir(t)
	flags := makeImportFlags(t, authPath, claudeProjectsDir, fakeCWD)

	var out strings.Builder
	result, err := cmd.RunImport(flags, &out)
	if err != nil {
		t.Fatalf("RunImport: %v\nOutput:\n%s", err, out.String())
	}
	t.Logf("RunImport output:\n%s", out.String())

	if result == nil {
		t.Fatal("RunImport returned nil result")
	}
	if result.ProjectSlug == "" {
		t.Fatal("RunImport result: ProjectSlug is empty")
	}
	if result.SessionCount < 2 {
		t.Errorf("SessionCount = %d; want >= 2", result.SessionCount)
	}

	// Record the project slug for teardown.
	intProjectSlug = result.ProjectSlug
	pslug := result.ProjectSlug

	t.Logf("Import complete: projectSlug=%s sessionCount=%d", pslug, result.SessionCount)

	// Poll GET /api/users/{uslug}/projects/{pslug} every 2s until importRef is null, timeout 60s.
	serverURL := intServerURL
	if serverURL == "" {
		serverURL = clicontext.DefaultServerURL
	}
	projectURL := fmt.Sprintf("%s/api/users/%s/projects/%s", serverURL, intUserSlug, pslug)

	deadline := time.Now().Add(60 * time.Second)
	importRefCleared := false
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		cleared, err := checkImportRefNull(projectURL, intJWT)
		if err != nil {
			t.Logf("poll importRef: %v (retrying)", err)
			continue
		}
		if cleared {
			importRefCleared = true
			break
		}
		t.Logf("importRef not yet null, polling...")
	}
	if !importRefCleared {
		t.Fatal("timed out waiting for importRef to become null — session-agent did not unpack within 60s")
	}

	t.Log("importRef is null — session-agent unpacked successfully")

	// Connect to NATS and assert sessions visible in KV.
	// Use the stored NATS URL from auth.json (ADR-0069); intNATSURL is populated
	// by TestMain with fallback to DeriveNATSURL.
	nc, err := connectTestNATS(intNATSURL, intJWT, intNKeySeed)
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}

	bucketName := "mclaude-sessions-" + intUserSlug
	kv, err := js.KeyValue(bucketName)
	if err != nil {
		t.Fatalf("KeyValue(%q): %v", bucketName, err)
	}

	// Watch the sessions prefix for this host+project.
	watchKey := fmt.Sprintf("hosts.%s.projects.%s.sessions.>", intHSlug, pslug)
	watcher, err := kv.Watch(watchKey)
	if err != nil {
		t.Fatalf("kv.Watch(%q): %v", watchKey, err)
	}
	defer watcher.Stop()

	// Drain watcher.Updates() until nil (initial values exhausted).
	entryCount := 0
	drainTimer := time.NewTimer(15 * time.Second)
	defer drainTimer.Stop()
drainLoop:
	for {
		select {
		case entry, ok := <-watcher.Updates():
			if !ok {
				break drainLoop
			}
			if entry == nil {
				// nil sentinel marks end of initial values.
				break drainLoop
			}
			entryCount++
			t.Logf("KV entry: key=%s", entry.Key())
		case <-drainTimer.C:
			t.Log("drain timer expired — stopping watcher")
			break drainLoop
		}
	}

	if entryCount < 1 {
		t.Errorf("expected at least 1 KV entry for sessions; got %d", entryCount)
	} else {
		t.Logf("KV watcher found %d session entries", entryCount)
	}
}

// TestIntegration_Import_SlugCollision imports once, then re-imports the same
// CWD. The second import detects slug collision and uses the injected name
// "my-project-2". Asserts HTTP 200 on GET .../projects/my-project-2.
func TestIntegration_Import_SlugCollision(t *testing.T) {
	if intJWT == "" {
		t.Skip("integration credentials not available")
	}

	claudeProjectsDir, fakeCWD, authPath := seedImportDir(t)
	flags := makeImportFlags(t, authPath, claudeProjectsDir, fakeCWD)

	serverURL := intServerURL
	if serverURL == "" {
		serverURL = clicontext.DefaultServerURL
	}

	// First import — create the project.
	var out1 strings.Builder
	result1, err := cmd.RunImport(flags, &out1)
	if err != nil {
		t.Fatalf("first RunImport: %v\nOutput:\n%s", err, out1.String())
	}
	t.Logf("first RunImport output:\n%s", out1.String())

	firstSlug := result1.ProjectSlug
	t.Logf("first import projectSlug=%s", firstSlug)

	// Second import — same CWD, slug collision. Inject "my-project-2\n" as input.
	flags2 := flags
	flags2.Input = strings.NewReader("my-project-2\n")

	var out2 strings.Builder
	result2, err := cmd.RunImport(flags2, &out2)
	if err != nil {
		t.Fatalf("second RunImport (slug collision): %v\nOutput:\n%s", err, out2.String())
	}
	t.Logf("second RunImport output:\n%s", out2.String())

	if result2.ProjectSlug != "my-project-2" {
		t.Errorf("second import ProjectSlug = %q; want %q", result2.ProjectSlug, "my-project-2")
	}

	// Confirm via HTTP GET.
	confirmURL := fmt.Sprintf("%s/api/users/%s/projects/my-project-2", serverURL, intUserSlug)
	httpReq, err := http.NewRequest(http.MethodGet, confirmURL, nil)
	if err != nil {
		t.Fatalf("build confirm request: %v", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+intJWT)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("GET %s: %v", confirmURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		t.Errorf("GET /api/users/%s/projects/my-project-2 returned %d; want 200\nbody: %s",
			intUserSlug, resp.StatusCode, body)
	}

	t.Logf("slug collision test passed: second project visible at my-project-2")
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// projectResponse is a partial decode of GET /api/users/{uslug}/projects/{pslug}.
// ADR-0065 requires the CP to include importRef so the test can poll for null.
type projectResponse struct {
	ImportRef *string `json:"importRef"`
}

// checkImportRefNull returns true if the project's importRef is null.
func checkImportRefNull(projectURL, jwt string) (bool, error) {
	httpReq, err := http.NewRequest(http.MethodGet, projectURL, nil)
	if err != nil {
		return false, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return false, fmt.Errorf("GET project returned %d: %s", resp.StatusCode, body)
	}
	var proj projectResponse
	if err := json.NewDecoder(resp.Body).Decode(&proj); err != nil {
		return false, fmt.Errorf("decode project response: %w", err)
	}
	return proj.ImportRef == nil, nil
}

// connectTestNATS connects to NATS using the test user's JWT + NKey seed.
func connectTestNATS(natsURL, jwt, nkeySeed string) (*nats.Conn, error) {
	kp, err := nkeys.FromSeed([]byte(nkeySeed))
	if err != nil {
		return nil, fmt.Errorf("parse nkey seed: %w", err)
	}
	nc, err := nats.Connect(natsURL,
		nats.UserJWT(
			func() (string, error) { return jwt, nil },
			func(nonce []byte) ([]byte, error) { return kp.Sign(nonce) },
		),
	)
	if err != nil {
		return nil, fmt.Errorf("nats.Connect(%s): %w", natsURL, err)
	}
	return nc, nil
}
