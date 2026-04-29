// Package subj provides typed NATS subject and KV key construction helpers
// for mclaude. Every helper accepts only typed slug wrappers from
// mclaude-common/pkg/slug — passing a raw string is a compile-time error.
//
// Subject patterns are defined in docs/spec-state-schema.md,
// docs/adr-0024-typed-slugs.md, and docs/adr-0004-multi-laptop.md.
// ADR-0035 inserts .hosts.{hslug}. between the user and project levels in
// all project-scoped subjects, KV keys, and JetStream filter constants.
package subj

import (
	"mclaude.io/common/pkg/slug"
)

// --------------------------------------------------------------------------
// JetStream stream filter constants
// ADR-0035: .hosts.*. inserted between user and project.
// --------------------------------------------------------------------------

// FilterMclaudeAPI is the subject filter for the MCLAUDE_API JetStream stream.
const FilterMclaudeAPI = "mclaude.users.*.hosts.*.projects.*.api.sessions.>"

// FilterMclaudeEvents is the subject filter for the MCLAUDE_EVENTS JetStream stream.
const FilterMclaudeEvents = "mclaude.users.*.hosts.*.projects.*.events.*"

// FilterMclaudeLifecycle is the subject filter for the MCLAUDE_LIFECYCLE JetStream stream.
const FilterMclaudeLifecycle = "mclaude.users.*.hosts.*.projects.*.lifecycle.*"

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
// Publisher: daemon (runQuotaPublisher). Subscriber: QuotaMonitor per-session.
func UserQuota(u slug.UserSlug) string {
	return "mclaude.users." + string(u) + ".quota"
}

// --------------------------------------------------------------------------
// User+host-scoped subjects (ADR-0035)
// --------------------------------------------------------------------------

// UserHostStatus returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.status
//
// Publisher: daemon (heartbeat). Subscriber: control-plane, SPA.
func UserHostStatus(u slug.UserSlug, h slug.HostSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".status"
}

// UserHostProjectAPISessionsRestart returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.restart
//
// Publisher: SPA. Subscriber: session-agent (request/reply).
func UserHostProjectAPISessionsRestart(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".api.sessions.restart"
}

// UserHostAPIProjectsProvision returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.api.projects.provision
//
// Publisher: control-plane. Subscriber: daemon.
func UserHostAPIProjectsProvision(u slug.UserSlug, h slug.HostSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".api.projects.provision"
}

// UserHostAPIProjectsCreate returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.api.projects.create
//
// Publisher: SPA. Subscriber: control-plane (request/reply).
func UserHostAPIProjectsCreate(u slug.UserSlug, h slug.HostSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".api.projects.create"
}

// UserHostAPIProjectsUpdate returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.api.projects.update
//
// Publisher: SPA. Subscriber: control-plane (request/reply).
func UserHostAPIProjectsUpdate(u slug.UserSlug, h slug.HostSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".api.projects.update"
}

// UserHostAPIProjectsDelete returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.api.projects.delete
//
// Publisher: SPA. Subscriber: control-plane (request/reply).
func UserHostAPIProjectsDelete(u slug.UserSlug, h slug.HostSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".api.projects.delete"
}

// --------------------------------------------------------------------------
// User+host+project-scoped API subjects (ADR-0035)
// All project-scoped subjects include .hosts.{hslug}. between user and project.
// --------------------------------------------------------------------------

// UserHostProjectAPISessionsInput returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.input
//
// Publisher: SPA, daemon. Subscriber: session-agent.
func UserHostProjectAPISessionsInput(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".api.sessions.input"
}

// UserHostProjectAPISessionsControl returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.control
//
// Publisher: SPA. Subscriber: session-agent.
func UserHostProjectAPISessionsControl(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".api.sessions.control"
}

// UserHostProjectAPISessionsCreate returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.create
//
// Publisher: SPA, daemon. Subscriber: session-agent (request/reply).
func UserHostProjectAPISessionsCreate(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".api.sessions.create"
}

// UserHostProjectAPISessionsDelete returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.sessions.delete
//
// Publisher: SPA, daemon. Subscriber: session-agent.
func UserHostProjectAPISessionsDelete(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".api.sessions.delete"
}

// UserHostProjectAPITerminal returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.{suffix}
//
// Publisher: SPA. Subscriber: session-agent. suffix is a raw terminal I/O
// discriminator (e.g. "in", "out", "resize") — not a slug.
func UserHostProjectAPITerminal(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug, suffix string) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".api.terminal." + suffix
}

// --------------------------------------------------------------------------
// Event and lifecycle subjects (ADR-0035)
// --------------------------------------------------------------------------

// UserHostProjectEvents returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events.{sslug}
//
// Publisher: session-agent. Subscriber: SPA (via MCLAUDE_EVENTS stream).
func UserHostProjectEvents(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug, s slug.SessionSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".events." + string(s)
}

// UserHostProjectLifecycle returns:
//
//	mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}
//
// Publisher: session-agent, daemon. Subscriber: SPA, daemon (via MCLAUDE_LIFECYCLE stream).
func UserHostProjectLifecycle(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug, s slug.SessionSlug) string {
	return "mclaude.users." + string(u) + ".hosts." + string(h) + ".projects." + string(p) + ".lifecycle." + string(s)
}

// --------------------------------------------------------------------------
// KV key helpers (ADR-0035)
// --------------------------------------------------------------------------

// SessionsKVKey returns the mclaude-sessions KV key:
//
//	{uslug}.{hslug}.{pslug}.{sslug}
//
// Per ADR-0035, {hslug} is inserted between user and project.
func SessionsKVKey(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug, s slug.SessionSlug) string {
	return string(u) + "." + string(h) + "." + string(p) + "." + string(s)
}

// ProjectsKVKey returns the mclaude-projects KV key:
//
//	{uslug}.{hslug}.{pslug}
//
// Per ADR-0035, {hslug} is inserted between user and project.
func ProjectsKVKey(u slug.UserSlug, h slug.HostSlug, p slug.ProjectSlug) string {
	return string(u) + "." + string(h) + "." + string(p)
}

// HostsKVKey returns the mclaude-hosts KV key:
//
//	{uslug}.{hslug}
//
// Replaces LaptopsKVKey. Per ADR-0035, {hslug} is a typed HostSlug (no longer
// a raw machine hostname). Bucket name changed from mclaude-laptops to
// mclaude-hosts.
func HostsKVKey(u slug.UserSlug, h slug.HostSlug) string {
	return string(u) + "." + string(h)
}

// JobQueueKVKey returns the mclaude-job-queue KV key:
//
//	{uslug}.{jobId}
//
// jobId stays UUID v4 shaped (not a slug). Unchanged by ADR-0035.
func JobQueueKVKey(u slug.UserSlug, jobID string) string {
	return string(u) + "." + jobID
}
