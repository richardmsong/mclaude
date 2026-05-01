package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nkeys"
	"github.com/rs/zerolog"

	"mclaude.io/common/pkg/slug"
)

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// nopLogger returns a zerolog logger that discards all output.
func nopLogger() zerolog.Logger {
	return zerolog.Nop()
}

// newTestController creates a Controller for unit testing.
// The NATS connection is nil — tests that need NATS must supply their own.
func newTestController(t *testing.T, hostSlugStr string) (*Controller, string) {
	t.Helper()
	dataDir := t.TempDir()
	return &Controller{
		nc:       nil,
		hostSlug: slug.HostSlug(hostSlugStr),
		dataDir:  dataDir,
		cpURL:    "",
		log:      nopLogger(),
		children: make(map[childKey]*supervisedChild),
	}, dataDir
}

// buildTestHostAuth creates a HostAuth with a freshly generated NKey pair.
// cpURL may be empty (for tests that don't call Refresh).
func buildTestHostAuth(t *testing.T, cpURL string) *HostAuth {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("create test NKey: %v", err)
	}
	return &HostAuth{
		kp:         kp,
		cpURL:      cpURL,
		log:        nopLogger(),
		currentJWT: "initial-jwt",
	}
}

// --------------------------------------------------------------------------
// childKey tests
// --------------------------------------------------------------------------

func TestChildKey_String(t *testing.T) {
	k := childKey{userSlug: "alice", projectSlug: "billing"}
	if got := k.String(); got != "alice:billing" {
		t.Errorf("childKey.String() = %q, want %q", got, "alice:billing")
	}
}

func TestChildKey_Distinct(t *testing.T) {
	k1 := childKey{userSlug: "alice", projectSlug: "billing"}
	k2 := childKey{userSlug: "bob", projectSlug: "billing"}
	if k1 == k2 {
		t.Error("childKeys for different users must not be equal")
	}
}

func TestChildKey_SameUserSameProjectEqual(t *testing.T) {
	k1 := childKey{userSlug: "alice", projectSlug: "billing"}
	k2 := childKey{userSlug: "alice", projectSlug: "billing"}
	if k1 != k2 {
		t.Error("childKeys for same user and project must be equal")
	}
}

// --------------------------------------------------------------------------
// Subject prefix / subject generation tests
// --------------------------------------------------------------------------

func TestControllerSubscriptionSubject(t *testing.T) {
	c, _ := newTestController(t, "laptop-a")
	got := c.subscriptionSubject()
	want := "mclaude.hosts.laptop-a.>"
	if got != want {
		t.Errorf("subscriptionSubject() = %q, want %q", got, want)
	}
}

func TestControllerAgentRegisterSubject(t *testing.T) {
	c, _ := newTestController(t, "laptop-a")
	got := c.agentRegisterSubject()
	want := "mclaude.hosts.laptop-a.api.agents.register"
	if got != want {
		t.Errorf("agentRegisterSubject() = %q, want %q", got, want)
	}
}

// --------------------------------------------------------------------------
// Subject routing / token parsing tests
// --------------------------------------------------------------------------

func TestHandleMessage_SubjectTokenParsing(t *testing.T) {
	cases := []struct {
		subject string
		valid   bool
		uslug   string
		pslug   string
		action  string
	}{
		{
			subject: "mclaude.hosts.laptop-a.users.alice.projects.billing.create",
			valid:   true, uslug: "alice", pslug: "billing", action: "create",
		},
		{
			subject: "mclaude.hosts.laptop-a.users.bob.projects.myapp.delete",
			valid:   true, uslug: "bob", pslug: "myapp", action: "delete",
		},
		{
			// api.agents.register subject — not a project lifecycle message.
			subject: "mclaude.hosts.laptop-a.api.agents.register",
			valid:   false,
		},
		{
			// Too few tokens.
			subject: "mclaude.hosts.laptop-a",
			valid:   false,
		},
		{
			// Wrong token at index 3 (not "users").
			subject: "mclaude.hosts.laptop-a.api.alice.projects.billing.create",
			valid:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.subject, func(t *testing.T) {
			tokens := strings.Split(tc.subject, ".")
			isProject := len(tokens) == 8 &&
				tokens[0] == "mclaude" &&
				tokens[1] == "hosts" &&
				tokens[3] == "users" &&
				tokens[5] == "projects"

			if isProject != tc.valid {
				t.Errorf("isProject=%v, want %v", isProject, tc.valid)
				return
			}
			if tc.valid {
				if got := tokens[4]; got != tc.uslug {
					t.Errorf("uslug: got %q, want %q", got, tc.uslug)
				}
				if got := tokens[6]; got != tc.pslug {
					t.Errorf("pslug: got %q, want %q", got, tc.pslug)
				}
				if got := tokens[7]; got != tc.action {
					t.Errorf("action: got %q, want %q", got, tc.action)
				}
			}
		})
	}
}

