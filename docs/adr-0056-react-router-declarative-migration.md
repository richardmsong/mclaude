# ADR: React Router Declarative Migration

**Status**: draft
**Status history**:
- 2026-04-29: draft

## Overview

Migrate the SPA from manual `parseRoute()` hash routing to declarative React Router `<Route>` elements. The app already uses `react-router-dom` v7.14.2 with `HashRouter`, but routes are resolved via a custom `parseRoute()` function instead of React Router's built-in `<Routes>`/`<Route>` tree. This creates a parallel routing system that bypasses React Router's features (loaders, error boundaries, outlet nesting).

## Motivation

Deferred from ADR-0052 (spec-implementation gap remediation). The spec says the SPA uses "React Router v6 with parametric segments" but the actual implementation uses a manual `parseRoute()` function alongside `HashRouter`. This hybrid approach:
- Loses React Router's built-in code splitting, loaders, and error boundaries
- Makes route-level state management harder
- Creates confusion for contributors expecting standard React Router patterns

## Current State

- `react-router-dom` v7.14.2 installed, `HashRouter` wrapping the app
- 8 route patterns resolved by `parseRoute()`:
  - `/` — dashboard
  - `/projects/{pslug}` — project detail
  - `/projects/{pslug}/sessions/{sid}` — session detail
  - `/settings` — settings
  - `/admin` — admin panel
  - `/admin/users` — user management
  - `/login` — auth flow
  - `/callback` — OAuth callback
- Manual `parseRoute()` returns a route object; components switch on `route.name`

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Router type | Keep `HashRouter` | SPA is served from a single HTML file; hash routing avoids server-side routing config |
| Migration approach | TODO: decide — incremental (one route at a time) vs big-bang |
| Nested routes | TODO: decide — flat route list vs nested layout routes |

## Open questions

- Should this migration also introduce React Router loaders for data fetching, or keep the existing NATS/KV subscription model?
- Should error boundaries be added per-route?
- Is there any reason to switch from `HashRouter` to `BrowserRouter`?

## Component Changes

### mclaude-web
- Replace `parseRoute()` with `<Routes>`/`<Route>` tree
- Remove manual route switching logic
- Add route-level components as needed

## Scope

### In scope
- Replace manual routing with declarative `<Route>` elements
- Keep all 8 existing routes functional
- Keep `HashRouter`

### Out of scope
- Adding new routes
- Changing data fetching patterns
- Server-side rendering

## Integration Test Cases

| Test case | What it verifies | Components exercised |
|-----------|------------------|----------------------|
| All routes navigable | Navigate to each of 8 routes, verify correct component renders | mclaude-web |
| Deep link works | Load app with `/#/projects/{pslug}/sessions/{sid}`, verify session detail renders | mclaude-web |
| OAuth callback | Complete OAuth flow, verify redirect to dashboard | mclaude-web |

## Implementation Plan

| Component | Est. lines | Notes |
|-----------|------------|-------|
| mclaude-web | ~150-300 | Replace parseRoute + route switching; mostly moving existing components into Route tree |
