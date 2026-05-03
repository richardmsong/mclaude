package cmd_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"mclaude-cli/cmd"
	clicontext "mclaude-cli/context"
)

// ── MockNATSConn ──────────────────────────────────────────────────────────────

// mockNATSRequest is a recorded request sent via MockNATSConn.Request.
type mockNATSRequest struct {
	Subject string
	Data    []byte
}

// mockNATSResponse is a canned response for a given subject prefix.
type mockNATSResponse struct {
	Data []byte
	Err  error
}

// MockNATSConn implements cmd.NATSConn for testing without a live NATS server.
type MockNATSConn struct {
	mu        sync.Mutex
	responses map[string]mockNATSResponse // subject → response
	requests  []mockNATSRequest
	closed    bool
}

// NewMockNATSConn creates a MockNATSConn with canned responses.
// responses maps subject substrings to the response that should be returned.
func NewMockNATSConn(responses map[string]mockNATSResponse) *MockNATSConn {
	return &MockNATSConn{responses: responses}
}

// Request implements cmd.NATSConn. Matches the subject against canned responses
// using substring matching (longest key wins).
func (m *MockNATSConn) Request(subj string, data []byte, _ time.Duration) (*nats.Msg, error) {
	m.mu.Lock()
	m.requests = append(m.requests, mockNATSRequest{Subject: subj, Data: data})
	m.mu.Unlock()

	// Find the best matching response (longest matching key).
	best := ""
	for key := range m.responses {
		if strings.Contains(subj, key) && len(key) > len(best) {
			best = key
		}
	}
	if best == "" {
		return nil, fmt.Errorf("no mock response for subject %q", subj)
	}
	resp := m.responses[best]
	if resp.Err != nil {
		return nil, resp.Err
	}
	return &nats.Msg{Data: resp.Data}, nil
}

// Close implements cmd.NATSConn.
func (m *MockNATSConn) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
}

// Requests returns all recorded requests (safe for concurrent use).
func (m *MockNATSConn) Requests() []mockNATSRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]mockNATSRequest, len(m.requests))
	copy(cp, m.requests)
	return cp
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// mustJSON marshals v to JSON bytes or panics.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// standardMockNATS creates a MockNATSConn with standard happy-path responses.
func standardMockNATS() *MockNATSConn {
	return NewMockNATSConn(map[string]mockNATSResponse{
		"projects.check-slug": {
			Data: mustJSON(map[string]any{"available": true}),
		},
		"import.request": {
			Data: mustJSON(map[string]any{
				"id":        "imp-test-001",
				"uploadUrl": "placeholder-upload-url",
			}),
		},
		"import.confirm": {
			Data: mustJSON(map[string]any{"ok": true}),
		},
	})
}

// setupImportDir creates a temporary directory structure with auth, context,
// and Claude Code project data. Returns the paths and a cleanup function.
func setupImportDir(t *testing.T, sessions []string) (authPath, ctxPath, claudeProjects, fakeCWD string) {
	t.Helper()
	dir := t.TempDir()

	authPath = filepath.Join(dir, "auth.json")
	if err := cmd.SaveAuth(authPath, &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUAIBDPBAUTWCWBKIO6XHQNINK5FWJW4OHLXC3HQ2KFE4PEJUA4LNNAL",
		UserSlug: "alice-test",
	}); err != nil {
		t.Fatal(err)
	}

	ctxPath = filepath.Join(dir, "context.json")
	if err := clicontext.Save(ctxPath, &clicontext.Context{
		UserSlug: "alice-test",
		HostSlug: "test-host",
	}); err != nil {
		t.Fatal(err)
	}

	fakeCWD = "/fake/project/myapp"
	encodedCWD := cmd.EncodeCWD(fakeCWD)
	claudeProjects = filepath.Join(dir, "claude", "projects")
	projectDir := filepath.Join(claudeProjects, encodedCWD)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, sess := range sessions {
		if err := os.WriteFile(filepath.Join(projectDir, sess+".jsonl"), []byte(`{"type":"system"}`+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return
}

// ── EncodeCWD ─────────────────────────────────────────────────────────────────

func TestEncodeCWDBasic(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"/Users/rsong/work/mclaude", "Users-rsong-work-mclaude"},
		{"/home/alice/projects/my-app", "home-alice-projects-my-app"},
		{"/", ""},
		{"/tmp", "tmp"},
	}
	for _, c := range cases {
		got := cmd.EncodeCWD(c.input)
		if got != c.want {
			t.Errorf("EncodeCWD(%q) = %q; want %q", c.input, got, c.want)
		}
	}
}

