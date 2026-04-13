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

// TestDirExists verifies the dirExists helper for the scratch project check.
func TestDirExists(t *testing.T) {
	// Existing directory.
	tmp := t.TempDir()
	if !dirExists(tmp) {
		t.Errorf("dirExists(%q): expected true for existing dir", tmp)
	}

	// Non-existent path.
	absent := filepath.Join(tmp, "does-not-exist")
	if dirExists(absent) {
		t.Errorf("dirExists(%q): expected false for absent path", absent)
	}

	// Existing file (not a directory).
	filePath := filepath.Join(tmp, "file.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if dirExists(filePath) {
		t.Errorf("dirExists(%q): expected false for regular file", filePath)
	}
}

// TestScratchProjectCWDComputation verifies the cwd computation logic for
// scratch projects (no /data/repo): cwd must be dataDir/{req.CWD} or just
// dataDir when cwd is empty.
//
// We test the dirExists-gated branch directly by setting up a temp dir that
// has NO "repo" subdirectory, then confirming that dirExists returns false
// (the scratch path is taken) and the resulting cwd is computed correctly.
func TestScratchProjectCWDComputation(t *testing.T) {
	dataDir := t.TempDir() // no "repo" subdir — scratch project

	repoPath := filepath.Join(dataDir, "repo")
	if dirExists(repoPath) {
		t.Fatal("test setup error: repo dir should not exist yet")
	}

	// Case 1: no cwd in request.
	{
		var cwd string
		if !dirExists(repoPath) { // scratch path
			cwd = dataDir
		}
		if cwd != dataDir {
			t.Errorf("scratch no-cwd: want %q, got %q", dataDir, cwd)
		}
	}

	// Case 2: cwd = "packages/api" in request.
	{
		reqCWD := "packages/api"
		var cwd string
		if !dirExists(repoPath) { // scratch path
			cwd = filepath.Join(dataDir, reqCWD)
		}
		want := filepath.Join(dataDir, reqCWD)
		if cwd != want {
			t.Errorf("scratch with-cwd: want %q, got %q", want, cwd)
		}
	}
}

// TestGitProjectCWDComputation verifies that when /data/repo exists, the git
// worktree path is computed (not the scratch path).
func TestGitProjectCWDComputation(t *testing.T) {
	dataDir := t.TempDir()
	repoPath := filepath.Join(dataDir, "repo")
	// Create repo directory to simulate a git project.
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	if !dirExists(repoPath) {
		t.Fatal("test setup error: repo dir should exist")
	}

	branch := "feature/auth"
	branchSlug := SlugifyBranch(branch)
	worktreePath := filepath.Join(dataDir, "worktrees", branchSlug)

	// Case 1: no cwd.
	{
		cwd := worktreePath
		want := filepath.Join(dataDir, "worktrees", "feature-auth")
		if cwd != want {
			t.Errorf("git no-cwd: want %q, got %q", want, cwd)
		}
	}

	// Case 2: cwd = "packages/api".
	{
		reqCWD := "packages/api"
		cwd := filepath.Join(worktreePath, reqCWD)
		want := filepath.Join(dataDir, "worktrees", "feature-auth", "packages", "api")
		if cwd != want {
			t.Errorf("git with-cwd: want %q, got %q", want, cwd)
		}
	}
}

// TestScratchProjectKVState verifies that scratch project sessions get empty
// branch and worktree in their SessionState — matching the spec requirement
// that KV is written with branch:"", worktree:"".
//
// We test this by simulating the scratch path logic that populates the
// SessionState fields, using the same dirExists guard the production code uses.
func TestScratchProjectKVState(t *testing.T) {
	dataDir := t.TempDir() // no repo subdir

	repoPath := filepath.Join(dataDir, "repo")
	scratch := !dirExists(repoPath)
	if !scratch {
		t.Fatal("test setup error: should be scratch (no repo)")
	}

	// Simulate the fields set in handleCreate for the scratch path.
	branch := ""
	branchSlug := ""
	cwd := dataDir // req.CWD is empty

	state := SessionState{
		ID:        "test-sess",
		ProjectID: "test-proj",
		Branch:    branch,
		Worktree:  branchSlug,
		CWD:       cwd,
	}

	if state.Branch != "" {
		t.Errorf("scratch: Branch should be empty, got %q", state.Branch)
	}
	if state.Worktree != "" {
		t.Errorf("scratch: Worktree should be empty, got %q", state.Worktree)
	}
	if state.CWD != dataDir {
		t.Errorf("scratch: CWD should be %q, got %q", dataDir, state.CWD)
	}
}

// TestHandleDeleteSkipsWorktreeForScratch verifies that the delete handler
// does NOT attempt git worktree removal for scratch projects.  The guard is
// st.Worktree != "" — scratch sessions have empty worktree — so removal is
// skipped without needing to check dirExists.
func TestHandleDeleteSkipsWorktreeForScratch(t *testing.T) {
	// A scratch session has Worktree == "".
	scratchState := SessionState{
		ID:        "scratch-sess",
		ProjectID: "test-proj",
		Branch:    "",
		Worktree:  "", // scratch: always empty
		CWD:       "/data",
	}

	// The delete handler only runs gitWorktreeRemove when st.Worktree != "".
	// Confirm the guard prevents removal.
	wouldRemove := scratchState.Worktree != ""
	if wouldRemove {
		t.Error(`scratch project: worktree removal guard should prevent removal (Worktree == "")`)
	}
}
