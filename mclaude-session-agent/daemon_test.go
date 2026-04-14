package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	testutil "mclaude-session-agent/testutil"
)

// skipIfNoDaemonDeps skips the test when Docker/INTEGRATION is unavailable.
func skipIfNoDaemonDeps(t *testing.T) {
	t.Helper()
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("set INTEGRATION=1 to run daemon integration tests (requires Docker)")
	}
}

// -----------------------------------------------------------------------
// Unit tests (no NATS needed)
// -----------------------------------------------------------------------

// TestJWTRemainingTTL verifies jwtRemainingTTL reads a creds file and returns
// the correct remaining TTL.
func TestJWTRemainingTTL(t *testing.T) {
	futureJWT := buildTestJWT(t, time.Now().Add(2*time.Hour))
	credsFile := writeTempCredsFile(t, futureJWT)

	ttl, err := jwtRemainingTTL(credsFile)
	if err != nil {
		t.Fatalf("jwtRemainingTTL: %v", err)
	}
	if ttl < 1*time.Hour || ttl > 3*time.Hour {
		t.Errorf("TTL out of expected range: got %v (want ~2h)", ttl)
	}
}

// TestJWTRemainingTTLExpired verifies that an expired JWT returns TTL=0.
func TestJWTRemainingTTLExpired(t *testing.T) {
	pastJWT := buildTestJWT(t, time.Now().Add(-1*time.Hour))
	credsFile := writeTempCredsFile(t, pastJWT)

	ttl, err := jwtRemainingTTL(credsFile)
	if err != nil {
		t.Fatalf("jwtRemainingTTL: %v", err)
	}
	if ttl != 0 {
		t.Errorf("expired JWT: expected TTL=0, got %v", ttl)
	}
}

// TestWriteJWTToCredsFile verifies that the JWT section is replaced and
// the rest of the creds file is preserved.
func TestWriteJWTToCredsFile(t *testing.T) {
	original := buildTestJWT(t, time.Now().Add(time.Hour))
	updated := buildTestJWT(t, time.Now().Add(8*time.Hour))

	credsFile := writeTempCredsFile(t, original)

	if err := writeJWTToCredsFile(credsFile, updated); err != nil {
		t.Fatalf("writeJWTToCredsFile: %v", err)
	}

	got, err := readJWTFromCredsFile(credsFile)
	if err != nil {
		t.Fatalf("readJWTFromCredsFile after write: %v", err)
	}
	if got != updated {
		t.Errorf("JWT not updated:\n  got  %q\n  want %q", got, updated)
	}

	// Markers must still be present.
	data, _ := os.ReadFile(credsFile)
	if !strings.Contains(string(data), "BEGIN NATS USER JWT") {
		t.Error("creds file missing BEGIN NATS USER JWT marker after update")
	}
	if !strings.Contains(string(data), "END NATS USER JWT") {
		t.Error("creds file missing END NATS USER JWT marker after update")
	}
}

// TestReadJWTFromCredsFileMissing verifies an error is returned for absent files.
func TestReadJWTFromCredsFileMissing(t *testing.T) {
	_, err := readJWTFromCredsFile("/nonexistent/path/creds")
	if err == nil {
		t.Error("expected error for missing creds file, got nil")
	}
}

// TestReadJWTFromCredsFileEmpty verifies an error is returned when no JWT is found.
func TestReadJWTFromCredsFileEmpty(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "creds-*")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("no jwt markers here\n")
	f.Close()

	_, err = readJWTFromCredsFile(f.Name())
	if err == nil {
		t.Error("expected error for creds file without JWT, got nil")
	}
}

// TestJWTRefreshCallsEndpoint verifies that maybeRefreshJWT calls the
// refresh endpoint when TTL is below the threshold.
func TestJWTRefreshCallsEndpoint(t *testing.T) {
	expiredSoonJWT := buildTestJWT(t, time.Now().Add(5*time.Minute)) // < 15 min threshold
	credsFile := writeTempCredsFile(t, expiredSoonJWT)

	newJWT := buildTestJWT(t, time.Now().Add(8*time.Hour))

	refreshCalled := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		refreshCalled <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"jwt": newJWT})
	}))
	defer srv.Close()

	daemon := &Daemon{
		cfg: DaemonConfig{
			NATSCredsFile: credsFile,
			RefreshURL:    srv.URL + "/auth/refresh",
			Log:           testLogger(t),
		},
	}

	daemon.maybeRefreshJWT(context.Background())

	select {
	case <-refreshCalled:
	case <-time.After(3 * time.Second):
		t.Error("refresh endpoint not called within timeout")
	}

	// The creds file should now contain the new JWT.
	got, err := readJWTFromCredsFile(credsFile)
	if err != nil {
		t.Fatalf("readJWTFromCredsFile after refresh: %v", err)
	}
	if got != newJWT {
		t.Errorf("creds file not updated with new JWT")
	}
}

