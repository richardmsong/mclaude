// Package subj provides typed NATS subject and KV key construction helpers
// for mclaude. Every helper accepts only typed slug wrappers from
// mclaude-common/pkg/slug — passing a raw string is a compile-time error.
//
// Subject patterns are defined in docs/spec-state-schema.md.
// ADR-0035 inserts .hosts.{hslug}. between the user and project levels in
// all project-scoped subjects, KV keys, and JetStream filter constants.
// ADR-0054 consolidates all session activity into a single per-user stream
// (MCLAUDE_SESSIONS_{uslug}) with the unified sessions.> subject hierarchy,
// and migrates KV buckets to per-user isolation with hierarchical key formats.
package subj

import (
	"mclaude.io/common/pkg/slug"
)

// --------------------------------------------------------------------------
// JetStream stream filter constants
// ADR-0054: three legacy filters replaced by single FilterMclaudeSessions.
// --------------------------------------------------------------------------

// FilterMclaudeSessions is the subject filter for the consolidated per-user
// MCLAUDE_SESSIONS_{uslug} JetStream stream. Captures all session activity
// (events, commands, lifecycle) under the sessions.> hierarchy.
const FilterMclaudeSessions = "mclaude.users.*.hosts.*.projects.*.sessions.>"

// --------------------------------------------------------------------------
// User-scoped subjects (core pub/sub — no host level)
// --------------------------------------------------------------------------

// UserAPIProjectsCreate returns:
//
//	mclaude.users.{uslug}.api.projects.create
//
// Publisher: SPA. Subscriber: control-plane (request/reply).
func UserAPIProjectsCreate(u slug.UserSlug) string {
	return "mclaude.users." + string(u) + ".api.projects.create"
}

// UserAPIProjectsUpdated returns:
//
//	mclaude.users.{uslug}.api.projects.updated
//
// Publisher: control-plane. Subscriber: SPA.
func UserAPIProjectsUpdated(u slug.UserSlug) string {
	return "mclaude.users." + string(u) + ".api.projects.updated"
}

// UserQuota returns:
//
//	mclaude.users.{uslug}.quota
//
// Publisher: designated per-project agent (runQuotaPublisher).
// Subscriber: QuotaMonitor per-session goroutine.
func UserQuota(u slug.UserSlug) string {
	return "mclaude.users." + string(u) + ".quota"
}

// --------------------------------------------------------------------------
// User+host-scoped subjects
// --------------------------------------------------------------------------

// UserHostStatus returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.status
//
// Publisher: host controller (heartbeat). Subscriber: control-plane, SPA.
func UserHostStatus(u slug.UserSlug, h slug.HostSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".status"
}

// --------------------------------------------------------------------------
// Host-scoped provisioning subjects (ADR-0054)
// CP publishes to these subjects after validating the HTTP project
// creation/deletion request. Host controllers receive them via their
// mclaude.hosts.{hslug}.> subscription.
// --------------------------------------------------------------------------

// HostUserProjectsCreate returns:
//
//	mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create
//
// Publisher: control-plane (fan-out after HTTP validation).
// Subscriber: host controller (mclaude.hosts.{hslug}.> subscription).
func HostUserProjectsCreate(h slug.HostSlug, u slug.UserSlug, p slug.ProjectSlug) string {
	return "mclaude.hosts." + string(h) + ".users." + string(u) + ".projects." + string(p) + ".create"
}

// HostUserProjectsDelete returns:
//
//	mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.delete
//
// Publisher: control-plane (fan-out).
// Subscriber: host controller (mclaude.hosts.{hslug}.> subscription).
func HostUserProjectsDelete(h slug.HostSlug, u slug.UserSlug, p slug.ProjectSlug) string {
	return "mclaude.hosts." + string(h) + ".users." + string(u) + ".projects." + string(p) + ".delete"
}

// --------------------------------------------------------------------------
// User+host+project+session-scoped subjects (ADR-0054 sessions.> hierarchy)
// All project-scoped subjects include .hosts.{hslug}. between user and project.
// These replace the previous .api.sessions.* and .events.* / .lifecycle.*
// subject trees with a single consolidated sessions.> hierarchy captured by
// the per-user MCLAUDE_SESSIONS_{uslug} stream.
// --------------------------------------------------------------------------

