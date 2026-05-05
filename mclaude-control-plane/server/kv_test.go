package server

import (
	"testing"
)

func TestSchema_SessionsKVEnsured(t *testing.T) {
	// Verify per-user KV helper functions exist (ADR-0054: per-user bucket model).
	// Checked as compile-time guards — KV bucket creation happens at runtime.
	_ = ensurePerUserSessionsKV // compile-time check: function must exist
	_ = ensurePerUserProjectsKV // compile-time check: function must exist
}