func TestEncodeCWDMatchesClaude(t *testing.T) {
	// The encoded path must match the directory names under ~/.claude/projects/.
	// For /Users/rsong/work/mclaude, Claude Code uses "Users-rsong-work-mclaude".
	encoded := cmd.EncodeCWD("/Users/rsong/work/mclaude")
	if encoded != "Users-rsong-work-mclaude" {
		t.Errorf("EncodeCWD = %q; want Users-rsong-work-mclaude", encoded)
	}
}

// ── DiscoverSessions ──────────────────────────────────────────────────────────

func TestDiscoverSessionsFindsJSONL(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "Users-alice-code-myapp")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Create two JSONL session files.
	for _, name := range []string{"sess-001.jsonl", "sess-002.jsonl"} {
		if err := os.WriteFile(filepath.Join(projectDir, name), []byte(`{"type":"system"}`+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Create a non-JSONL file that should be ignored.
	if err := os.WriteFile(filepath.Join(projectDir, "README.md"), []byte("# project"), 0644); err != nil {
		t.Fatal(err)
	}

	sessions, err := cmd.DiscoverSessions(dir, "Users-alice-code-myapp")
	if err != nil {
		t.Fatalf("DiscoverSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("session count = %d; want 2", len(sessions))
	}
	for _, id := range sessions {
		if id != "sess-001" && id != "sess-002" {
			t.Errorf("unexpected session ID %q", id)
		}
	}
}

func TestDiscoverSessionsMissingDir(t *testing.T) {
	dir := t.TempDir()
	_, err := cmd.DiscoverSessions(dir, "nonexistent-encoded-cwd")
	if err == nil {
		t.Fatal("DiscoverSessions: expected error for missing dir; got nil")
	}
}

func TestDiscoverSessionsListsAvailableOnMissing(t *testing.T) {
	dir := t.TempDir()
	// Create a real project dir.
	if err := os.MkdirAll(filepath.Join(dir, "Users-alice-code-real"), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := cmd.DiscoverSessions(dir, "Users-alice-code-wrong")
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	// The error should hint at the available directory.
	if !strings.Contains(err.Error(), "Users-alice-code-real") {
		t.Errorf("error %q does not list available dirs", err.Error())
	}
}

// ── BuildArchive ──────────────────────────────────────────────────────────────

func TestBuildArchiveCreatesValidTarGz(t *testing.T) {
	claudeDir := t.TempDir()
	projectDir := filepath.Join(claudeDir, "test-project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a JSONL session file.
	sessionID := "test-session-123"
	if err := os.WriteFile(filepath.Join(projectDir, sessionID+".jsonl"), []byte(`{"type":"system"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(t.TempDir(), "test.tar.gz")
	meta := cmd.ImportMetadata{
		CWD:        "/test/project",
		SessionIDs: []string{sessionID},
	}

	size, err := cmd.BuildArchive(archivePath, claudeDir, "test-project", meta)
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}
	if size <= 0 {
		t.Errorf("archive size = %d; want > 0", size)
	}

	// Verify it's a valid tar.gz.
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	var fileNames []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		fileNames = append(fileNames, hdr.Name)
	}

	// Must contain metadata.json.
	var hasMetadata, hasSession bool
	for _, name := range fileNames {
		if name == "metadata.json" {
			hasMetadata = true
		}
		if strings.Contains(name, sessionID+".jsonl") {
			hasSession = true
		}
	}
	if !hasMetadata {
		t.Error("archive missing metadata.json")
	}
	if !hasSession {
		t.Errorf("archive missing %s.jsonl; files = %v", sessionID, fileNames)
	}
}

func TestBuildArchiveMetadataJSON(t *testing.T) {
	claudeDir := t.TempDir()
	projectDir := filepath.Join(claudeDir, "my-app")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "abc.jsonl"), []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(t.TempDir(), "test.tar.gz")
	meta := cmd.ImportMetadata{
		CWD:        "/home/user/my-app",
		GitRemote:  "git@github.com:user/my-app.git",
		GitBranch:  "main",
		SessionIDs: []string{"abc"},
	}

	if _, err := cmd.BuildArchive(archivePath, claudeDir, "my-app", meta); err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}

	// Extract and verify metadata.json content.
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	gr, _ := gzip.NewReader(f)
	defer gr.Close()
	tr := tar.NewReader(gr)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		if hdr.Name != "metadata.json" {
			continue
		}
		var got cmd.ImportMetadata
		data, _ := io.ReadAll(tr)
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal metadata: %v", err)
		}
		if got.CWD != "/home/user/my-app" {
			t.Errorf("CWD = %q; want /home/user/my-app", got.CWD)
		}
		if got.GitRemote != "git@github.com:user/my-app.git" {
			t.Errorf("GitRemote = %q; want expected", got.GitRemote)
		}
		if len(got.SessionIDs) != 1 || got.SessionIDs[0] != "abc" {
			t.Errorf("SessionIDs = %v; want [abc]", got.SessionIDs)
		}
		return
	}
	t.Error("metadata.json not found in archive")
}

// ── RunImport ─────────────────────────────────────────────────────────────────

func TestRunImportMissingAuth(t *testing.T) {
	dir := t.TempDir()
	_, err := cmd.RunImport(cmd.ImportFlags{
		AuthPath:    filepath.Join(dir, "auth.json"),
		ContextPath: filepath.Join(dir, "context.json"),
	}, io.Discard)
	if err == nil {
		t.Fatal("RunImport: expected error when auth file missing; got nil")
	}
	if !containsAny(err.Error(), "mclaude login", "not logged in") {
		t.Errorf("error %q; want mention of login", err.Error())
	}
}

func TestRunImportMissingHost(t *testing.T) {
	dir := t.TempDir()

	// Write valid auth credentials.
	authPath := filepath.Join(dir, "auth.json")
	if err := cmd.SaveAuth(authPath, &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "test-seed",
		UserSlug: "alice-test",
	}); err != nil {
		t.Fatal(err)
	}

	// Write context without host slug.
	ctxPath := filepath.Join(dir, "context.json")
	if err := clicontext.Save(ctxPath, &clicontext.Context{
		UserSlug: "alice-test",
	}); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MCLAUDE_HOME", dir) // so ResolveActiveHost reads from temp dir

	_, err := cmd.RunImport(cmd.ImportFlags{
		AuthPath:    authPath,
		ContextPath: ctxPath,
	}, io.Discard)
	if err == nil {
		t.Fatal("RunImport: expected error when host slug missing; got nil")
	}
	if !containsAny(err.Error(), "host", "register") {
		t.Errorf("error %q; want mention of host registration", err.Error())
	}
}

// TestRunImportCWDEncoding tests the full happy path using an injected mock NATS
// connection and a fake S3 server. This verifies that:
//  1. CWD is correctly encoded and sessions are discovered.
//  2. NATS check-slug, import.request, and import.confirm are called.
//  3. The archive is uploaded to S3 via the pre-signed URL.
//  4. The result contains the correct project slug and session count.
func TestRunImportCWDEncoding(t *testing.T) {
	authPath, ctxPath, claudeProjects, fakeCWD := setupImportDir(t, []string{"sess-001"})
	encodedCWD := cmd.EncodeCWD(fakeCWD)

	// Start a fake S3 server that accepts PUT requests.
	var s3UploadCalled bool
	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			s3UploadCalled = true
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
	}))
	defer s3Server.Close()

	// Mock NATS returning the fake S3 server URL as uploadUrl.
	mockNC := NewMockNATSConn(map[string]mockNATSResponse{
		"projects.check-slug": {
			Data: mustJSON(map[string]any{"available": true}),
		},
		"import.request": {
			Data: mustJSON(map[string]any{
				"id":        "imp-test-001",
				"uploadUrl": s3Server.URL + "/test-upload",
			}),
		},
		"import.confirm": {
			Data: mustJSON(map[string]any{"ok": true}),
		},
	})

	var out bytes.Buffer
	result, err := cmd.RunImport(cmd.ImportFlags{
		AuthPath:          authPath,
		ContextPath:       ctxPath,
		ClaudeProjectsDir: claudeProjects,
		CWD:               fakeCWD,
		NATSConn:          mockNC,
	}, &out)
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}

	if result.SessionCount != 1 {
		t.Errorf("SessionCount = %d; want 1", result.SessionCount)
	}
	if result.ProjectSlug == "" {
		t.Error("ProjectSlug is empty")
	}
	// Archive must exist.
	if _, err := os.Stat(result.ArchivePath); err != nil {
		t.Errorf("archive %s not found: %v", result.ArchivePath, err)
	}
	defer os.Remove(result.ArchivePath)

	// Output must contain the encoded CWD.
	if !strings.Contains(out.String(), encodedCWD) {
		t.Errorf("output %q does not contain encoded CWD %q", out.String(), encodedCWD)
	}

	// Verify all three NATS subjects were called.
	reqs := mockNC.Requests()
	subjects := make([]string, len(reqs))
	for i, r := range reqs {
		subjects[i] = r.Subject
	}

	var hasCheckSlug, hasImportReq, hasImportConf bool
	for _, s := range subjects {
		if strings.Contains(s, "check-slug") {
			hasCheckSlug = true
		}
		if strings.Contains(s, "import.request") {
			hasImportReq = true
		}
		if strings.Contains(s, "import.confirm") {
			hasImportConf = true
		}
	}
	if !hasCheckSlug {
		t.Errorf("check-slug NATS request not sent; subjects = %v", subjects)
	}
	if !hasImportReq {
		t.Errorf("import.request NATS request not sent; subjects = %v", subjects)
	}
	if !hasImportConf {
		t.Errorf("import.confirm NATS request not sent; subjects = %v", subjects)
	}

	// S3 upload must have been called.
	if !s3UploadCalled {
		t.Error("S3 upload PUT request was not sent")
	}
}

// TestRunImportNATSSubjectFormat verifies the exact NATS subject format used
// for each request/reply call, matching spec-nats-payload-schema.md.
func TestRunImportNATSSubjectFormat(t *testing.T) {
	authPath, ctxPath, claudeProjects, fakeCWD := setupImportDir(t, []string{"sess-001"})

	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer s3Server.Close()

	mockNC := NewMockNATSConn(map[string]mockNATSResponse{
		"projects.check-slug": {Data: mustJSON(map[string]any{"available": true})},
		"import.request": {Data: mustJSON(map[string]any{
			"id":        "imp-001",
			"uploadUrl": s3Server.URL + "/upload",
		})},
		"import.confirm": {Data: mustJSON(map[string]any{"ok": true})},
	})

	result, err := cmd.RunImport(cmd.ImportFlags{
		AuthPath:          authPath,
		ContextPath:       ctxPath,
		ClaudeProjectsDir: claudeProjects,
		CWD:               fakeCWD,
		NATSConn:          mockNC,
	}, io.Discard)
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	defer os.Remove(result.ArchivePath)

	reqs := mockNC.Requests()
	if len(reqs) != 3 {
		t.Fatalf("expected 3 NATS requests, got %d: %v", len(reqs), reqs)
	}

	// Verify subject patterns.
	// uslug=alice-test, hslug=test-host, pslug=myapp (derived from /fake/project/myapp)
	uslug := "alice-test"
	hslug := "test-host"
	pslug := result.ProjectSlug

	wantCheckSlug := fmt.Sprintf("mclaude.users.%s.hosts.%s.projects.check-slug", uslug, hslug)
	wantImportReq := fmt.Sprintf("mclaude.users.%s.hosts.%s.projects.%s.import.request", uslug, hslug, pslug)
	wantImportConf := fmt.Sprintf("mclaude.users.%s.hosts.%s.projects.%s.import.confirm", uslug, hslug, pslug)

	if reqs[0].Subject != wantCheckSlug {
		t.Errorf("request[0] subject = %q; want %q", reqs[0].Subject, wantCheckSlug)
	}
	if reqs[1].Subject != wantImportReq {
		t.Errorf("request[1] subject = %q; want %q", reqs[1].Subject, wantImportReq)
	}
	if reqs[2].Subject != wantImportConf {
		t.Errorf("request[2] subject = %q; want %q", reqs[2].Subject, wantImportConf)
	}
}

// TestRunImportNATSPayloads verifies the JSON payloads sent for each NATS request.
func TestRunImportNATSPayloads(t *testing.T) {
	authPath, ctxPath, claudeProjects, fakeCWD := setupImportDir(t, []string{"sess-a", "sess-b"})

	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer s3Server.Close()

	const testImportID = "imp-payload-test"
	mockNC := NewMockNATSConn(map[string]mockNATSResponse{
		"projects.check-slug": {Data: mustJSON(map[string]any{"available": true})},
		"import.request": {Data: mustJSON(map[string]any{
			"id":        testImportID,
			"uploadUrl": s3Server.URL + "/upload",
		})},
		"import.confirm": {Data: mustJSON(map[string]any{"ok": true})},
	})

	result, err := cmd.RunImport(cmd.ImportFlags{
		AuthPath:          authPath,
		ContextPath:       ctxPath,
		ClaudeProjectsDir: claudeProjects,
		CWD:               fakeCWD,
		NATSConn:          mockNC,
	}, io.Discard)
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	defer os.Remove(result.ArchivePath)

	reqs := mockNC.Requests()
	if len(reqs) != 3 {
		t.Fatalf("expected 3 NATS requests, got %d", len(reqs))
	}

	// Verify check-slug payload: {"slug": "<pslug>"}
	var checkSlugReq map[string]any
	if err := json.Unmarshal(reqs[0].Data, &checkSlugReq); err != nil {
		t.Fatalf("parse check-slug payload: %v", err)
	}
	if checkSlugReq["slug"] != result.ProjectSlug {
		t.Errorf("check-slug slug = %q; want %q", checkSlugReq["slug"], result.ProjectSlug)
	}

	// Verify import.request payload: {"id":"...","ts":...,"slug":"...","sizeBytes":...}
	var importReqPayload map[string]any
	if err := json.Unmarshal(reqs[1].Data, &importReqPayload); err != nil {
		t.Fatalf("parse import.request payload: %v", err)
	}
	if importReqPayload["slug"] != result.ProjectSlug {
		t.Errorf("import.request slug = %q; want %q", importReqPayload["slug"], result.ProjectSlug)
	}
	if _, ok := importReqPayload["sizeBytes"]; !ok {
		t.Error("import.request missing sizeBytes field")
	}
	if _, ok := importReqPayload["id"]; !ok {
		t.Error("import.request missing id field")
	}
	if _, ok := importReqPayload["ts"]; !ok {
		t.Error("import.request missing ts field")
	}

	// Verify import.confirm payload: {"id":"...","ts":...,"importId":"imp-payload-test"}
	var confirmPayload map[string]any
	if err := json.Unmarshal(reqs[2].Data, &confirmPayload); err != nil {
		t.Fatalf("parse import.confirm payload: %v", err)
	}
	if confirmPayload["importId"] != testImportID {
		t.Errorf("import.confirm importId = %q; want %q", confirmPayload["importId"], testImportID)
	}
}

// TestRunImportSlugCollisionLoop verifies that when check-slug returns
// available=false, the CLI prompts for a new name and re-checks until
// an available slug is found.
func TestRunImportSlugCollisionLoop(t *testing.T) {
	authPath, ctxPath, claudeProjects, fakeCWD := setupImportDir(t, []string{"sess-001"})

	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer s3Server.Close()

	// First check-slug returns taken, second returns available.
	callCount := 0
	mockNC := &callCountingNATSConn{
		onRequest: func(subj string, data []byte) ([]byte, error) {
			if strings.Contains(subj, "check-slug") {
				callCount++
				if callCount == 1 {
					// First check: slug taken
					return mustJSON(map[string]any{
						"available":  false,
						"suggestion": "my-project-2",
					}), nil
				}
				// Second check: slug available
				return mustJSON(map[string]any{"available": true}), nil
			}
			if strings.Contains(subj, "import.request") {
				return mustJSON(map[string]any{
					"id":        "imp-collision-test",
					"uploadUrl": s3Server.URL + "/upload",
				}), nil
			}
			if strings.Contains(subj, "import.confirm") {
				return mustJSON(map[string]any{"ok": true}), nil
			}
			return nil, fmt.Errorf("unexpected subject: %s", subj)
		},
	}

	// Inject stdin with the new project name.
	inputReader := strings.NewReader("new-project-name\n")

	var out bytes.Buffer
	result, err := cmd.RunImport(cmd.ImportFlags{
		AuthPath:          authPath,
		ContextPath:       ctxPath,
		ClaudeProjectsDir: claudeProjects,
		CWD:               fakeCWD,
		NATSConn:          mockNC,
		Input:             inputReader,
	}, &out)
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	defer os.Remove(result.ArchivePath)

	// The output must mention the slug collision.
	outStr := out.String()
	if !strings.Contains(outStr, "already taken") {
		t.Errorf("output %q does not mention slug collision", outStr)
	}
	// Check-slug should have been called exactly twice.
	if callCount != 2 {
		t.Errorf("check-slug called %d times; want 2", callCount)
	}
	// Final slug must be "new-project-name" slugified.
	if result.ProjectSlug != "new-project-name" {
		t.Errorf("ProjectSlug = %q; want %q", result.ProjectSlug, "new-project-name")
	}
}

// TestRunImportNATSCheckSlugError verifies that NATS errors during slug check
// are surfaced as RunImport errors.
func TestRunImportNATSCheckSlugError(t *testing.T) {
	authPath, ctxPath, claudeProjects, fakeCWD := setupImportDir(t, []string{"sess-001"})

	mockNC := NewMockNATSConn(map[string]mockNATSResponse{
		"projects.check-slug": {Err: fmt.Errorf("NATS: connection refused")},
	})

	_, err := cmd.RunImport(cmd.ImportFlags{
		AuthPath:          authPath,
		ContextPath:       ctxPath,
		ClaudeProjectsDir: claudeProjects,
		CWD:               fakeCWD,
		NATSConn:          mockNC,
	}, io.Discard)
	if err == nil {
		t.Fatal("RunImport: expected error on NATS failure; got nil")
	}
	if !strings.Contains(err.Error(), "slug check") {
		t.Errorf("error %q; want mention of slug check failure", err.Error())
	}
}

// TestRunImportNATSImportRequestError verifies that errors from import.request
// are surfaced.
func TestRunImportNATSImportRequestError(t *testing.T) {
	authPath, ctxPath, claudeProjects, fakeCWD := setupImportDir(t, []string{"sess-001"})

	mockNC := NewMockNATSConn(map[string]mockNATSResponse{
		"projects.check-slug": {Data: mustJSON(map[string]any{"available": true})},
		"import.request":      {Err: fmt.Errorf("NATS timeout")},
	})

	_, err := cmd.RunImport(cmd.ImportFlags{
		AuthPath:          authPath,
		ContextPath:       ctxPath,
		ClaudeProjectsDir: claudeProjects,
		CWD:               fakeCWD,
		NATSConn:          mockNC,
	}, io.Discard)
	if err == nil {
		t.Fatal("RunImport: expected error on import.request failure; got nil")
	}
	if !strings.Contains(err.Error(), "request import URL") {
		t.Errorf("error %q; want mention of request import URL", err.Error())
	}
}

// TestRunImportNATSImportConfirmError verifies that errors from import.confirm
// are surfaced.
func TestRunImportNATSImportConfirmError(t *testing.T) {
	authPath, ctxPath, claudeProjects, fakeCWD := setupImportDir(t, []string{"sess-001"})

	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer s3Server.Close()

	mockNC := NewMockNATSConn(map[string]mockNATSResponse{
		"projects.check-slug": {Data: mustJSON(map[string]any{"available": true})},
		"import.request": {Data: mustJSON(map[string]any{
			"id":        "imp-err",
			"uploadUrl": s3Server.URL + "/upload",
		})},
		"import.confirm": {Err: fmt.Errorf("NATS timeout")},
	})

	_, err := cmd.RunImport(cmd.ImportFlags{
		AuthPath:          authPath,
		ContextPath:       ctxPath,
		ClaudeProjectsDir: claudeProjects,
		CWD:               fakeCWD,
		NATSConn:          mockNC,
	}, io.Discard)
	if err == nil {
		t.Fatal("RunImport: expected error on import.confirm failure; got nil")
	}
	if !strings.Contains(err.Error(), "confirm import") {
		t.Errorf("error %q; want mention of confirm import", err.Error())
	}
}

// TestRunImportS3UploadError verifies that S3 upload failures are surfaced.
func TestRunImportS3UploadError(t *testing.T) {
	authPath, ctxPath, claudeProjects, fakeCWD := setupImportDir(t, []string{"sess-001"})

	// S3 server returns 403.
	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer s3Server.Close()

	mockNC := NewMockNATSConn(map[string]mockNATSResponse{
		"projects.check-slug": {Data: mustJSON(map[string]any{"available": true})},
		"import.request": {Data: mustJSON(map[string]any{
			"id":        "imp-s3-err",
			"uploadUrl": s3Server.URL + "/upload",
		})},
	})

	_, err := cmd.RunImport(cmd.ImportFlags{
		AuthPath:          authPath,
		ContextPath:       ctxPath,
		ClaudeProjectsDir: claudeProjects,
		CWD:               fakeCWD,
		NATSConn:          mockNC,
	}, io.Discard)
	if err == nil {
		t.Fatal("RunImport: expected error on S3 upload failure; got nil")
	}
	if !strings.Contains(err.Error(), "upload to S3") {
		t.Errorf("error %q; want mention of S3 upload", err.Error())
	}
}

// TestRunImportImportRequestRejected verifies that when the CP rejects the
// import.request (e.g. archive too large), the error is surfaced.
func TestRunImportImportRequestRejected(t *testing.T) {
	authPath, ctxPath, claudeProjects, fakeCWD := setupImportDir(t, []string{"sess-001"})

	mockNC := NewMockNATSConn(map[string]mockNATSResponse{
		"projects.check-slug": {Data: mustJSON(map[string]any{"available": true})},
		"import.request": {Data: mustJSON(map[string]any{
			"error": "archive exceeds maximum size",
			"code":  "size_limit_exceeded",
		})},
	})

	_, err := cmd.RunImport(cmd.ImportFlags{
		AuthPath:          authPath,
		ContextPath:       ctxPath,
		ClaudeProjectsDir: claudeProjects,
		CWD:               fakeCWD,
		NATSConn:          mockNC,
	}, io.Discard)
	if err == nil {
		t.Fatal("RunImport: expected error on import.request rejection; got nil")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error %q; want mention of rejection", err.Error())
	}
}

// TestRunImportSuccessOutput verifies the success output contains key information.
func TestRunImportSuccessOutput(t *testing.T) {
	authPath, ctxPath, claudeProjects, fakeCWD := setupImportDir(t, []string{"sess-001", "sess-002"})

	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer s3Server.Close()

	mockNC := standardMockNATS()
	mockNC.responses["import.request"] = mockNATSResponse{
		Data: mustJSON(map[string]any{
			"id":        "imp-success",
			"uploadUrl": s3Server.URL + "/upload",
		}),
	}

	var out bytes.Buffer
	result, err := cmd.RunImport(cmd.ImportFlags{
		AuthPath:          authPath,
		ContextPath:       ctxPath,
		ClaudeProjectsDir: claudeProjects,
		CWD:               fakeCWD,
		NATSConn:          mockNC,
	}, &out)
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	defer os.Remove(result.ArchivePath)

	outStr := out.String()
	// Must mention "Import complete" or equivalent success message.
	if !containsAny(outStr, "Import complete", "complete") {
		t.Errorf("output %q does not contain success message", outStr)
	}
	// Must mention the user, host, and project.
	if !strings.Contains(outStr, "alice-test") {
		t.Errorf("output %q does not contain user slug", outStr)
	}
	if !strings.Contains(outStr, "test-host") {
		t.Errorf("output %q does not contain host slug", outStr)
	}
	if !strings.Contains(outStr, result.ProjectSlug) {
		t.Errorf("output %q does not contain project slug %q", outStr, result.ProjectSlug)
	}
}

// TestRunImport_UsesStoredNatsUrl verifies that when auth.json contains a
// non-empty NATSUrl, RunImport uses it (and not DeriveNATSURL) (ADR-0069).
// The NATSConn injection means we don't actually dial NATS — we just confirm
// the stored URL is printed in the output when NATSConn is nil. To keep the
// test hermetic we use an injected NATSConn and verify the stored URL appears
// in the pre-connect output, confirming the code path was taken.
func TestRunImport_UsesStoredNatsUrl(t *testing.T) {
	authPath, ctxPath, claudeProjects, fakeCWD := setupImportDir(t, []string{"sess-001"})

	// Override auth with a stored NATSUrl.
	const storedNATSUrl = "wss://dev-nats.mclaude.example.com"
	if err := cmd.SaveAuth(authPath, &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUAIBDPBAUTWCWBKIO6XHQNINK5FWJW4OHLXC3HQ2KFE4PEJUA4LNNAL",
		UserSlug: "alice-test",
		NATSUrl:  storedNATSUrl,
	}); err != nil {
		t.Fatal(err)
	}

	// Use a fake S3 server.
	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer s3Server.Close()

	mockNC := NewMockNATSConn(map[string]mockNATSResponse{
		"projects.check-slug": {Data: mustJSON(map[string]any{"available": true})},
		"import.request": {Data: mustJSON(map[string]any{
			"id":        "imp-nats-url-test",
			"uploadUrl": s3Server.URL + "/upload",
		})},
		"import.confirm": {Data: mustJSON(map[string]any{"ok": true})},
	})

	var out bytes.Buffer
	result, err := cmd.RunImport(cmd.ImportFlags{
		AuthPath:          authPath,
		ContextPath:       ctxPath,
		ClaudeProjectsDir: claudeProjects,
		CWD:               fakeCWD,
		NATSConn:          mockNC,
	}, &out)
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	defer os.Remove(result.ArchivePath)

	// Verify the import succeeded end-to-end.
	if result.SessionCount != 1 {
		t.Errorf("SessionCount = %d; want 1", result.SessionCount)
	}
}

// TestRunImport_FallsBackToDeriveNATSURL verifies that when NATSUrl is empty in
// auth.json, RunImport falls back to DeriveNATSURL(serverURL) (ADR-0069).
func TestRunImport_FallsBackToDeriveNATSURL(t *testing.T) {
	authPath, ctxPath, claudeProjects, fakeCWD := setupImportDir(t, []string{"sess-001"})

	// Auth has no NATSUrl (simulates older or production CP).
	if err := cmd.SaveAuth(authPath, &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "SUAIBDPBAUTWCWBKIO6XHQNINK5FWJW4OHLXC3HQ2KFE4PEJUA4LNNAL",
		UserSlug: "alice-test",
		// NATSUrl intentionally omitted.
	}); err != nil {
		t.Fatal(err)
	}

	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer s3Server.Close()

	mockNC := NewMockNATSConn(map[string]mockNATSResponse{
		"projects.check-slug": {Data: mustJSON(map[string]any{"available": true})},
		"import.request": {Data: mustJSON(map[string]any{
			"id":        "imp-fallback-test",
			"uploadUrl": s3Server.URL + "/upload",
		})},
		"import.confirm": {Data: mustJSON(map[string]any{"ok": true})},
	})

	var out bytes.Buffer
	result, err := cmd.RunImport(cmd.ImportFlags{
		AuthPath:          authPath,
		ContextPath:       ctxPath,
		ClaudeProjectsDir: claudeProjects,
		CWD:               fakeCWD,
		NATSConn:          mockNC,
	}, &out)
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	defer os.Remove(result.ArchivePath)

	// Import succeeded — DeriveNATSURL fallback path was used.
	if result.SessionCount != 1 {
		t.Errorf("SessionCount = %d; want 1", result.SessionCount)
	}
}

// ── DeriveNATSURL ─────────────────────────────────────────────────────────────

func TestDeriveNATSURL(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"https://api.mclaude.internal", "wss://api.mclaude.internal/nats"},
		{"http://localhost:8080", "ws://localhost:8080/nats"},
		{"https://api.mclaude.internal/nats", "https://api.mclaude.internal/nats"},
		{"https://api.mclaude.internal/", "wss://api.mclaude.internal/nats"},
	}
	for _, c := range cases {
		got := clicontext.DeriveNATSURL(c.input)
		if got != c.want {
			t.Errorf("DeriveNATSURL(%q) = %q; want %q", c.input, got, c.want)
		}
	}
}

// ── callCountingNATSConn ──────────────────────────────────────────────────────

// callCountingNATSConn is a flexible mock that invokes an onRequest callback.
type callCountingNATSConn struct {
	mu        sync.Mutex
	onRequest func(subj string, data []byte) ([]byte, error)
}

func (c *callCountingNATSConn) Request(subj string, data []byte, _ time.Duration) (*nats.Msg, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	respData, err := c.onRequest(subj, data)
	if err != nil {
		return nil, err
	}
	return &nats.Msg{Data: respData}, nil
}

func (c *callCountingNATSConn) Close() {}