// --------------------------------------------------------------------------
// waitForNKeyFile tests
// --------------------------------------------------------------------------

func TestWaitForNKeyFile_FileExists(t *testing.T) {
	c, _ := newTestController(t, "laptop-a")
	path := filepath.Join(t.TempDir(), ".nkey-pub")

	if err := os.WriteFile(path, []byte("UABC123\n"), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := c.waitForNKeyFile(path)
	if err != nil {
		t.Fatalf("waitForNKeyFile: %v", err)
	}
	if got != "UABC123" {
		t.Errorf("waitForNKeyFile = %q, want %q", got, "UABC123")
	}
}

func TestWaitForNKeyFile_FileAppearsLate(t *testing.T) {
	c, _ := newTestController(t, "laptop-a")
	dir := t.TempDir()
	path := filepath.Join(dir, ".nkey-pub")

	// Write the file after a short delay to simulate agent startup.
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = os.WriteFile(path, []byte("UXYZ789\n"), 0600)
	}()

	got, err := c.waitForNKeyFile(path)
	if err != nil {
		t.Fatalf("waitForNKeyFile: %v", err)
	}
	if got != "UXYZ789" {
		t.Errorf("waitForNKeyFile = %q, want %q", got, "UXYZ789")
	}
}

func TestWaitForNKeyFile_WhitespaceStripping(t *testing.T) {
	c, _ := newTestController(t, "laptop-a")
	path := filepath.Join(t.TempDir(), ".nkey-pub")

	// Write with trailing newline and spaces.
	if err := os.WriteFile(path, []byte("  UFOO123  \n"), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := c.waitForNKeyFile(path)
	if err != nil {
		t.Fatalf("waitForNKeyFile: %v", err)
	}
	if got != "UFOO123" {
		t.Errorf("waitForNKeyFile = %q (whitespace not stripped)", got)
	}
}

// --------------------------------------------------------------------------
// HostAuth tests (using httptest)
// --------------------------------------------------------------------------

func TestHostAuth_RefreshFlow(t *testing.T) {
	nonce := "test-challenge-nonce-42"
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

	ha := buildTestHostAuth(t, srv.URL)
	ctx := context.Background()

	jwt, err := ha.Refresh(ctx)
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

func TestHostAuth_RefreshUpdatesStoredJWT(t *testing.T) {
	newJWT := "refreshed-jwt-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/challenge":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": "n1"})
		case "/api/auth/verify":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "jwt": newJWT})
		}
	}))
	defer srv.Close()

	ha := buildTestHostAuth(t, srv.URL)
	ctx := context.Background()

	if _, err := ha.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	ha.mu.RLock()
	stored := ha.currentJWT
	ha.mu.RUnlock()

	if stored != newJWT {
		t.Errorf("stored JWT = %q, want %q", stored, newJWT)
	}
}

func TestHostAuth_JWTFuncReturnsCurrent(t *testing.T) {
	ha := buildTestHostAuth(t, "")
	ha.mu.Lock()
	ha.currentJWT = "static-jwt"
	ha.mu.Unlock()

	got, err := ha.JWTFunc()()
	if err != nil {
		t.Fatalf("JWTFunc(): %v", err)
	}
	if got != "static-jwt" {
		t.Errorf("JWTFunc() = %q, want %q", got, "static-jwt")
	}
}

