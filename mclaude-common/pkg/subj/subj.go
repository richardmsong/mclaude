// Package subj provides typed NATS subject and KV key construction helpers
// for mclaude. Every helper accepts only typed slug wrappers from
// mclaude-common/pkg/slug — passing a raw string is a compile-time error.
//
// Subject patterns are defined in docs/spec-state-schema.md and
// docs/adr-0024-typed-slugs.md.
package subj

import (
	"mclaude.io/common/pkg/slug"
)

// --------------------------------------------------------------------------
// JetStream stream filter constants
// --------------------------------------------------------------------------

// FilterMclaudeAPI is the subject filter for the MCLAUDE_API JetStream stream.
const FilterMclaudeAPI = "mclaude.users.*.projects.*.api.sessions.>"

// FilterMclaudeEvents is the subject filter for the MCLAUDE_EVENTS JetStream stream.
const FilterMclaudeEvents = "mclaude.users.*.projects.*.events.*"

// FilterMclaudeLifecycle is the subject filter for the MCLAUDE_LIFECYCLE JetStream stream.
const FilterMclaudeLifecycle = "mclaude.users.*.projects.*.lifecycle.*"

// --------------------------------------------------------------------------
// User-scoped subjects (core pub/sub)
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
// User+project-scoped API subjects
// --------------------------------------------------------------------------

// UserProjectAPISessionsInput returns:
//
//	mclaude.users.{uslug}.projects.{pslug}.api.sessions.input
//
// Publisher: SPA, daemon. Subscriber: session-agent.
func UserProjectAPISessionsInput(u slug.UserSlug, p slug.ProjectSlug) string {
	return "mclaude.users." + string(u) + ".projects." + string(p) + ".api.sessions.input"
}

// UserProjectAPISessionsControl returns:
//
//	mclaude.users.{uslug}.projects.{pslug}.api.sessions.control
//
// Publisher: SPA. Subscriber: session-agent.
func UserProjectAPISessionsControl(u slug.UserSlug, p slug.ProjectSlug) string {
	return "mclaude.users." + string(u) + ".projects." + string(p) + ".api.sessions.control"
}

// UserProjectAPISessionsCreate returns:
//
//	mclaude.users.{uslug}.projects.{pslug}.api.sessions.create
//
// Publisher: SPA, daemon. Subscriber: session-agent (request/reply).
func UserProjectAPISessionsCreate(u slug.UserSlug, p slug.ProjectSlug) string {
	return "mclaude.users." + string(u) + ".projects." + string(p) + ".api.sessions.create"
}

// UserProjectAPISessionsDelete returns:
//
//	mclaude.users.{uslug}.projects.{pslug}.api.sessions.delete
//
// Publisher: SPA, daemon. Subscriber: session-agent.
func UserProjectAPISessionsDelete(u slug.UserSlug, p slug.ProjectSlug) string {
	return "mclaude.users." + string(u) + ".projects." + string(p) + ".api.sessions.delete"
}

// UserProjectAPITerminal returns:
//
//	mclaude.users.{uslug}.projects.{pslug}.api.terminal.{suffix}
//
// Publisher: SPA. Subscriber: session-agent. suffix is a raw terminal I/O
// discriminator (e.g. "in", "out", "resize") — not a slug.
func UserProjectAPITerminal(u slug.UserSlug, p slug.ProjectSlug, suffix string) string {
	return "mclaude.users." + string(u) + ".projects." + string(p) + ".api.terminal." + suffix
}

// --------------------------------------------------------------------------
// Event and lifecycle subjects
// --------------------------------------------------------------------------

// UserProjectEvents returns:
//
//	mclaude.users.{uslug}.projects.{pslug}.events.{sslug}
//
// Publisher: session-agent. Subscriber: SPA (via MCLAUDE_EVENTS stream).
func UserProjectEvents(u slug.UserSlug, p slug.ProjectSlug, s slug.SessionSlug) string {
	return "mclaude.users." + string(u) + ".projects." + string(p) + ".events." + string(s)
}

// UserProjectLifecycle returns:
//
//	mclaude.users.{uslug}.projects.{pslug}.lifecycle.{sslug}
//
// Publisher: session-agent, daemon. Subscriber: SPA, daemon (via MCLAUDE_LIFECYCLE stream).
func UserProjectLifecycle(u slug.UserSlug, p slug.ProjectSlug, s slug.SessionSlug) string {
	return "mclaude.users." + string(u) + ".projects." + string(p) + ".lifecycle." + string(s)
}

// --------------------------------------------------------------------------
// Cluster-scoped subjects
// --------------------------------------------------------------------------

// ClusterAPIProjectsProvision returns:
//
//	mclaude.clusters.{cslug}.api.projects.provision
//
// Publisher: control-plane. Subscriber: worker controller (request/reply).
func ClusterAPIProjectsProvision(c slug.ClusterSlug) string {
	return "mclaude.clusters." + string(c) + ".api.projects.provision"
}

// ClusterAPIStatus returns:
//
//	mclaude.clusters.{cslug}.api.status
//
// Publisher: worker controller. Subscriber: control-plane.
func ClusterAPIStatus(c slug.ClusterSlug) string {
	return "mclaude.clusters." + string(c) + ".api.status"
}

// --------------------------------------------------------------------------
// KV key helpers
// --------------------------------------------------------------------------

// SessionsKVKey returns the mclaude-sessions KV key:
//
//	{uslug}.{pslug}.{sslug}
func SessionsKVKey(u slug.UserSlug, p slug.ProjectSlug, s slug.SessionSlug) string {
	return string(u) + "." + string(p) + "." + string(s)
}

// ProjectsKVKey returns the mclaude-projects KV key:
//
//	{uslug}.{pslug}
func ProjectsKVKey(u slug.UserSlug, p slug.ProjectSlug) string {
	return string(u) + "." + string(p)
}

// ClustersKVKey returns the mclaude-clusters KV key:
//
//	{uslug}
//
// The value is a JSON list of cluster slugs accessible to the user.
func ClustersKVKey(u slug.UserSlug) string {
	return string(u)
}

// LaptopsKVKey returns the mclaude-laptops KV key:
//
//	{uslug}.{hostname}
//
// hostname is the raw machine hostname (not a slug). Per ADR-0024, this
// transitions to {uslug}.{hslug} when ADR-0004 (BYOH) lands.
func LaptopsKVKey(u slug.UserSlug, hostname string) string {
	return string(u) + "." + hostname
}

// JobQueueKVKey returns the mclaude-job-queue KV key:
//
//	{uslug}.{jobId}
//
// jobId stays UUID v4 shaped (not a slug).
func JobQueueKVKey(u slug.UserSlug, jobID string) string {
	return string(u) + "." + jobID
}