// TestJWTRefreshSkipsWhenNotExpiringSoon verifies maybeRefreshJWT does NOT
// call the endpoint when TTL > threshold.
func TestJWTRefreshSkipsWhenNotExpiringSoon(t *testing.T) {
	freshJWT := buildTestJWT(t, time.Now().Add(2*time.Hour)) // > 15 min threshold
	credsFile := writeTempCredsFile(t, freshJWT)

	var mu sync.Mutex
	refreshCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		refreshCalled = true
		mu.Unlock()
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	daemon := &Daemon{
		cfg: DaemonConfig{
			NATSCredsFile: credsFile,
			RefreshURL:    srv.URL + "/auth/refresh",
			Log:           testLogger(t),
		},
	}

	daemon.maybeRefreshJWT(context.Background())

	mu.Lock()
	called := refreshCalled
	mu.Unlock()
	if called {
		t.Error("refresh endpoint should NOT be called when TTL > threshold")
	}
}

// TestDaemonRestartsChild verifies that manageChild restarts a crashed child.
func TestDaemonRestartsChild(t *testing.T) {
	trueBin := findTrueBinary(t)

	daemon := &Daemon{
		cfg: DaemonConfig{
			AgentBinary: trueBin,
			AgentArgs:   []string{},
			UserID:      "test-user",
			Log:         testLogger(t),
		},
		children: make(map[string]*managedChild),
	}

	child := &managedChild{
		projectID: "proj-restart-unit",
		stopCh:    make(chan struct{}),
	}
	daemon.mu.Lock()
	daemon.children["proj-restart-unit"] = child
	daemon.mu.Unlock()

	// Stop after some time so manageChild exits.
	go func() {
		// Give it time for at least one restart cycle.
		time.Sleep(300 * time.Millisecond)
		close(child.stopCh)
	}()

	start := time.Now()
	daemon.manageChild(child)
	elapsed := time.Since(start)

	if elapsed > 10*time.Second {
		t.Errorf("manageChild took too long: %v (expected < 10s)", elapsed)
	}
	// If we reach here, manageChild returned — it respects stopCh.
}

// TestDaemonCLIFlag verifies the --daemon flag appears in usage output.
func TestDaemonCLIFlag(t *testing.T) {
	bin := buildAgentBinary(t)

	cmd := exec.Command(bin, "--help")
	out, _ := cmd.CombinedOutput()

	for _, flag := range []string{"-daemon", "-hostname", "-machine-id", "-refresh-url"} {
		if !containsBytes(out, flag) {
			t.Errorf("--help output missing flag %q", flag)
		}
	}
}

// -----------------------------------------------------------------------
// Integration tests (require Docker / NATS)
// -----------------------------------------------------------------------

// TestDaemonHostnameCollisionSameID verifies no error when same machineID is registered.
func TestDaemonHostnameCollisionSameID(t *testing.T) {
	skipIfNoDaemonDeps(t)
	deps := testutil.StartDeps(t)

	ctx := context.Background()
	kv, err := deps.JetStream.KeyValue(ctx, "mclaude-laptops")
	if err != nil {
		t.Fatalf("get laptops KV: %v", err)
	}

	entry, _ := json.Marshal(laptopEntry{MachineID: "machine-abc", TS: time.Now().Format(time.RFC3339)})
	_, _ = kv.Put(ctx, "user1.my-laptop", entry)

	nc, err := nats.Connect(deps.NATSURL)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer nc.Close()

	daemon, err := NewDaemon(nc, DaemonConfig{
		UserID:    "user1",
		Hostname:  "my-laptop",
		MachineID: "machine-abc",
		Log:       testLogger(t),
	})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}

	if err := daemon.checkHostnameCollision(ctx); err != nil {
		t.Errorf("no collision expected for same machineID, got: %v", err)
	}
}

