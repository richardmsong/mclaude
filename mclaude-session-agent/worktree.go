package main

import (
	"regexp"
	"strings"
)

var nonAlphanumDash = regexp.MustCompile(`[^a-z0-9-]+`)

// SlugifyBranch converts a git branch name to a URL/path-safe slug.
// Examples:
//
//	"feature/auth"     → "feature-auth"
//	"fix/my bug here"  → "fix-my-bug-here"
//	"MAIN"             → "main"
func SlugifyBranch(branch string) string {
	s := strings.ToLower(branch)
	s = strings.ReplaceAll(s, "/", "-")
	s = nonAlphanumDash.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "main"
	}
	return s
}

// worktreeExists checks whether a worktree slug is already in use by any
// session in the given project.  sessions is the map of sessionId→SessionState
// loaded from NATS KV.
func worktreeExists(sessions map[string]*SessionState, projectID, slug string) bool {
	for _, s := range sessions {
		if s.ProjectID == projectID && s.Worktree == slug {
			return true
		}
	}
	return false
}
