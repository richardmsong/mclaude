# ADR: Fix User Slug Derivation and K8s Namespace Naming

**Status**: accepted
**Status history**:
- 2026-05-01: accepted

## Overview

Two bugs in identity naming: (1) `computeUserSlug()` drops the email domain, producing `dev` instead of `dev-mclaude-local` for `dev@mclaude.local` — violating ADR-0024's collision-prevention requirement, and (2) K8s namespaces use the user UUID (`mclaude-0ade44ea-...`) instead of the user slug (`mclaude-dev-mclaude-local`), making cluster inspection unnecessarily opaque.

## Motivation

ADR-0024 specifies user slugs as `{slugify(local-part)}-{domain}` to prevent collisions between users with the same local-part on different domains (e.g., `richard@rbc.com` vs `richard@gmail.com`). The current implementation only uses the local-part, so both would produce `richard` — a unique-index violation on the second insert.

K8s namespace naming was deferred by ADR-0024 ("a separate future ADR can address K8s-resource naming"), but there's no technical reason to defer — user slugs are already DNS-1123 safe (lowercase alphanumeric + hyphens, max 63 chars). Using `mclaude-{uslug}` instead of `mclaude-{userId}` makes `kubectl` output human-readable.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| User slug derivation | `slugify(full-email)`: lowercase, replace all non-`[a-z0-9]` runs with `-`, trim leading/trailing `-`, truncate to 63 chars. `dev@mclaude.local` → `dev-mclaude-local`. `richard.song@gmail.com` → `richard-song-gmail-com`. | Full domain included to prevent collisions. The `@` and `.` become `-`. Simpler than ADR-0024's `split('.')[0]` rule and strictly more collision-resistant. |
| SQL backfill | `UPDATE users SET slug = lower(regexp_replace(email, '[^a-zA-Z0-9]+', '-', 'g'))` trimmed of leading/trailing `-`. Idempotent — runs on every migration. | Existing users get correct slugs. Dev seed user `dev@mclaude.local` becomes `dev-mclaude-local`. |
| K8s namespace format | `mclaude-{userSlug}` instead of `mclaude-{userId}` | Human-readable. `mclaude-dev-mclaude-local` instead of `mclaude-0ade44ea-9cef-4c29-af96-92c0b0dd19a5`. |
| Namespace migration | Controller checks for old UUID-named namespace; if it exists and new slug-named namespace does not, it creates the new namespace and lets the old one be cleaned up manually. No automatic PVC migration — fresh PVCs in the new namespace. | Clean cut-over. Old namespace can be deleted after verification. |
| MCProject CR naming | `{uslug}-{pslug}` instead of `dev-default` (already correct). No change needed. | Already uses slugs. |
| PVC naming | Keep `project-{projectId}` and `nix-{projectId}` (UUIDs). | PVC names are internal, not user-facing. Slug-based PVC names can be a follow-up. |

## Impact

**Specs updated:**
- `docs/mclaude-control-plane/spec-control-plane.md` — user slug derivation algorithm
- `docs/mclaude-controller/spec-controller.md` — namespace naming format

**Components implementing the change:**
- `mclaude-control-plane` — `computeUserSlug()` + SQL backfill migration
- `mclaude-controller-k8s` — `reconcileNamespace()` uses `mcp.Spec.UserSlug` instead of `mcp.Spec.UserID`

## Scope

**In v1:**
- Fix `computeUserSlug()` to include full domain
- Fix SQL backfill to match
- Change namespace format to `mclaude-{uslug}`
- Dev seed produces correct slug

**Deferred:**
- PVC naming migration (UUID → slug)
- Deployment naming migration
- Automatic old-namespace cleanup

## Integration Test Cases

| Test case | What it verifies | Components exercised |
|-----------|------------------|----------------------|
| User slug includes domain | `computeUserSlug("dev@mclaude.local")` returns `dev-mclaude-local` | mclaude-control-plane |
| User slug collision resistance | `computeUserSlug("richard@rbc.com")` ≠ `computeUserSlug("richard@gmail.com")` | mclaude-control-plane |
| Namespace uses slug | Controller creates namespace `mclaude-dev-mclaude-local` (not UUID) | mclaude-controller-k8s |
| Session agent receives correct USER_SLUG | Pod env var `USER_SLUG=dev-mclaude-local` | mclaude-controller-k8s |
