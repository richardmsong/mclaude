# ADR: Shared KV State Types in mclaude-common

**Status**: draft
**Status history**:
- 2026-04-29: draft

## Overview

Consolidate duplicated KV state types (`SessionState`, `ProjectState`, `HostState`, `JobEntry`, `UsageStats`, `QuotaStatus`, `Capabilities`) into `mclaude-common/pkg/types/` as the single source of truth. Currently, `mclaude-session-agent` defines its own versions in `state.go` with field divergences, and `mclaude-control-plane` uses differently-named types (`ProjectKVState`, `HostKVState`) with structural mismatches. The session-agent does not import `mclaude-common/pkg/types` at all.

## Motivation

Deferred from ADR-0052 (spec-implementation gap remediation). When multiple components serialize/deserialize the same KV data with different struct definitions, field mismatches cause silent data loss or parsing failures. The `spec-state-schema.md` defines canonical schemas, but each component has its own interpretation.

## Current State

**mclaude-common/pkg/types/**:
- Defines `SessionState`, `ProjectState`, `JobEntry`, `UsageStats`, `Capabilities`, `QuotaStatus`
- These are intended to be the shared canonical types

**mclaude-session-agent/state.go**:
- Defines its own `SessionState`, `UsageStats`, `Capabilities`, `QuotaStatus` locally
- Does NOT import `mclaude-common/pkg/types`
- Field divergences exist (e.g., different field names, missing fields)

**mclaude-control-plane**:
- Uses `ProjectKVState` and `HostKVState` (different names from common types)
- Structural mismatches with `mclaude-common` definitions

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Canonical location | `mclaude-common/pkg/types/` | Already exists as the intended shared package |
| Migration approach | TODO: decide — update common types to superset of all component fields, then migrate consumers one at a time |
| Naming | TODO: decide — keep component-specific names (ProjectKVState) as aliases, or rename to canonical names |

## Open questions

- Should `mclaude-common/pkg/types` types match `spec-state-schema.md` exactly, or should they be the superset of what all components actually use?
- Should components that add extra fields (not in spec) contribute those back to the shared type or keep them as embedded extensions?
- What's the testing strategy — integration test that serializes from one component and deserializes from another?

## Component Changes

### mclaude-common
- Reconcile `pkg/types/` structs with `spec-state-schema.md` and all component usages
- Ensure JSON tags match the KV serialization format all components expect

### mclaude-session-agent
- Remove local `SessionState`, `UsageStats`, `Capabilities`, `QuotaStatus` from `state.go`
- Import and use `mclaude-common/pkg/types` instead
- Fix any field name / type mismatches

### mclaude-control-plane
- Alias or replace `ProjectKVState` / `HostKVState` with common types
- Fix any structural mismatches

### mclaude-controller-k8s
- Verify it uses common types for any KV deserialization

## Scope

### In scope
- Reconcile all KV state types into mclaude-common
- Migrate session-agent to import shared types
- Migrate control-plane to import shared types
- Verify controller-k8s compatibility

### Out of scope
- TypeScript type alignment (mclaude-web has its own TS interfaces)
- Adding new fields to state types
- Changing KV serialization format

## Integration Test Cases

| Test case | What it verifies | Components exercised |
|-----------|------------------|----------------------|
| Session KV round-trip | Session-agent writes SessionState to KV, control-plane reads it — all fields preserved | session-agent, control-plane, mclaude-common |
| Project KV round-trip | Control-plane writes ProjectKVState to KV, SPA reads it — all fields preserved | control-plane, mclaude-web |

## Implementation Plan

| Component | Est. lines | Notes |
|-----------|------------|-------|
| mclaude-common | ~100-200 | Reconcile type definitions |
| mclaude-session-agent | ~150-250 | Replace local types with imports, fix mismatches |
| mclaude-control-plane | ~50-100 | Alias or replace component-specific types |
| mclaude-controller-k8s | ~20-50 | Verify / fix imports |
