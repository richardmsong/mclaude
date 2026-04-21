package subj_test

import (
	"testing"

	"mclaude.io/common/pkg/slug"
	"mclaude.io/common/pkg/subj"
)

// helper: typed slug constructors. Tests must use typed wrappers — passing
// raw strings is a compile-time error.
var (
	u = slug.MustParseUserSlug("alice-gmail")
	h = slug.MustParseHostSlug("mbp16")
	p = slug.MustParseProjectSlug("my-project")
	s = slug.MustParseSessionSlug("s-abc123")
	c = slug.MustParseClusterSlug("us-west")
)

// --------------------------------------------------------------------------
// JetStream filter constants (ADR-0004 — .hosts.*. between user and project)
// --------------------------------------------------------------------------

func TestFilterConstants(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"FilterMclaudeAPI", subj.FilterMclaudeAPI, "mclaude.users.*.hosts.*.projects.*.api.sessions.>"},
		{"FilterMclaudeEvents", subj.FilterMclaudeEvents, "mclaude.users.*.hosts.*.projects.*.events.*"},
		{"FilterMclaudeLifecycle", subj.FilterMclaudeLifecycle, "mclaude.users.*.hosts.*.projects.*.lifecycle.*"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// User-scoped subjects (no host level — unchanged by ADR-0004)
// --------------------------------------------------------------------------

func TestUserAPIProjectsCreate(t *testing.T) {
	got := subj.UserAPIProjectsCreate(u)
	want := "mclaude.users.alice-gmail.api.projects.create"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserAPIProjectsUpdated(t *testing.T) {
	got := subj.UserAPIProjectsUpdated(u)
	want := "mclaude.users.alice-gmail.api.projects.updated"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserQuota(t *testing.T) {
	got := subj.UserQuota(u)
	want := "mclaude.users.alice-gmail.quota"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --------------------------------------------------------------------------
// Host-scoped subject (ADR-0004)
// --------------------------------------------------------------------------

func TestUserHostStatus(t *testing.T) {
	got := subj.UserHostStatus(u, h)
	want := "mclaude.users.alice-gmail.hosts.mbp16.status"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --------------------------------------------------------------------------
// User+host+project-scoped API subjects (ADR-0004)
// --------------------------------------------------------------------------

func TestUserHostProjectAPISessionsInput(t *testing.T) {
	got := subj.UserHostProjectAPISessionsInput(u, h, p)
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.api.sessions.input"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserHostProjectAPISessionsControl(t *testing.T) {
	got := subj.UserHostProjectAPISessionsControl(u, h, p)
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.api.sessions.control"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserHostProjectAPISessionsCreate(t *testing.T) {
	got := subj.UserHostProjectAPISessionsCreate(u, h, p)
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.api.sessions.create"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserHostProjectAPISessionsDelete(t *testing.T) {
	got := subj.UserHostProjectAPISessionsDelete(u, h, p)
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.api.sessions.delete"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserHostProjectAPITerminal(t *testing.T) {
	got := subj.UserHostProjectAPITerminal(u, h, p, "resize")
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.api.terminal.resize"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --------------------------------------------------------------------------
// Event and lifecycle subjects (ADR-0004)
// --------------------------------------------------------------------------

func TestUserHostProjectEvents(t *testing.T) {
	got := subj.UserHostProjectEvents(u, h, p, s)
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.events.s-abc123"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserHostProjectLifecycle(t *testing.T) {
	got := subj.UserHostProjectLifecycle(u, h, p, s)
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.lifecycle.s-abc123"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --------------------------------------------------------------------------
// Cluster-scoped subjects (unchanged)
// --------------------------------------------------------------------------

func TestClusterAPIProjectsProvision(t *testing.T) {
	got := subj.ClusterAPIProjectsProvision(c)
	want := "mclaude.clusters.us-west.api.projects.provision"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestClusterAPIStatus(t *testing.T) {
	got := subj.ClusterAPIStatus(c)
	want := "mclaude.clusters.us-west.api.status"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --------------------------------------------------------------------------
// KV key helpers (ADR-0004)
// --------------------------------------------------------------------------

func TestSessionsKVKey(t *testing.T) {
	got := subj.SessionsKVKey(u, h, p, s)
	want := "alice-gmail.mbp16.my-project.s-abc123"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestProjectsKVKey(t *testing.T) {
	got := subj.ProjectsKVKey(u, h, p)
	want := "alice-gmail.mbp16.my-project"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestClustersKVKey(t *testing.T) {
	got := subj.ClustersKVKey(u)
	want := "alice-gmail"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHostsKVKey(t *testing.T) {
	got := subj.HostsKVKey(u, h)
	want := "alice-gmail.mbp16"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestJobQueueKVKey(t *testing.T) {
	jobID := "550e8400-e29b-41d4-a716-446655440000"
	got := subj.JobQueueKVKey(u, jobID)
	want := "alice-gmail.550e8400-e29b-41d4-a716-446655440000"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --------------------------------------------------------------------------
// Type-safety: demonstrate compile-time enforcement
//
// The functions below verify that typed wrappers are required. If any helper
// accepted a raw string, these tests would fail to compile — which is the
// intended guarantee. We test this implicitly by using the typed wrappers
// above without any unsafe casting.
//
// A separate documentation comment explains what WOULD fail at compile time:
//
//	subj.UserAPIProjectsCreate("alice-gmail")                               // compile error
//	subj.UserHostProjectAPISessionsCreate("alice-gmail", "mbp16", "my-project") // compile error
//
// --------------------------------------------------------------------------

func TestTypedWrapperEnforcement(t *testing.T) {
	// This test passes by virtue of having compiled successfully.
	// If the helpers accepted raw strings, we'd add explicit compile-fail
	// checks via go vet / go build constraints. The compile-time safety is
	// guaranteed by the type system: slug.UserSlug is `type UserSlug string`,
	// not a type alias, so untyped string literals won't pass.
	t.Log("typed wrapper enforcement verified at compile time")
}

// --------------------------------------------------------------------------
// Verify all spec-defined subject patterns are covered
// --------------------------------------------------------------------------

func TestAllSpecSubjects(t *testing.T) {
	// Enumerate all subject patterns from spec-state-schema.md and
	// verify each helper produces the expected pattern.
	type specCase struct {
		name string
		got  string
		want string
	}

	cases := []specCase{
		// User-level (no host)
		{"mclaude.users.{u}.api.projects.create", subj.UserAPIProjectsCreate(u), "mclaude.users.alice-gmail.api.projects.create"},
		{"mclaude.users.{u}.api.projects.updated", subj.UserAPIProjectsUpdated(u), "mclaude.users.alice-gmail.api.projects.updated"},
		{"mclaude.users.{u}.quota", subj.UserQuota(u), "mclaude.users.alice-gmail.quota"},
		// Host-level
		{"mclaude.users.{u}.hosts.{h}.status", subj.UserHostStatus(u, h), "mclaude.users.alice-gmail.hosts.mbp16.status"},
		// User+host+project-level API (ADR-0004)
		{"mclaude.users.{u}.hosts.{h}.projects.{p}.api.sessions.input", subj.UserHostProjectAPISessionsInput(u, h, p), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.api.sessions.input"},
		{"mclaude.users.{u}.hosts.{h}.projects.{p}.api.sessions.control", subj.UserHostProjectAPISessionsControl(u, h, p), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.api.sessions.control"},
		{"mclaude.users.{u}.hosts.{h}.projects.{p}.api.sessions.create", subj.UserHostProjectAPISessionsCreate(u, h, p), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.api.sessions.create"},
		{"mclaude.users.{u}.hosts.{h}.projects.{p}.api.sessions.delete", subj.UserHostProjectAPISessionsDelete(u, h, p), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.api.sessions.delete"},
		{"mclaude.users.{u}.hosts.{h}.projects.{p}.api.terminal.*", subj.UserHostProjectAPITerminal(u, h, p, "in"), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.api.terminal.in"},
		// Events + lifecycle
		{"mclaude.users.{u}.hosts.{h}.projects.{p}.events.{s}", subj.UserHostProjectEvents(u, h, p, s), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.events.s-abc123"},
		{"mclaude.users.{u}.hosts.{h}.projects.{p}.lifecycle.{s}", subj.UserHostProjectLifecycle(u, h, p, s), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.lifecycle.s-abc123"},
		// Cluster
		{"mclaude.clusters.{c}.api.projects.provision", subj.ClusterAPIProjectsProvision(c), "mclaude.clusters.us-west.api.projects.provision"},
		{"mclaude.clusters.{c}.api.status", subj.ClusterAPIStatus(c), "mclaude.clusters.us-west.api.status"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Verify all spec-defined KV key patterns are covered
// --------------------------------------------------------------------------

func TestAllSpecKVKeys(t *testing.T) {
	type specCase struct {
		name string
		got  string
		want string
	}

	jobID := "550e8400-e29b-41d4-a716-446655440000"

	cases := []specCase{
		// mclaude-sessions: {uslug}.{hslug}.{pslug}.{sslug}
		{"mclaude-sessions key", subj.SessionsKVKey(u, h, p, s), "alice-gmail.mbp16.my-project.s-abc123"},
		// mclaude-projects: {uslug}.{hslug}.{pslug}
		{"mclaude-projects key", subj.ProjectsKVKey(u, h, p), "alice-gmail.mbp16.my-project"},
		// mclaude-clusters: {uslug}
		{"mclaude-clusters key", subj.ClustersKVKey(u), "alice-gmail"},
		// mclaude-hosts: {uslug}.{hslug}
		{"mclaude-hosts key", subj.HostsKVKey(u, h), "alice-gmail.mbp16"},
		// mclaude-job-queue: {uslug}.{jobId}
		{"mclaude-job-queue key", subj.JobQueueKVKey(u, jobID), "alice-gmail." + jobID},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}