// UserHostProjectSessionsCreate returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.create
//
// Publisher: SPA. Subscriber: session-agent (via MCLAUDE_SESSIONS stream).
func UserHostProjectSessionsCreate(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".sessions.create"
}

// UserHostProjectSessionsEvents returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.events
//
// Publisher: session-agent. Subscriber: SPA (via MCLAUDE_SESSIONS stream).
func UserHostProjectSessionsEvents(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug, s slug.SessionSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".sessions." + string(s) + ".events"
}

// UserHostProjectSessionsInput returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.input
//
// Publisher: SPA, QuotaMonitor. Subscriber: session-agent (via MCLAUDE_SESSIONS stream).
func UserHostProjectSessionsInput(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug, s slug.SessionSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".sessions." + string(s) + ".input"
}

// UserHostProjectSessionsDelete returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.delete
//
// Publisher: SPA. Subscriber: session-agent (via MCLAUDE_SESSIONS stream).
func UserHostProjectSessionsDelete(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug, s slug.SessionSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".sessions." + string(s) + ".delete"
}

// UserHostProjectSessionsControl returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.control.{suffix}
//
// suffix is one of: "interrupt", "restart".
// Publisher: SPA. Subscriber: session-agent (via MCLAUDE_SESSIONS stream).
func UserHostProjectSessionsControl(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug, s slug.SessionSlug, suffix string) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".sessions." + string(s) + ".control." + suffix
}

// UserHostProjectSessionsConfig returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.config
//
// Publisher: SPA. Subscriber: session-agent (via MCLAUDE_SESSIONS stream).
func UserHostProjectSessionsConfig(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug, s slug.SessionSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".sessions." + string(s) + ".config"
}

// UserHostProjectSessionsLifecycle returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.lifecycle.{suffix}
//
// suffix is one of: "started", "stopped", "error".
// Publisher: session-agent. Subscriber: SPA (via MCLAUDE_SESSIONS stream).
func UserHostProjectSessionsLifecycle(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug, s slug.SessionSlug, suffix string) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".sessions." + string(s) + ".lifecycle." + suffix
}

// --------------------------------------------------------------------------
// Terminal subjects (unchanged from ADR-0035 — NOT part of the sessions.>
// rename per ADR-0054. Terminal I/O remains under api.terminal.* prefix.)
// --------------------------------------------------------------------------

// UserHostProjectAPITerminal returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.{suffix}
//
// Publisher: SPA. Subscriber: session-agent. suffix is a raw terminal I/O
// discriminator (e.g. "in", "out", "resize", "{termId}.input", "{termId}.output")
// — not a slug.
func UserHostProjectAPITerminal(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug, suffix string) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".api.terminal." + suffix
}

// --------------------------------------------------------------------------
// KV key helpers (ADR-0054 — literal type-tokens, per-user buckets)
//
// Keys now include literal type-tokens to form a hierarchical, human-readable
// structure. The user slug prefix is removed from keys because buckets are
// now per-user (mclaude-sessions-{uslug}, mclaude-projects-{uslug}).
// --------------------------------------------------------------------------

// SessionsKVKey returns the mclaude-sessions-{uslug} KV key:
//
//	hosts.{hslug}.projects.{pslug}.sessions.{sslug}
//
// ADR-0054: literal type-tokens inserted; user slug moved to the bucket name.
// Bucket: mclaude-sessions-{uslug}
func SessionsKVKey(h slug.HostSlug, p slug.ProjectSlug, s slug.SessionSlug) string {
	return "hosts." + string(h) + ".projects." + string(p) + ".sessions." + string(s)
}

// ProjectsKVKey returns the mclaude-projects-{uslug} KV key:
//
//	hosts.{hslug}.projects.{pslug}
//
// ADR-0054: literal type-tokens inserted; user slug moved to the bucket name.
// Bucket: mclaude-projects-{uslug}
func ProjectsKVKey(h slug.HostSlug, p slug.ProjectSlug) string {
	return "hosts." + string(h) + ".projects." + string(p)
}

// HostsKVKey returns the mclaude-hosts KV key:
//
//	{hslug}
//
// ADR-0054: flat key — the shared mclaude-hosts bucket uses flat {hslug} keys.
// Read access is scoped per-host in the user JWT.
// Bucket: mclaude-hosts (shared)
func HostsKVKey(h slug.HostSlug) string {
	return string(h)
}