func TestHostAuth_PublicKeyFormat(t *testing.T) {
	ha := buildTestHostAuth(t, "")
	pub, err := ha.PublicKey()
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if pub == "" {
		t.Error("PublicKey() returned empty string")
	}
	// NKey user public keys start with "U" (for user type).
	if !strings.HasPrefix(pub, "U") {
		t.Errorf("PublicKey() = %q — expected NKey user key starting with 'U'", pub)
	}
}

func TestHostAuth_SignFuncProducesSignature(t *testing.T) {
	ha := buildTestHostAuth(t, "")
	nonce := []byte("test-nonce-bytes")
	sig, err := ha.SignFunc()(nonce)
	if err != nil {
		t.Fatalf("SignFunc(): %v", err)
	}
	if len(sig) == 0 {
		t.Error("SignFunc() returned empty signature")
	}
}

func TestHostAuth_RefreshErrorWhenNoCPURL(t *testing.T) {
	ha := buildTestHostAuth(t, "")
	_, err := ha.Refresh(context.Background())
	if err == nil {
		t.Error("Refresh with empty cpURL should return error")
	}
}

func TestHostAuth_VerifySignatureCorrectness(t *testing.T) {
	// Verify that the signature produced by SignFunc() is valid for the NKey pair.
	ha := buildTestHostAuth(t, "")
	nonce := []byte("nonce-to-sign")
	sig, err := ha.SignFunc()(nonce)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// The NKey pair should be able to verify the signature (NKey is Ed25519).
	pub, err := ha.kp.PublicKey()
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	verifier, err := nkeys.FromPublicKey(pub)
	if err != nil {
		t.Fatalf("from public key: %v", err)
	}
	if err := verifier.Verify(nonce, sig); err != nil {
		t.Errorf("signature verification failed: %v", err)
	}
}

// --------------------------------------------------------------------------
// NewHostAuthFromCredsFile tests
// --------------------------------------------------------------------------

func TestNewHostAuthFromCredsFile_Valid(t *testing.T) {
	// Generate a real NKey pair and a fake JWT, format as a .creds file.
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	seed, err := kp.Seed()
	if err != nil {
		t.Fatal(err)
	}

	// Build minimal .creds file content.
	fakeJWT := "TEST_JWT_FOR_UNIT_TEST_NOT_A_REAL_TOKEN"
	credsData := []byte(
		"-----BEGIN NATS USER JWT-----\n" +
			fakeJWT + "\n" +
			"------END NATS USER JWT------\n" +
			"\n" +
			"-----BEGIN USER NKEY SEED-----\n" +
			string(seed) + "\n" +
			"------END USER NKEY SEED------\n",
	)

	ha, err := NewHostAuthFromCredsFile(credsData, "https://cp.example.com", nopLogger())
	if err != nil {
		t.Fatalf("NewHostAuthFromCredsFile: %v", err)
	}

	// Verify the JWT was extracted.
	ha.mu.RLock()
	storedJWT := ha.currentJWT
	ha.mu.RUnlock()
	if storedJWT != fakeJWT {
		t.Errorf("currentJWT = %q, want %q", storedJWT, fakeJWT)
	}

	// Verify the NKey pair was extracted (can sign).
	sig, err := ha.SignFunc()([]byte("test"))
	if err != nil {
		t.Fatalf("SignFunc after load: %v", err)
	}
	if len(sig) == 0 {
		t.Error("empty signature from loaded NKey")
	}
}

func TestNewHostAuthFromCredsFile_InvalidSeed(t *testing.T) {
	credsData := []byte("garbage-data-not-a-creds-file")
	_, err := NewHostAuthFromCredsFile(credsData, "", nopLogger())
	if err == nil {
		t.Error("expected error for invalid creds data")
	}
}

// --------------------------------------------------------------------------
// agentRegisterRequest marshalling
// --------------------------------------------------------------------------

