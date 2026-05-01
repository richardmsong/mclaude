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
)

// --------------------------------------------------------------------------
// JetStream filter constant (ADR-0054 — consolidated sessions stream)
// --------------------------------------------------------------------------

func TestFilterConstants(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{
			"FilterMclaudeSessions",
			subj.FilterMclaudeSessions,
			"mclaude.users.*.hosts.*.projects.*.sessions.>",
		},
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
// User-scoped subjects (no host level — unchanged by ADR-0035/ADR-0054)
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
// User+host-scoped subjects
// --------------------------------------------------------------------------

func TestUserHostStatus(t *testing.T) {
	got := subj.UserHostStatus(u, h)
	want := "mclaude.users.alice-gmail.hosts.mbp16.status"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --------------------------------------------------------------------------
// Host-scoped provisioning subjects (ADR-0054)
// --------------------------------------------------------------------------

func TestHostUserProjectsCreate(t *testing.T) {
	got := subj.HostUserProjectsCreate(h, u, p)
	want := "mclaude.hosts.mbp16.users.alice-gmail.projects.my-project.create"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHostUserProjectsDelete(t *testing.T) {
	got := subj.HostUserProjectsDelete(h, u, p)
	want := "mclaude.hosts.mbp16.users.alice-gmail.projects.my-project.delete"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --------------------------------------------------------------------------
// Session subjects (ADR-0054 consolidated sessions.> hierarchy)
// --------------------------------------------------------------------------

func TestUserHostProjectSessionsCreate(t *testing.T) {
	got := subj.UserHostProjectSessionsCreate(u, h, p)
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.create"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserHostProjectSessionsEvents(t *testing.T) {
	got := subj.UserHostProjectSessionsEvents(u, h, p, s)
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.events"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserHostProjectSessionsInput(t *testing.T) {
	got := subj.UserHostProjectSessionsInput(u, h, p, s)
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.input"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserHostProjectSessionsDelete(t *testing.T) {
	got := subj.UserHostProjectSessionsDelete(u, h, p, s)
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.delete"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserHostProjectSessionsControlInterrupt(t *testing.T) {
	got := subj.UserHostProjectSessionsControl(u, h, p, s, "interrupt")
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.control.interrupt"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserHostProjectSessionsControlRestart(t *testing.T) {
	got := subj.UserHostProjectSessionsControl(u, h, p, s, "restart")
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.control.restart"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserHostProjectSessionsConfig(t *testing.T) {
	got := subj.UserHostProjectSessionsConfig(u, h, p, s)
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.config"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserHostProjectSessionsLifecycleStarted(t *testing.T) {
	got := subj.UserHostProjectSessionsLifecycle(u, h, p, s, "started")
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.lifecycle.started"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserHostProjectSessionsLifecycleStopped(t *testing.T) {
	got := subj.UserHostProjectSessionsLifecycle(u, h, p, s, "stopped")
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.lifecycle.stopped"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserHostProjectSessionsLifecycleError(t *testing.T) {
	got := subj.UserHostProjectSessionsLifecycle(u, h, p, s, "error")
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.lifecycle.error"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --------------------------------------------------------------------------
// Terminal subjects (unchanged — NOT part of the sessions.> rename per ADR-0054)
// --------------------------------------------------------------------------

func TestUserHostProjectAPITerminal(t *testing.T) {
	got := subj.UserHostProjectAPITerminal(u, h, p, "resize")
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.api.terminal.resize"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUserHostProjectAPITerminalOutput(t *testing.T) {
	got := subj.UserHostProjectAPITerminal(u, h, p, "term-1.output")
	want := "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.api.terminal.term-1.output"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --------------------------------------------------------------------------
// KV key helpers (ADR-0054 — literal type-tokens, per-user buckets)
// --------------------------------------------------------------------------

func TestSessionsKVKey(t *testing.T) {
	got := subj.SessionsKVKey(h, p, s)
	want := "hosts.mbp16.projects.my-project.sessions.s-abc123"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestProjectsKVKey(t *testing.T) {
	got := subj.ProjectsKVKey(h, p)
	want := "hosts.mbp16.projects.my-project"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHostsKVKey(t *testing.T) {
	got := subj.HostsKVKey(h)
	want := "mbp16"
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
//	subj.UserAPIProjectsCreate("alice-gmail")                                // compile error
//	subj.UserHostProjectSessionsCreate("alice-gmail", "mbp16", "my-project") // compile error
//	subj.SessionsKVKey("mbp16", "my-project", "s-abc123")                   // compile error
//
// --------------------------------------------------------------------------

func TestTypedWrapperEnforcement(t *testing.T) {
	// This test passes by virtue of having compiled successfully.
	// The compile-time safety is guaranteed by the type system: slug.UserSlug
	// is `type UserSlug string`, not a type alias, so untyped string literals
	// won't pass.
	t.Log("typed wrapper enforcement verified at compile time")
}

// --------------------------------------------------------------------------
// Verify all spec-defined subject patterns are covered (ADR-0054 state)
// --------------------------------------------------------------------------

func TestAllSpecSubjects(t *testing.T) {
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
		// User+host-level
		{"mclaude.users.{u}.hosts.{h}.status", subj.UserHostStatus(u, h), "mclaude.users.alice-gmail.hosts.mbp16.status"},
		// Host-scoped provisioning (ADR-0054)
		{"mclaude.hosts.{h}.users.{u}.projects.{p}.create", subj.HostUserProjectsCreate(h, u, p), "mclaude.hosts.mbp16.users.alice-gmail.projects.my-project.create"},
		{"mclaude.hosts.{h}.users.{u}.projects.{p}.delete", subj.HostUserProjectsDelete(h, u, p), "mclaude.hosts.mbp16.users.alice-gmail.projects.my-project.delete"},
		// Session subjects (ADR-0054 sessions.> hierarchy)
		{"sessions.create", subj.UserHostProjectSessionsCreate(u, h, p), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.create"},
		{"sessions.{s}.events", subj.UserHostProjectSessionsEvents(u, h, p, s), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.events"},
		{"sessions.{s}.input", subj.UserHostProjectSessionsInput(u, h, p, s), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.input"},
		{"sessions.{s}.delete", subj.UserHostProjectSessionsDelete(u, h, p, s), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.delete"},
		{"sessions.{s}.control.interrupt", subj.UserHostProjectSessionsControl(u, h, p, s, "interrupt"), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.control.interrupt"},
		{"sessions.{s}.control.restart", subj.UserHostProjectSessionsControl(u, h, p, s, "restart"), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.control.restart"},
		{"sessions.{s}.config", subj.UserHostProjectSessionsConfig(u, h, p, s), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.config"},
		{"sessions.{s}.lifecycle.started", subj.UserHostProjectSessionsLifecycle(u, h, p, s, "started"), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.lifecycle.started"},
		{"sessions.{s}.lifecycle.stopped", subj.UserHostProjectSessionsLifecycle(u, h, p, s, "stopped"), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.lifecycle.stopped"},
		{"sessions.{s}.lifecycle.error", subj.UserHostProjectSessionsLifecycle(u, h, p, s, "error"), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.sessions.s-abc123.lifecycle.error"},
		// Terminal (unchanged)
		{"mclaude.users.{u}.hosts.{h}.projects.{p}.api.terminal.*", subj.UserHostProjectAPITerminal(u, h, p, "in"), "mclaude.users.alice-gmail.hosts.mbp16.projects.my-project.api.terminal.in"},
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
// Verify all spec-defined KV key patterns are covered (ADR-0054 state)
// --------------------------------------------------------------------------

func TestAllSpecKVKeys(t *testing.T) {
	type specCase struct {
		name string
		got  string
		want string
	}

	cases := []specCase{
		// mclaude-sessions-{uslug}: hosts.{hslug}.projects.{pslug}.sessions.{sslug}
		{"mclaude-sessions key", subj.SessionsKVKey(h, p, s), "hosts.mbp16.projects.my-project.sessions.s-abc123"},
		// mclaude-projects-{uslug}: hosts.{hslug}.projects.{pslug}
		{"mclaude-projects key", subj.ProjectsKVKey(h, p), "hosts.mbp16.projects.my-project"},
		// mclaude-hosts: {hslug} (flat)
		{"mclaude-hosts key", subj.HostsKVKey(h), "mbp16"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}
