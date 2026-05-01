import { describe, it, expect } from 'vitest'
import {
  subjProjectsCreate,
  subjProjectsUpdated,
  subjQuota,
  subjUserHostStatus,
  subjProjectsProvision,
  subjProjectsUpdate,
  subjProjectsDelete,
  subjSessionsInput,
  subjSessionsControl,
  subjSessionsCreate,
  subjSessionsDelete,
  subjSessionsRestart,
  subjTerminal,
  subjTerminalWildcard,
  subjEvents,
  subjEventsApi,
  subjLifecycle,
  subjLifecycleWildcard,
  subjClusterProvision,
  subjClusterStatus,
  kvKeySession,
  kvKeySessionsForUser,
  kvKeyProject,
  kvKeyProjectsForUser,
  kvKeyUserClusters,
  kvKeyHost,
  kvKeyHostsForUser,
  kvKeyJob,
} from './subj'
import type { UserSlug, HostSlug, ProjectSlug, SessionSlug, ClusterSlug } from './slug'

// ── Type-safe test slugs ──────────────────────────────────────────────────────
// These casts are intentional — in tests we bypass the brand constructors.
const U = 'alice-gmail' as UserSlug
const H = 'mbp16' as HostSlug
const P = 'mclaude' as ProjectSlug
const S = 's-42' as SessionSlug
const C = 'us-west' as ClusterSlug

describe('NATS subject builders', () => {
  describe('user-scoped subjects', () => {
    it('subjProjectsCreate matches spec (host-scoped)', () => {
      expect(subjProjectsCreate(U, H)).toBe('mclaude.users.alice-gmail.hosts.mbp16.api.projects.create')
    })

    it('subjProjectsUpdated matches spec', () => {
      expect(subjProjectsUpdated(U)).toBe('mclaude.users.alice-gmail.api.projects.updated')
    })

    it('subjQuota matches spec', () => {
      expect(subjQuota(U)).toBe('mclaude.users.alice-gmail.quota')
    })
  })

  describe('user+host-scoped subjects (ADR-0035)', () => {
    it('subjUserHostStatus matches spec', () => {
      expect(subjUserHostStatus(U, H)).toBe('mclaude.users.alice-gmail.hosts.mbp16.status')
    })

    it('subjProjectsProvision matches spec', () => {
      expect(subjProjectsProvision(U, H)).toBe('mclaude.users.alice-gmail.hosts.mbp16.api.projects.provision')
    })

    it('subjProjectsUpdate matches spec', () => {
      expect(subjProjectsUpdate(U, H)).toBe('mclaude.users.alice-gmail.hosts.mbp16.api.projects.update')
    })

    it('subjProjectsDelete matches spec', () => {
      expect(subjProjectsDelete(U, H)).toBe('mclaude.users.alice-gmail.hosts.mbp16.api.projects.delete')
    })
  })

  describe('host+project-scoped API subjects (ADR-0035)', () => {
    it('subjSessionsInput matches spec', () => {
      expect(subjSessionsInput(U, H, P)).toBe(
        'mclaude.users.alice-gmail.hosts.mbp16.projects.mclaude.api.sessions.input',
      )
    })

    it('subjSessionsControl matches spec', () => {
      expect(subjSessionsControl(U, H, P)).toBe(
        'mclaude.users.alice-gmail.hosts.mbp16.projects.mclaude.api.sessions.control',
      )
    })

    it('subjSessionsCreate matches spec', () => {
      expect(subjSessionsCreate(U, H, P)).toBe(
        'mclaude.users.alice-gmail.hosts.mbp16.projects.mclaude.api.sessions.create',
      )
    })

    it('subjSessionsDelete matches spec', () => {
      expect(subjSessionsDelete(U, H, P)).toBe(
        'mclaude.users.alice-gmail.hosts.mbp16.projects.mclaude.api.sessions.delete',
      )
    })

    it('subjSessionsRestart matches spec', () => {
      expect(subjSessionsRestart(U, H, P)).toBe(
        'mclaude.users.alice-gmail.hosts.mbp16.projects.mclaude.api.sessions.restart',
      )
    })

    it('subjTerminal(create) matches spec', () => {
      expect(subjTerminal(U, H, P, 'create')).toBe(
        'mclaude.users.alice-gmail.hosts.mbp16.projects.mclaude.api.terminal.create',
      )
    })

    it('subjTerminalWildcard matches spec wildcard pattern', () => {
      expect(subjTerminalWildcard(U, H, P)).toBe(
        'mclaude.users.alice-gmail.hosts.mbp16.projects.mclaude.api.terminal.>',
      )
    })
  })

  describe('events and lifecycle subjects (ADR-0035)', () => {
    it('subjEvents matches spec', () => {
      expect(subjEvents(U, H, P, S)).toBe(
        'mclaude.users.alice-gmail.hosts.mbp16.projects.mclaude.events.s-42',
      )
    })

    it('subjEventsApi uses _api sentinel', () => {
      expect(subjEventsApi(U, H, P)).toBe(
        'mclaude.users.alice-gmail.hosts.mbp16.projects.mclaude.events._api',
      )
    })

    it('subjLifecycle matches spec', () => {
      expect(subjLifecycle(U, H, P, S)).toBe(
        'mclaude.users.alice-gmail.hosts.mbp16.projects.mclaude.lifecycle.s-42',
      )
    })

    it('subjLifecycleWildcard matches spec wildcard pattern', () => {
      expect(subjLifecycleWildcard(U, H, P)).toBe(
        'mclaude.users.alice-gmail.hosts.mbp16.projects.mclaude.lifecycle.>',
      )
    })
  })

  describe('cluster-scoped subjects', () => {
    it('subjClusterProvision matches spec', () => {
      expect(subjClusterProvision(C)).toBe(
        'mclaude.clusters.us-west.api.projects.provision',
      )
    })

    it('subjClusterStatus matches spec', () => {
      expect(subjClusterStatus(C)).toBe(
        'mclaude.clusters.us-west.api.status',
      )
    })
  })

  describe('typed-literal structure invariants', () => {
    it('user-scoped subjects always start with mclaude.users.{uslug}', () => {
      expect(subjProjectsCreate(U, H)).toMatch(/^mclaude\.users\.alice-gmail\./)
      expect(subjSessionsInput(U, H, P)).toMatch(/^mclaude\.users\.alice-gmail\./)
    })

    it('cluster-scoped subjects always start with mclaude.clusters.{cslug}', () => {
      expect(subjClusterProvision(C)).toMatch(/^mclaude\.clusters\.us-west\./)
    })

    it('reserved word "users" appears as literal, not as a slug value', () => {
      const s = subjProjectsCreate(U, H)
      // The token after 'mclaude.' is 'users' (literal), then the actual user slug
      const tokens = s.split('.')
      expect(tokens[1]).toBe('users')
      expect(tokens[2]).toBe('alice-gmail')
    })

    it('host-scoped subjects contain .hosts.{hslug}. between user and project', () => {
      const s = subjSessionsInput(U, H, P)
      const tokens = s.split('.')
      expect(tokens[3]).toBe('hosts')
      expect(tokens[4]).toBe('mbp16')
      expect(tokens[5]).toBe('projects')
      expect(tokens[6]).toBe('mclaude')
    })
  })
})

