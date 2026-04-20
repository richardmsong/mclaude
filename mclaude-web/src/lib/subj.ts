/**
 * Typed subject and KV key builders for mclaude-web.
 * Mirrors semantics of mclaude-common/pkg/subj in TypeScript.
 * ADR-0024: typed slug scheme for subjects, URLs, and KV keys.
 *
 * All builders accept branded slug types only.
 * Passing a raw string is rejected at the TypeScript type level.
 */

import type { UserSlug, ProjectSlug, SessionSlug, ClusterSlug } from './slug'

const PREFIX = 'mclaude'

export function subjProjectsCreate(uslug: UserSlug): string {
  return `${PREFIX}.users.${uslug}.api.projects.create`
}

export function subjProjectsUpdated(uslug: UserSlug): string {
  return `${PREFIX}.users.${uslug}.api.projects.updated`
}

export function subjQuota(uslug: UserSlug): string {
  return `${PREFIX}.users.${uslug}.quota`
}

export function subjSessionsInput(uslug: UserSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.projects.${pslug}.api.sessions.input`
}

export function subjSessionsControl(uslug: UserSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.projects.${pslug}.api.sessions.control`
}

export function subjSessionsCreate(uslug: UserSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.projects.${pslug}.api.sessions.create`
}

export function subjSessionsDelete(uslug: UserSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.projects.${pslug}.api.sessions.delete`
}

export function subjSessionsRestart(uslug: UserSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.projects.${pslug}.api.sessions.restart`
}

export function subjTerminal(uslug: UserSlug, pslug: ProjectSlug, action: string): string {
  return `${PREFIX}.users.${uslug}.projects.${pslug}.api.terminal.${action}`
}

export function subjTerminalWildcard(uslug: UserSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.projects.${pslug}.api.terminal.>`
}

export function subjEvents(uslug: UserSlug, pslug: ProjectSlug, sslug: SessionSlug): string {
  return `${PREFIX}.users.${uslug}.projects.${pslug}.events.${sslug}`
}

export function subjEventsApi(uslug: UserSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.projects.${pslug}.events._api`
}

export function subjLifecycle(uslug: UserSlug, pslug: ProjectSlug, sslug: SessionSlug): string {
  return `${PREFIX}.users.${uslug}.projects.${pslug}.lifecycle.${sslug}`
}

export function subjLifecycleWildcard(uslug: UserSlug, pslug: ProjectSlug): string {
  return `${PREFIX}.users.${uslug}.projects.${pslug}.lifecycle.>`
}

export function subjClusterProvision(cslug: ClusterSlug): string {
  return `mclaude.clusters.${cslug}.api.projects.provision`
}

export function subjClusterStatus(cslug: ClusterSlug): string {
  return `mclaude.clusters.${cslug}.api.status`
}

export function kvKeySession(uslug: UserSlug, pslug: ProjectSlug, sslug: SessionSlug): string {
  return `${uslug}.${pslug}.${sslug}`
}

export function kvKeySessionsForUser(uslug: UserSlug): string {
  return `${uslug}.>`
}

export function kvKeyProject(uslug: UserSlug, pslug: ProjectSlug): string {
  return `${uslug}.${pslug}`
}

export function kvKeyProjectsForUser(uslug: UserSlug): string {
  return `${uslug}.*`
}

export function kvKeyUserClusters(uslug: UserSlug): string {
  return `${uslug}`
}

export function kvKeyLaptop(uslug: UserSlug, hostname: string): string {
  return `${uslug}.${hostname}`
}

export function kvKeyHeartbeatsForUser(uslug: UserSlug): string {
  return `${uslug}.*`
}

export function kvKeyJob(uslug: UserSlug, jobId: string): string {
  return `${uslug}.${jobId}`
}