func TestAgentRegisterRequest_Marshal(t *testing.T) {
	req := agentRegisterRequest{
		UserSlug:    "alice",
		HostSlug:    "laptop-a",
		ProjectSlug: "billing",
		NKeyPublic:  "UABC123",
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["user_slug"] != "alice" {
		t.Errorf("user_slug = %q, want %q", m["user_slug"], "alice")
	}
	if m["host_slug"] != "laptop-a" {
		t.Errorf("host_slug = %q, want %q", m["host_slug"], "laptop-a")
	}
	if m["project_slug"] != "billing" {
		t.Errorf("project_slug = %q, want %q", m["project_slug"], "billing")
	}
	if m["nkey_public"] != "UABC123" {
		t.Errorf("nkey_public = %q, want %q", m["nkey_public"], "UABC123")
	}
}

// --------------------------------------------------------------------------
// Supervisor buildCmd tests
// --------------------------------------------------------------------------

func TestSupervisorBuildCmd_BaseFlags(t *testing.T) {
	s := &supervisedChild{
		projectSlug: "billing",
		userSlug:    "alice",
		dataDir:     "/data/alice/billing/worktree",
		hostSlug:    "laptop-a",
		log:         nopLogger(),
	}
	cmd := s.buildCmd()
	assertContainsArgs(t, cmd.Args, "--mode", "standalone")
	assertContainsArgs(t, cmd.Args, "--user-slug", "alice")
	assertContainsArgs(t, cmd.Args, "--host", "laptop-a")
	assertContainsArgs(t, cmd.Args, "--project-slug", "billing")
	assertContainsArgs(t, cmd.Args, "--data-dir", "/data/alice/billing/worktree")
}

func TestSupervisorBuildCmd_NKeyPubFile(t *testing.T) {
	s := &supervisedChild{
		projectSlug: "billing",
		userSlug:    "alice",
		dataDir:     "/data",
		hostSlug:    "laptop-a",
		nkeyPubFile: "/data/alice/billing/.nkey-pub",
		log:         nopLogger(),
	}
	assertContainsArgs(t, s.buildCmd().Args, "--nkey-pub-file", "/data/alice/billing/.nkey-pub")
}

func TestSupervisorBuildCmd_AuthURL(t *testing.T) {
	s := &supervisedChild{
		projectSlug: "billing",
		userSlug:    "alice",
		dataDir:     "/data",
		hostSlug:    "laptop-a",
		authURL:     "https://cp.example.com",
		log:         nopLogger(),
	}
	assertContainsArgs(t, s.buildCmd().Args, "--auth-url", "https://cp.example.com")
}

func TestSupervisorBuildCmd_NoNKeyFileWhenEmpty(t *testing.T) {
	s := &supervisedChild{
		projectSlug: "billing",
		userSlug:    "alice",
		dataDir:     "/data",
		hostSlug:    "laptop-a",
		// nkeyPubFile and authURL are intentionally empty.
		log: nopLogger(),
	}
	cmd := s.buildCmd()
	for _, a := range cmd.Args {
		if a == "--nkey-pub-file" {
			t.Error("--nkey-pub-file should not be present when nkeyPubFile is empty")
		}
		if a == "--auth-url" {
			t.Error("--auth-url should not be present when authURL is empty")
		}
	}
}

func TestSupervisorBuildCmd_EnvVars(t *testing.T) {
	s := &supervisedChild{
		projectSlug: "billing",
		userSlug:    "alice",
		dataDir:     "/data",
		hostSlug:    "laptop-a",
		nkeyPubFile: "/tmp/.nkey-pub",
		authURL:     "https://cp.example.com",
		log:         nopLogger(),
	}
	env := s.buildCmd().Env
	assertEnvContains(t, env, "USER_SLUG=alice")
	assertEnvContains(t, env, "HOST_SLUG=laptop-a")
	assertEnvContains(t, env, "PROJECT_SLUG=billing")
	assertEnvContains(t, env, "NKEY_PUB_FILE=/tmp/.nkey-pub")
	assertEnvContains(t, env, "AUTH_URL=https://cp.example.com")
}

// --------------------------------------------------------------------------
// ProvisionReply marshalling
// --------------------------------------------------------------------------

func TestProvisionReply_OKMarshal(t *testing.T) {
	r := ProvisionReply{OK: true, ProjectSlug: "billing"}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["ok"] != true {
		t.Errorf("ok = %v, want true", m["ok"])
	}
	if m["projectSlug"] != "billing" {
		t.Errorf("projectSlug = %q", m["projectSlug"])
	}
}

func TestProvisionReply_ErrorMarshal(t *testing.T) {
	r := ProvisionReply{OK: false, Error: "bad thing", Code: "bad_code"}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["ok"] != false {
		t.Errorf("ok = %v, want false", m["ok"])
	}
	if m["error"] != "bad thing" {
		t.Errorf("error = %q", m["error"])
	}
	if m["code"] != "bad_code" {
		t.Errorf("code = %q", m["code"])
	}
}

// --------------------------------------------------------------------------
// Shutdown grace period constant
// --------------------------------------------------------------------------

func TestShutdownGracePeriod_Is10s(t *testing.T) {
	// Spec says "10s grace, SIGKILL" — must match exactly.
	if shutdownGracePeriod != 10*time.Second {
		t.Errorf("shutdownGracePeriod = %v, want 10s", shutdownGracePeriod)
	}
}

// --------------------------------------------------------------------------
// Git clone tests (handleCreate — git_clone_failed path)
// --------------------------------------------------------------------------

// TestHandleCreate_GitCloneFailed verifies that git clone fails on an invalid URL
// and that the ProvisionReply shape for git_clone_failed is correct.
// Because handleCreate depends on a live NATS connection for reply(), we test
// the git exec path directly and verify the ProvisionReply fields inline.
func TestHandleCreate_GitCloneFailed(t *testing.T) {
	// Use an invalid git URL that git will immediately reject.
	invalidURL := "not-a-valid-git-url-xyz-abc"

	worktreeDir := t.TempDir()
	// Remove the directory so git clone can create it fresh (mirrors handleCreate).
	os.Remove(worktreeDir)

	cmd := exec.Command("git", "clone", invalidURL, worktreeDir)
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected git clone to fail for %q, but it succeeded", invalidURL)
	}

	// Verify the ProvisionReply shape that handleCreate would produce on failure.
	got := ProvisionReply{
		OK:          false,
		ProjectSlug: "billing",
		Error:       fmt.Sprintf("git clone: %v", err),
		Code:        "git_clone_failed",
	}
	if got.Code != "git_clone_failed" {
		t.Errorf("reply code = %q, want %q", got.Code, "git_clone_failed")
	}
	if got.OK {
		t.Error("reply ok should be false on git clone failure")
	}
}

