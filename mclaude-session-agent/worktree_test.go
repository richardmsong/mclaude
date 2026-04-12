package main

import "testing"

func TestSlugifyBranch(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"main", "main"},
		{"feature/auth", "feature-auth"},
		{"fix/my bug here", "fix-my-bug-here"},
		{"MAIN", "main"},
		{"Feature/Auth-Token", "feature-auth-token"},
		{"release/v1.2.3", "release-v1-2-3"},
		{"a/b/c/d", "a-b-c-d"},
		{"---leading-trailing---", "leading-trailing"},
		{"double--hyphen", "double--hyphen"},
		{"", "main"},
		{"!!!invalid!!!", "invalid"},
		{"!!!!!!!", "main"}, // all punctuation → empty → fallback
	}

	for _, tc := range cases {
		got := SlugifyBranch(tc.input)
		if got != tc.want {
			t.Errorf("SlugifyBranch(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestWorktreeExists(t *testing.T) {
	sessions := map[string]*SessionState{
		"s1": {ID: "s1", ProjectID: "proj-1", Worktree: "feature-auth"},
		"s2": {ID: "s2", ProjectID: "proj-1", Worktree: "main"},
		"s3": {ID: "s3", ProjectID: "proj-2", Worktree: "feature-auth"},
	}

	t.Run("exists same project", func(t *testing.T) {
		if !worktreeExists(sessions, "proj-1", "feature-auth") {
			t.Error("expected worktree to exist in proj-1")
		}
	})

	t.Run("exists another project does not count", func(t *testing.T) {
		// "feature-auth" is in proj-2 too, but not in proj-3
		if worktreeExists(sessions, "proj-3", "feature-auth") {
			t.Error("expected no worktree in proj-3")
		}
	})

	t.Run("does not exist", func(t *testing.T) {
		if worktreeExists(sessions, "proj-1", "nonexistent") {
			t.Error("expected worktree not to exist")
		}
	})

	t.Run("cross-project isolation", func(t *testing.T) {
		// proj-1 has "feature-auth"; proj-2 also has "feature-auth"
		// They are independent.
		if !worktreeExists(sessions, "proj-2", "feature-auth") {
			t.Error("expected worktree to exist in proj-2")
		}
	})

	t.Run("empty sessions", func(t *testing.T) {
		if worktreeExists(nil, "proj-1", "main") {
			t.Error("expected no worktree in empty map")
		}
	})
}
