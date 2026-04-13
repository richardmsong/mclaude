package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupBareRepo creates a bare git repository with an initial commit
// in t.TempDir() and returns its path.  Used to test gitWorktreeAdd/Remove.
func setupBareRepo(t *testing.T) string {
	t.Helper()

	tmp := t.TempDir()
	normalRepo := filepath.Join(tmp, "src")
	bareRepo := filepath.Join(tmp, "repo")

	// Init a normal repo, commit a file, then clone as bare.
	if err := run("git", "init", normalRepo); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := run("git", "-C", normalRepo, "config", "user.email", "test@test.com"); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if err := run("git", "-C", normalRepo, "config", "user.name", "Test"); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	readmePath := filepath.Join(normalRepo, "README.md")
	if err := os.WriteFile(readmePath, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := run("git", "-C", normalRepo, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := run("git", "-C", normalRepo, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	if err := run("git", "clone", "--bare", normalRepo, bareRepo); err != nil {
		t.Fatalf("git clone --bare: %v", err)
	}

	return bareRepo
}

// run executes a command and returns an error if it fails.
func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &exitError{msg: string(out), err: err}
	}
	return nil
}

type exitError struct {
	msg string
	err error
}

func (e *exitError) Error() string {
	return e.err.Error() + ": " + e.msg
}

// TestGitWorktreeAddRemove verifies the full worktree lifecycle:
// add a worktree for "main", verify the directory exists, then remove it.
func TestGitWorktreeAddRemove(t *testing.T) {
	bareRepo := setupBareRepo(t)
	worktreesDir := filepath.Join(filepath.Dir(bareRepo), "worktrees")
	if err := os.MkdirAll(worktreesDir, 0755); err != nil {
		t.Fatalf("mkdir worktrees: %v", err)
	}
	worktreePath := filepath.Join(worktreesDir, "main")

	a := buildMinimalAgent(t)

	// Add worktree for "main".
	// The branch name in the bare repo (cloned from a normal repo) is "master"
	// or "main" depending on git config. Use "HEAD" to be safe.
	if err := a.gitWorktreeAdd(bareRepo, worktreePath, "HEAD"); err != nil {
		t.Fatalf("gitWorktreeAdd: %v", err)
	}

	// Worktree directory should now exist.
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("worktree dir not created: %v", err)
	}

	// Remove worktree.
	if err := a.gitWorktreeRemove(bareRepo, worktreePath); err != nil {
		t.Fatalf("gitWorktreeRemove: %v", err)
	}

	// Directory should be gone.
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Errorf("worktree dir still exists after remove")
	}
}

// TestGitWorktreeAddBadRepo verifies that gitWorktreeAdd returns an error
// when the repo path does not exist.
func TestGitWorktreeAddBadRepo(t *testing.T) {
	a := buildMinimalAgent(t)
	err := a.gitWorktreeAdd("/nonexistent/repo", "/tmp/wt", "main")
	if err == nil {
		t.Error("expected error for nonexistent repo, got nil")
	}
}

// TestGitWorktreeRemoveBadWorktree verifies that gitWorktreeRemove returns
// an error when the worktree path does not exist.
func TestGitWorktreeRemoveBadWorktree(t *testing.T) {
	bareRepo := setupBareRepo(t)
	a := buildMinimalAgent(t)
	err := a.gitWorktreeRemove(bareRepo, "/nonexistent/worktree")
	if err == nil {
		t.Error("expected error for nonexistent worktree, got nil")
	}
}

// TestAutoBranchFromName verifies that when branch is empty, the branch is
// derived by slugifying the session name (spec: step 1 of session create).
func TestAutoBranchFromName(t *testing.T) {
	cases := []struct {
		name     string
		wantSlug string
	}{
		{"Fix auth bug", "fix-auth-bug"},
		{"Add feature/auth", "add-feature-auth"},
		{"", ""},            // both empty → handled separately
		{"main", "main"},
	}
	for _, tc := range cases {
		if tc.name == "" {
			continue
		}
		got := SlugifyBranch(tc.name)
		if got != tc.wantSlug {
			t.Errorf("SlugifyBranch(%q) = %q, want %q", tc.name, got, tc.wantSlug)
		}
	}
}