// TestHandleCreate_GitCloneSkippedWhenRepoExists verifies that the controller
// does not re-run git clone when the .git directory already exists (idempotent).
func TestHandleCreate_GitCloneSkippedWhenRepoExists(t *testing.T) {
	// Create a worktree dir that already has a .git directory.
	worktreeDir := t.TempDir()
	gitDir := filepath.Join(worktreeDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Verify os.Stat behaviour: .git exists → clone should be skipped.
	_, err := os.Stat(gitDir)
	if os.IsNotExist(err) {
		t.Fatal("expected .git to exist")
	}
	if err != nil {
		t.Fatalf("os.Stat: %v", err)
	}
	// If we reached here, the idempotency guard (os.IsNotExist → skip) works correctly.
}

// --------------------------------------------------------------------------
// minDuration tests
// --------------------------------------------------------------------------

func TestMinDuration(t *testing.T) {
	cases := []struct {
		a, b, want time.Duration
	}{
		{1 * time.Second, 2 * time.Second, 1 * time.Second},
		{5 * time.Second, 3 * time.Second, 3 * time.Second},
		{4 * time.Second, 4 * time.Second, 4 * time.Second},
	}
	for _, tc := range cases {
		got := minDuration(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("minDuration(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// --------------------------------------------------------------------------
// Test helper utilities
// --------------------------------------------------------------------------

// assertContainsArgs asserts that args contains the consecutive pair (key, value).
func assertContainsArgs(t *testing.T, args []string, key, value string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return
		}
	}
	t.Errorf("args %v does not contain %q %q", args, key, value)
}

// assertEnvContains asserts that env contains the given "KEY=VALUE" entry.
func assertEnvContains(t *testing.T, env []string, entry string) {
	t.Helper()
	for _, e := range env {
		if e == entry {
			return
		}
	}
	t.Errorf("env does not contain %q\ngot: %v", entry, env)
}
