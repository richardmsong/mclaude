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
// User+host+project-scoped API subjects (ADR-0035)
// All project-scoped subjects include .hosts.{hslug}. between user and project.
// --------------------------------------------------------------------------

export function subjSessionsInput(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.api.sessions.input`
}

export function subjSessionsControl(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.api.sessions.control`
}

export function subjSessionsCreate(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.api.sessions.create`
}

export function subjSessionsDelete(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.api.sessions.delete`
}

export function subjSessionsRestart(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.api.sessions.restart`
}

export function subjTerminal(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug, action: string): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.api.terminal.${action}`
}

export function subjTerminalWildcard(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.api.terminal.>`
}

// --------------------------------------------------------------------------
// Event and lifecycle subjects (ADR-0035)
// --------------------------------------------------------------------------

export function subjEvents(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug, sslug: SessionSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.events.${sslug}`
}

export function subjEventsApi(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.events._api`
}

export function subjLifecycle(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug, sslug: SessionSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.lifecycle.${sslug}`
}

export function subjLifecycleWildcard(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.hosts.${hslug}.projects.${pslug}.lifecycle.>`
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
// KV key helpers (ADR-0035)
// --------------------------------------------------------------------------

/** mclaude-sessions KV key: {uslug}.{hslug}.{pslug}.{sslug} */
export function kvKeySession(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug, sslug: SessionSlug): string {
  return `${uslug}.${hslug}.${pslug}.${sslug}`
}

/** Watch all sessions for a user across all hosts: {uslug}.> */
export function kvKeySessionsForUser(uslug: UserSlug): string {
  return `${uslug}.>`
}

/** mclaude-projects KV key: {uslug}.{hslug}.{pslug} */
export function kvKeyProject(uslug: UserSlug, hslug: HostSlug, pslug: ProjectSlug): string {
  return `${uslug}.${hslug}.${pslug}`
}

/** Watch all projects for a user across all hosts: {uslug}.> */
export function kvKeyProjectsForUser(uslug: UserSlug): string {
  return `${uslug}.>`
}

/** mclaude-clusters KV key: {uslug} */
export function kvKeyUserClusters(uslug: UserSlug): string {
  return `${uslug}`
}

/** mclaude-hosts KV key: {uslug}.{hslug} (renamed from kvKeyLaptop per ADR-0035) */
export function kvKeyHost(uslug: UserSlug, hslug: HostSlug): string {
  return `${uslug}.${hslug}`
}

/** Watch all hosts for a user: {uslug}.* */
export function kvKeyHostsForUser(uslug: UserSlug): string {
  return `${uslug}.*`
}

export function kvKeyJob(uslug: UserSlug, jobId: string): string {
  return `${uslug}.${jobId}`
}
