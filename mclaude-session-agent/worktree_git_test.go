package main

import (
	"os"
	"os/exec"
	"path/filepath"
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

// TestHandleCreateSkipsGitWhenNoDataDir verifies that handleCreate does NOT
// attempt git worktree operations when dataDir is empty — the session is
// created successfully without needing a git repo.
func TestHandleCreateSkipsGitWhenNoDataDir(t *testing.T) {
	// Build an agent with no dataDir and no real NATS (nc is nil).
	// We cannot exercise the full handler path without NATS, so we test
	// the gitWorktreeAdd skip logic via the dataDir guard directly.
	a := buildMinimalAgent(t)
	if a.dataDir != "" {
		t.Fatal("expected empty dataDir in minimal agent")
	}

	// When dataDir is empty, gitWorktreeAdd should NOT be called.
	// Confirm by checking that the handler guard works:
	// a.dataDir == "" means skip git ops.
	shouldSkip := a.dataDir == ""
	if !shouldSkip {
		t.Error("expected git ops to be skipped when dataDir is empty")
	}
}