// TestDaemonHostnameCollisionDifferentID verifies error when different machineID is registered.
func TestDaemonHostnameCollisionDifferentID(t *testing.T) {
	skipIfNoDaemonDeps(t)
	deps := testutil.StartDeps(t)

	ctx := context.Background()
	kv, err := deps.JetStream.KeyValue(ctx, "mclaude-laptops")
	if err != nil {
		t.Fatalf("get laptops KV: %v", err)
	}

	entry, _ := json.Marshal(laptopEntry{MachineID: "other-machine", TS: time.Now().Format(time.RFC3339)})
	_, _ = kv.Put(ctx, "user1.contested-laptop", entry)

	nc, err := nats.Connect(deps.NATSURL)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer nc.Close()

	daemon, err := NewDaemon(nc, DaemonConfig{
		UserID:    "user1",
		Hostname:  "contested-laptop",
		MachineID: "my-machine",
		Log:       testLogger(t),
	})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}

	err = daemon.checkHostnameCollision(ctx)
	if err == nil {
		t.Error("expected collision error, got nil")
	}
	if !strings.Contains(err.Error(), "already registered to another machine") {
		t.Errorf("error should mention collision: %v", err)
	}
}

// TestDaemonSpawnsChildOnProjectCreate verifies that the daemon spawns a child
// when a projects.create message arrives via NATS.
func TestDaemonSpawnsChildOnProjectCreate(t *testing.T) {
	skipIfNoDaemonDeps(t)
	deps := testutil.StartDeps(t)

	nc, err := nats.Connect(deps.NATSURL)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer nc.Close()

	trueBin := findTrueBinary(t)

	daemon, err := NewDaemon(nc, DaemonConfig{
		UserID:      "daemon-user",
		Hostname:    "test-laptop",
		MachineID:   "test-machine",
		AgentBinary: trueBin,
		AgentArgs:   []string{},
		Log:         testLogger(t),
	})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- daemon.Run(ctx) }()

	// Allow subscription to be established.
	time.Sleep(50 * time.Millisecond)

	// Publish projects.create.
	subject := fmt.Sprintf("mclaude.%s.api.projects.create", "daemon-user")
	payload, _ := json.Marshal(map[string]string{"projectId": "proj-daemon-integ"})
	if err := nc.Publish(subject, payload); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Wait for the child to be registered.
	deadline := time.Now().Add(5 * time.Second)
	var childFound bool
	for time.Now().Before(deadline) {
		daemon.mu.Lock()
		_, found := daemon.children["proj-daemon-integ"]
		daemon.mu.Unlock()
		if found {
			childFound = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !childFound {
		t.Error("daemon did not register child for project within 5s")
	}

	cancel()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Error("daemon.Run did not return after cancel")
	}
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

// buildTestJWT constructs a parseable (but not cryptographically valid) JWT
// with the given exp claim. Suitable for jwtTTL parsing tests.
func buildTestJWT(t *testing.T, exp time.Time) string {
	t.Helper()
	b64url := func(b []byte) string {
		s := base64.StdEncoding.EncodeToString(b)
		s = strings.ReplaceAll(s, "+", "-")
		s = strings.ReplaceAll(s, "/", "_")
		s = strings.TrimRight(s, "=")
		return s
	}
	header := b64url([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, _ := json.Marshal(map[string]any{
		"sub": "test",
		"exp": exp.Unix(),
		"iat": time.Now().Unix(),
	})
	body := b64url(payload)
	sig := b64url([]byte("fakesig"))
	return header + "." + body + "." + sig
}

// writeTempCredsFile writes a minimal NATS creds file containing jwtStr.
func writeTempCredsFile(t *testing.T, jwtStr string) string {
	t.Helper()
	content := fmt.Sprintf(
		"-----BEGIN NATS USER JWT-----\n%s\n------END NATS USER JWT------\n",
		jwtStr,
	)
	path := t.TempDir() + "/test.creds"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write creds file: %v", err)
	}
	return path
}

// findTrueBinary returns the path to a binary that always exits 0.
// Uses /usr/bin/true on Unix.
func findTrueBinary(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{"/usr/bin/true", "/bin/true"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	// Fall back to building a tiny Go binary that exits 0.
	dir := t.TempDir()
	src := dir + "/main.go"
	bin := dir + "/true"
	if err := os.WriteFile(src, []byte("package main\nfunc main() {}"), 0644); err != nil {
		t.Fatalf("write true source: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build true binary: %s: %v", out, err)
	}
	return bin
}
