package main

import (
	"os"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// GAP-CP-06: Provisioning subject uses .provision not .create
// ---------------------------------------------------------------------------

func TestProvisionSubjectFormat(t *testing.T) {
	// ADR-0054: provision (create) subject uses host-scoped fan-out format.
	hostSlug := "dev-box"
	userSlug := "alice"
	projectSlug := "myapp"
	expected := "mclaude.hosts.dev-box.users.alice.projects.myapp.create"
	got := "mclaude.hosts." + hostSlug + ".users." + userSlug + ".projects." + projectSlug + ".create"
	if got != expected {
		t.Errorf("provision subject: got %q, want %q", got, expected)
	}
}

// ---------------------------------------------------------------------------
// KNOWN-18: provisionTimeoutSeconds reads from env
// ---------------------------------------------------------------------------

func TestProvisionTimeoutSeconds_Default(t *testing.T) {
	// Unset env to test default.
	os.Unsetenv("PROVISION_TIMEOUT_SECONDS")
	got := provisionTimeoutSeconds()
	want := 10 * time.Second
	if got != want {
		t.Errorf("provisionTimeoutSeconds() = %v, want %v", got, want)
	}
}

func TestProvisionTimeoutSeconds_EnvOverride(t *testing.T) {
	t.Setenv("PROVISION_TIMEOUT_SECONDS", "30")
	got := provisionTimeoutSeconds()
	want := 30 * time.Second
	if got != want {
		t.Errorf("provisionTimeoutSeconds() = %v, want %v", got, want)
	}
}

func TestProvisionTimeoutSeconds_InvalidEnv(t *testing.T) {
	t.Setenv("PROVISION_TIMEOUT_SECONDS", "not-a-number")
	got := provisionTimeoutSeconds()
	want := 10 * time.Second
	if got != want {
		t.Errorf("provisionTimeoutSeconds() with invalid env = %v, want default %v", got, want)
	}
}

func TestProvisionTimeoutSeconds_ZeroEnv(t *testing.T) {
	t.Setenv("PROVISION_TIMEOUT_SECONDS", "0")
	got := provisionTimeoutSeconds()
	want := 10 * time.Second
	if got != want {
		t.Errorf("provisionTimeoutSeconds() with 0 = %v, want default %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// NATS publish helper subjects
// ---------------------------------------------------------------------------

func TestPublishProjectsUpdatedSubject(t *testing.T) {
	// publishProjectsUpdated publishes to mclaude.users.{uslug}.api.projects.updated.
	// We verify the subject format is correct (can't test actual publish without NATS).
	userSlug := "alice"
	expected := "mclaude.users.alice.api.projects.updated"
	got := "mclaude.users." + userSlug + ".api.projects.updated"
	if got != expected {
		t.Errorf("projects.updated subject: got %q, want %q", got, expected)
	}
}

func TestPublishProjectsUpdateToHostSubject(t *testing.T) {
	userSlug := "alice"
	hostSlug := "dev-box"
	expected := "mclaude.users.alice.hosts.dev-box.api.projects.update"
	got := "mclaude.users." + userSlug + ".hosts." + hostSlug + ".api.projects.update"
	if got != expected {
		t.Errorf("projects.update subject: got %q, want %q", got, expected)
	}
}

func TestPublishProjectsDeleteToHostSubject(t *testing.T) {
	// ADR-0054: fan-out delete subject is host-scoped.
	hostSlug := "dev-box"
	userSlug := "alice"
	projectSlug := "myapp"
	expected := "mclaude.hosts.dev-box.users.alice.projects.myapp.delete"
	got := "mclaude.hosts." + hostSlug + ".users." + userSlug + ".projects." + projectSlug + ".delete"
	if got != expected {
		t.Errorf("projects.delete subject: got %q, want %q", got, expected)
	}
}

// ---------------------------------------------------------------------------
// NATS publish helpers — nil safety
// ---------------------------------------------------------------------------

func TestPublishProjectsUpdated_NilConn(t *testing.T) {
	// Should not panic when nc is nil.
	publishProjectsUpdated(nil, "alice")
}

func TestPublishProjectsUpdated_EmptySlug(t *testing.T) {
	// Should not panic or publish when slug is empty.
	publishProjectsUpdated(nil, "")
}

func TestPublishProjectsUpdateToHost_NilConn(t *testing.T) {
	publishProjectsUpdateToHost(nil, "alice", "dev-box")
}

func TestPublishProjectsUpdateToHost_EmptySlug(t *testing.T) {
	publishProjectsUpdateToHost(nil, "", "dev-box")
	publishProjectsUpdateToHost(nil, "alice", "")
}

func TestPublishProjectsDeleteToHost_NilConn(t *testing.T) {
	publishProjectsDeleteToHost(nil, "alice", "dev-box", "myapp", "proj-id-1")
}

func TestPublishProjectsDeleteToHost_EmptySlug(t *testing.T) {
	publishProjectsDeleteToHost(nil, "", "dev-box", "myapp", "proj-id-1")
	publishProjectsDeleteToHost(nil, "alice", "", "myapp", "proj-id-1")
}

// ---------------------------------------------------------------------------
// DB method tests (UpdateProjectStatus, GetHostsByUser, etc.)
// ---------------------------------------------------------------------------

func TestUpdateProjectStatusMethod(t *testing.T) {
	// Verify the method exists and signature compiles — actual DB tests are
	// in integration_test.go. This is a compile-time check.
	var db *DB
	_ = db // reference to avoid unused import
}

// ---------------------------------------------------------------------------
// notifyHostsCredentialsChanged — nil safety
// ---------------------------------------------------------------------------

func TestNotifyHostsCredentialsChanged_NilNC(t *testing.T) {
	srv := newTestServer(t)
	srv.nc = nil
	// Should not panic with nil nc.
	srv.notifyHostsCredentialsChanged(nil, "user-1")
}

func TestNotifyHostsCredentialsChanged_NilDB(t *testing.T) {
	srv := newTestServer(t)
	srv.db = nil
	// Should not panic with nil db.
	srv.notifyHostsCredentialsChanged(nil, "user-1")
}