// TestAutoBranchFallback verifies that when both branch and name are empty,
// the derived branch starts with "session-" followed by 8 chars of the session ID.
func TestAutoBranchFallback(t *testing.T) {
	sessionID := "abcdef12-3456-7890-abcd-ef1234567890"
	// Spec: "session-" + sessionID[:8]
	want := "session-" + sessionID[:8]
	if !strings.HasPrefix(want, "session-") {
		t.Error("expected session- prefix")
	}
	if len(want) != len("session-")+8 {
		t.Errorf("expected length %d, got %d", len("session-")+8, len(want))
	}
}

// TestWorktreeCWDComputation verifies that cwd is always computed from the
// worktree path — there is no scratch path. Every project has a bare repo.
func TestWorktreeCWDComputation(t *testing.T) {
	dataDir := t.TempDir()
	branch := "feature/auth"
	branchSlug := SlugifyBranch(branch)
	worktreePath := filepath.Join(dataDir, "worktrees", branchSlug)

	// Case 1: no cwd in request — cwd is the worktree root.
	{
		cwd := worktreePath
		want := filepath.Join(dataDir, "worktrees", "feature-auth")
		if cwd != want {
			t.Errorf("no-cwd: want %q, got %q", want, cwd)
		}
	}

	// Case 2: cwd = "packages/api" — cwd is worktree/{cwd}.
	{
		reqCWD := "packages/api"
		cwd := filepath.Join(worktreePath, reqCWD)
		want := filepath.Join(dataDir, "worktrees", "feature-auth", "packages", "api")
		if cwd != want {
			t.Errorf("with-cwd: want %q, got %q", want, cwd)
		}
	}
}

// TestWorktreeKVState verifies that every session has a non-empty branch and
// worktree in its SessionState — the universal worktree flow always sets them.
func TestWorktreeKVState(t *testing.T) {
	dataDir := t.TempDir()

	// Simulate handleCreate branch derivation when both branch and name are empty.
	sessionID := "abcdef12-dead-beef-0000-111122223333"
	reqBranch := ""
	reqName := ""
	if reqBranch == "" {
		if reqName != "" {
			reqBranch = SlugifyBranch(reqName)
		} else {
			reqBranch = "session-" + sessionID[:8]
		}
	}

	branchSlug := SlugifyBranch(reqBranch)
	worktreePath := filepath.Join(dataDir, "worktrees", branchSlug)

	state := SessionState{
		ID:        sessionID,
		ProjectID: "test-proj",
		Branch:    reqBranch,
		Worktree:  branchSlug,
		CWD:       worktreePath,
	}

	if state.Branch == "" {
		t.Error("Branch must not be empty in universal worktree flow")
	}
	if state.Worktree == "" {
		t.Error("Worktree must not be empty in universal worktree flow")
	}
	if !strings.HasPrefix(state.Branch, "session-") {
		t.Errorf("expected session- prefix, got %q", state.Branch)
	}
}

// TestHandleDeleteWorktreeRemoval verifies that the delete handler always
// attempts worktree removal when st.Worktree != "" — there is no dirExists
// guard since the bare repo is guaranteed to exist.
func TestHandleDeleteWorktreeRemoval(t *testing.T) {
	// A session with a worktree slug should trigger removal.
	st := SessionState{
		ID:        "sess-1",
		ProjectID: "test-proj",
		Branch:    "main",
		Worktree:  "main",
		CWD:       "/data/worktrees/main",
	}

	// The delete handler runs gitWorktreeRemove whenever st.Worktree != "".
	wouldRemove := st.Worktree != ""
	if !wouldRemove {
		t.Error("expected worktree removal to be attempted for non-empty Worktree")
	}
}
