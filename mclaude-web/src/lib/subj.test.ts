import { describe, it, expect } from 'vitest'
import {
  subjProjectsCreate,
  subjProjectsUpdated,
  subjQuota,
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
  kvKeyLaptop,
  kvKeyHeartbeatsForUser,
  kvKeyJob,
} from './subj'
import type { UserSlug, ProjectSlug, SessionSlug, ClusterSlug } from './slug'

// ── Type-safe test slugs ──────────────────────────────────────────────────────
// These casts are intentional — in tests we bypass the brand constructors.
const U = 'alice-gmail' as UserSlug
const P = 'mclaude' as ProjectSlug
const S = 's-42' as SessionSlug
const C = 'us-west' as ClusterSlug

describe('NATS subject builders', () => {
  describe('user-scoped subjects', () => {
    it('subjProjectsCreate matches spec', () => {
      expect(subjProjectsCreate(U)).toBe('mclaude.users.alice-gmail.api.projects.create')
    })

    it('subjProjectsUpdated matches spec', () => {
      expect(subjProjectsUpdated(U)).toBe('mclaude.users.alice-gmail.api.projects.updated')
    })

    it('subjQuota matches spec', () => {
      expect(subjQuota(U)).toBe('mclaude.users.alice-gmail.quota')
    })
  })

  describe('project-scoped API subjects', () => {
    it('subjSessionsInput matches spec', () => {
      expect(subjSessionsInput(U, P)).toBe(
        'mclaude.users.alice-gmail.projects.mclaude.api.sessions.input',
      )
    })

    it('subjSessionsControl matches spec', () => {
      expect(subjSessionsControl(U, P)).toBe(
        'mclaude.users.alice-gmail.projects.mclaude.api.sessions.control',
      )
    })

    it('subjSessionsCreate matches spec', () => {
      expect(subjSessionsCreate(U, P)).toBe(
        'mclaude.users.alice-gmail.projects.mclaude.api.sessions.create',
      )
    })

    it('subjSessionsDelete matches spec', () => {
      expect(subjSessionsDelete(U, P)).toBe(
        'mclaude.users.alice-gmail.projects.mclaude.api.sessions.delete',
      )
    })

    it('subjSessionsRestart matches spec', () => {
      expect(subjSessionsRestart(U, P)).toBe(
        'mclaude.users.alice-gmail.projects.mclaude.api.sessions.restart',
      )
    })

    it('subjTerminal(create) matches spec', () => {
      expect(subjTerminal(U, P, 'create')).toBe(
        'mclaude.users.alice-gmail.projects.mclaude.api.terminal.create',
      )
    })

    it('subjTerminalWildcard matches spec wildcard pattern', () => {
      expect(subjTerminalWildcard(U, P)).toBe(
        'mclaude.users.alice-gmail.projects.mclaude.api.terminal.>',
      )
    })
  })

  describe('events and lifecycle subjects', () => {
    it('subjEvents matches spec', () => {
      expect(subjEvents(U, P, S)).toBe(
        'mclaude.users.alice-gmail.projects.mclaude.events.s-42',
      )
    })

    it('subjEventsApi uses _api sentinel', () => {
      expect(subjEventsApi(U, P)).toBe(
        'mclaude.users.alice-gmail.projects.mclaude.events._api',
      )
    })

    it('subjLifecycle matches spec', () => {
      expect(subjLifecycle(U, P, S)).toBe(
        'mclaude.users.alice-gmail.projects.mclaude.lifecycle.s-42',
      )
    })

    it('subjLifecycleWildcard matches spec wildcard pattern', () => {
      expect(subjLifecycleWildcard(U, P)).toBe(
        'mclaude.users.alice-gmail.projects.mclaude.lifecycle.>',
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
      expect(subjProjectsCreate(U)).toMatch(/^mclaude\.users\.alice-gmail\./)
      expect(subjSessionsInput(U, P)).toMatch(/^mclaude\.users\.alice-gmail\./)
    })

    it('cluster-scoped subjects always start with mclaude.clusters.{cslug}', () => {
      expect(subjClusterProvision(C)).toMatch(/^mclaude\.clusters\.us-west\./)
    })

    it('reserved word "users" appears as literal, not as a slug value', () => {
      const s = subjProjectsCreate(U)
      // The token after 'mclaude.' is 'users' (literal), then the actual user slug
      const tokens = s.split('.')
      expect(tokens[1]).toBe('users')
      expect(tokens[2]).toBe('alice-gmail')
    })

    it('reserved word "projects" appears as literal between user and project slugs', () => {
      const s = subjSessionsInput(U, P)
      const tokens = s.split('.')
      expect(tokens[3]).toBe('projects')
      expect(tokens[4]).toBe('mclaude')
    })
  })
})

describe('KV key builders', () => {
  it('kvKeySession: {uslug}.{pslug}.{sslug}', () => {
    expect(kvKeySession(U, P, S)).toBe('alice-gmail.mclaude.s-42')
  })

  it('kvKeySessionsForUser: {uslug}.>', () => {
    expect(kvKeySessionsForUser(U)).toBe('alice-gmail.>')
  })

  it('kvKeyProject: {uslug}.{pslug}', () => {
    expect(kvKeyProject(U, P)).toBe('alice-gmail.mclaude')
  })

  it('kvKeyProjectsForUser: {uslug}.*', () => {
    expect(kvKeyProjectsForUser(U)).toBe('alice-gmail.*')
  })

  it('kvKeyUserClusters: {uslug}', () => {
    expect(kvKeyUserClusters(U)).toBe('alice-gmail')
  })

  it('kvKeyLaptop: {uslug}.{hostname}', () => {
    expect(kvKeyLaptop(U, 'my-macbook-pro')).toBe('alice-gmail.my-macbook-pro')
  })

  it('kvKeyHeartbeatsForUser: {uslug}.*', () => {
    expect(kvKeyHeartbeatsForUser(U)).toBe('alice-gmail.*')
  })

  it('kvKeyJob: {uslug}.{jobId}', () => {
    const jobId = '550e8400-e29b-41d4-a716-446655440000'
    expect(kvKeyJob(U, jobId)).toBe(`alice-gmail.${jobId}`)
  })
})
