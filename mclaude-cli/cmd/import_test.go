package cmd_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mclaude-cli/cmd"
	clicontext "mclaude-cli/context"
)

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

func TestRunImportCWDEncoding(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()

	// Write auth credentials.
	authPath := filepath.Join(dir, "auth.json")
	if err := cmd.SaveAuth(authPath, &cmd.AuthCredentials{
		JWT:      "test-jwt",
		NKeySeed: "test-seed",
		UserSlug: "alice-test",
	}); err != nil {
		t.Fatal(err)
	}

	// Write context with host slug.
	ctxPath := filepath.Join(dir, "context.json")
	if err := clicontext.Save(ctxPath, &clicontext.Context{
		UserSlug: "alice-test",
		HostSlug: "test-host",
	}); err != nil {
		t.Fatal(err)
	}

	// Create fake CWD /fake/project/myapp.
	fakeCWD := "/fake/project/myapp"
	encodedCWD := cmd.EncodeCWD(fakeCWD) // "fake-project-myapp"

	// Create the Claude projects directory with session data.
	claudeProjects := filepath.Join(homeDir, "claude", "projects")
	projectDir := filepath.Join(claudeProjects, encodedCWD)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "sess-001.jsonl"), []byte(`{"type":"system"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	result, err := cmd.RunImport(cmd.ImportFlags{
		AuthPath:          authPath,
		ContextPath:       ctxPath,
		ClaudeProjectsDir: claudeProjects,
		CWD:               fakeCWD,
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

	// Output must contain the encoded CWD.
	if !strings.Contains(out.String(), encodedCWD) {
		t.Errorf("output %q does not contain encoded CWD %q", out.String(), encodedCWD)
	}

	// Clean up archive.
	os.Remove(result.ArchivePath)
}
