/**
 * Typed subject and KV key builders for mclaude-web.
 * Mirrors semantics of mclaude-common/pkg/subj in TypeScript.
 * ADR-0024: typed slug scheme for subjects, URLs, and KV keys.
 * ADR-0035: host-scoped subjects — every project-scoped builder takes hslug.
 *
 * All builders accept branded slug types only.
 * Passing a raw string is rejected at the TypeScript type level.
 */

import type { UserSlug, HostSlug, ProjectSlug, SessionSlug, ClusterSlug } from './slug'

const PREFIX = 'mclaude'

// --------------------------------------------------------------------------
// User-scoped subjects (no host qualifier)
// --------------------------------------------------------------------------

export function subjProjectsCreate(uslug: UserSlug, hslug: HostSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.api.projects.create`
}

export function subjProjectsUpdated(uslug: UserSlug): string {
  return `${PREFIX}.users.${uslug}.api.projects.updated`
}

export function subjQuota(uslug: UserSlug): string {
  return `${PREFIX}.users.${uslug}.quota`
}

// --------------------------------------------------------------------------
// User+host-scoped subjects (ADR-0035)
// --------------------------------------------------------------------------

export function subjUserHostStatus(uslug: UserSlug, hslug: HostSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.status`
}

export function subjProjectsProvision(uslug: UserSlug, hslug: HostSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.api.projects.provision`
}

export function subjProjectsUpdate(uslug: UserSlug, hslug: HostSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.api.projects.update`
}

export function subjProjectsDelete(uslug: UserSlug, hslug: HostSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.api.projects.delete`
}

// --------------------------------------------------------------------------
// User+host+project-scoped session subjects (ADR-0054)
// Format: mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{...}
// No .api. prefix — ADR-0054 consolidated all session subjects under sessions.>
// --------------------------------------------------------------------------

export function subjSessionsInput(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug, sslug: SessionSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.sessions.${sslug}.input`
}

export function subjSessionsControl(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug, sslug: SessionSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.sessions.${sslug}.control`
}

export function subjSessionsCreate(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.sessions.create`
}

export function subjSessionsDelete(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug, sslug: SessionSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.sessions.${sslug}.delete`
}

export function subjSessionsRestart(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug, sslug: SessionSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.sessions.${sslug}.restart`
}

export function subjTerminal(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug, action: string): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.terminal.${action}`
}

export function subjTerminalWildcard(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.terminal.>`
}

// --------------------------------------------------------------------------
// Event and lifecycle subjects (ADR-0054)
// Events and lifecycle are now under sessions.{sslug}.events / sessions.{sslug}.lifecycle.*
// --------------------------------------------------------------------------

export function subjEvents(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug, sslug: SessionSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.sessions.${sslug}.events`
}

export function subjEventsWildcard(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.sessions.*.events`
}

export function subjSessionsApi(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.sessions._api`
}

export function subjLifecycle(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug, sslug: SessionSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.sessions.${sslug}.lifecycle.>`
}

export function subjLifecycleWildcard(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.sessions.*.lifecycle.>`
}

// --------------------------------------------------------------------------
// Cluster-scoped subjects (unchanged — these are not host-scoped)
// --------------------------------------------------------------------------

export function subjClusterProvision(cslug: ClusterSlug): string {
  return `mclaude.clusters.${cslug}.api.projects.provision`
}

export function subjClusterStatus(cslug: ClusterSlug): string {
  return `mclaude.clusters.${cslug}.api.status`
}

// --------------------------------------------------------------------------
// KV key helpers (ADR-0054)
// --------------------------------------------------------------------------

/**
 * mclaude-sessions-{uslug} KV key (ADR-0054 — user slug is in the bucket name).
 * Key format: hosts.{hslug}.projects.{pslug}.sessions.{sslug}
 */
export function kvKeySession(hslug: HostSlug, pslug: ProjectSlug, sslug: SessionSlug): string {
  return `hosts.${hslug}.projects.${pslug}.sessions.${sslug}`
}

/**
 * Watch all sessions in the per-user bucket: > (bucket name encodes user slug — ADR-0054).
 * The uslug parameter is intentionally unused; it is kept for call-site clarity.
 */
export function kvKeySessionsForUser(_uslug: UserSlug): string {
  return `>`
}

/**
 * mclaude-projects-{uslug} KV key (ADR-0054 — user slug is in the bucket name).
 * Key format: hosts.{hslug}.projects.{pslug}
 */
export function kvKeyProject(hslug: HostSlug, pslug: ProjectSlug): string {
  return `hosts.${hslug}.projects.${pslug}`
}

/**
 * Watch all projects in the per-user bucket: > (bucket name encodes user slug — ADR-0054).
 * The uslug parameter is intentionally unused; it is kept for call-site clarity.
 */
export function kvKeyProjectsForUser(_uslug: UserSlug): string {
  return `>`
}

/** mclaude-clusters KV key: {uslug} */
export function kvKeyUserClusters(uslug: UserSlug): string {
  return `${uslug}`
}

/**
 * mclaude-hosts KV key (ADR-0054 — shared bucket, globally unique host slugs).
 * Key format: {hslug} (flat, no user prefix)
 */
export function kvKeyHost(hslug: HostSlug): string {
  return `${hslug}`
}

/**
 * Watch all accessible hosts in the shared mclaude-hosts bucket: > (ADR-0054).
 * JWT scopes delivery to the user's permitted hosts server-side.
 * The uslug parameter is intentionally unused; it is kept for call-site clarity.
 */
export function kvKeyHostsForUser(_uslug: UserSlug): string {
  return `>`
}

export function kvKeyJob(uslug: UserSlug, jobId: string): string {
  return `${uslug}.${jobId}`
}