describe('KV key builders (ADR-0054 — per-user buckets)', () => {
  it('kvKeySession: hosts.{hslug}.projects.{pslug}.sessions.{sslug} (user slug in bucket name)', () => {
    expect(kvKeySession(H, P, S)).toBe('hosts.mbp16.projects.mclaude.sessions.s-42')
  })

  it('kvKeySessionsForUser: > (watch all in per-user bucket)', () => {
    expect(kvKeySessionsForUser(U)).toBe('>')
  })

  it('kvKeyProject: hosts.{hslug}.projects.{pslug} (user slug in bucket name)', () => {
    expect(kvKeyProject(H, P)).toBe('hosts.mbp16.projects.mclaude')
  })

  it('kvKeyProjectsForUser: > (watch all in per-user bucket)', () => {
    expect(kvKeyProjectsForUser(U)).toBe('>')
  })

  it('kvKeyUserClusters: {uslug}', () => {
    expect(kvKeyUserClusters(U)).toBe('alice-gmail')
  })

  it('kvKeyHost: {hslug} (flat — no user prefix, globally unique per ADR-0054)', () => {
    expect(kvKeyHost(H)).toBe('mbp16')
  })

  it('kvKeyHostsForUser: > (watch all; JWT scopes delivery per-host)', () => {
    expect(kvKeyHostsForUser(U)).toBe('>')
  })

  it('kvKeyJob: {uslug}.{jobId}', () => {
    const jobId = '550e8400-e29b-41d4-a716-446655440000'
    expect(kvKeyJob(U, jobId)).toBe(`alice-gmail.${jobId}`)
  })
})
