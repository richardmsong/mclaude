# ADR: Per-Project Cost Grouping View

**Status**: draft
**Status history**:
- 2026-04-29: draft

## Overview

Add a dedicated per-project cost aggregation view to the SPA. Basic client-side aggregation already exists in `TokenUsage.tsx` (sums session KV `usage` fields grouped by `projectId`), but the spec calls for a proper cost grouping view that lets users see cumulative spend per project over time. This ADR captures what's still needed beyond the existing component.

## Motivation

Deferred from ADR-0052 (spec-implementation gap remediation). The audit identified "G5: No per-project cost grouping view" as a missing feature. Users need visibility into per-project costs to manage budgets and identify expensive workloads.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Data source | Session KV `usage` fields (already available) | No new backend work needed — aggregation is client-side |
| View location | TODO: decide — dashboard tab vs settings page vs dedicated route |
| Time grouping | TODO: decide — daily/weekly/monthly breakdown vs cumulative only |
| Historical data | TODO: decide — KV only has current sessions; lifecycle events in MCLAUDE_LIFECYCLE stream have historical cost data (30d retention) |

## Open questions

- Is the existing `TokenUsage.tsx` aggregation sufficient, or does the user need a dedicated page/view?
- Should cost history come from the MCLAUDE_LIFECYCLE JetStream (30d retention) for historical aggregation beyond live sessions?
- What cost breakdown is needed — just tokens + USD, or also by model/provider?
- Should there be export functionality (CSV)?

## Component Changes

### mclaude-web
- New cost grouping view component with per-project breakdown
- Possibly a JetStream consumer for historical cost data from MCLAUDE_LIFECYCLE

## Scope

### In scope
- Per-project cost aggregation view in SPA
- Current session cost data (from KV)

### Possibly in scope (pending answers)
- Historical cost data from lifecycle events
- Time-based grouping
- Export

## Integration Test Cases

| Test case | What it verifies | Components exercised |
|-----------|------------------|----------------------|
| Cost view shows per-project totals | Navigate to cost view, verify projects listed with cumulative token/cost values | mclaude-web |

## Implementation Plan

| Component | Est. lines | Notes |
|-----------|------------|-------|
| mclaude-web | ~200-400 | New view component + optional JetStream consumer |
