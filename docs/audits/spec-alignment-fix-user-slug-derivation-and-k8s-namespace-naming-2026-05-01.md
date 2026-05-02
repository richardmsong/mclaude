## Run: 2026-05-01T00:00:00Z (re-verification)

**ADR:** `docs/adr-0062-fix-user-slug-derivation-and-k8s-namespace-naming.md`
**Status:** accepted
**Specs evaluated:** spec-control-plane.md, spec-controller.md, spec-state-schema.md, spec-session-agent.md, spec-common.md

### Phase 1 — ADR → Spec (forward pass)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions:1 | User slug derivation: `slugify(full-email)`: lowercase, replace all non-`[a-z0-9]` runs with `-`, trim leading/trailing `-`, truncate to 63 chars. `dev@mclaude.local` → `dev-mclaude-local`. | spec-control-plane.md §Postgres — "computeUserSlug(email) slugifies the full email address — lowercase, replace all non-[a-z0-9] runs with -, trim leading/trailing -, truncate to 63 chars. Examples: dev@mclaude.local → dev-mclaude-local, richard.song@gmail.com → richard-song-gmail-com." | REFLECTED | — | Spec explicitly cites ADR-0024 and ADR-0062. |
| Decisions:1 | User slug derivation (same decision) | spec-common.md §DeriveUserSlug — "Produces slugify(full-email) — lowercase, replace all non-[a-z0-9] runs with -, trim leading/trailing -, truncate to 63 chars. Examples: dev@mclaude.local → dev-mclaude-local, richard.song@gmail.com → richard-song-gmail-com. The full domain is included to prevent collisions between users on different domains (ADR-0062)." | REFLECTED | — | Updated since prior audit. |
| Decisions:2 | SQL backfill: idempotent migration applying same algorithm to existing rows. | spec-control-plane.md §Postgres — "SQL backfill applies the same algorithm to existing rows." | REFLECTED | — | |
| Decisions:3 | K8s namespace format: `mclaude-{userSlug}` instead of `mclaude-{userId}` | spec-controller.md §Kubernetes resources — "Per-user namespace `mclaude-{userSlug}` (ADR-0062)" | REFLECTED | — | Spec explicitly cites ADR-0062. |
| Decisions:3 | K8s namespace format (same decision) | spec-state-schema.md §Kubernetes Resources — "Namespace: `mclaude-{userSlug}`", Secret/ConfigMap/PVC/Deployment/RBAC headers all use `(in mclaude-{userSlug})` | REFLECTED | — | All 7 namespace references in spec-state-schema.md use `{userSlug}`. |
| Decisions:3 | K8s namespace format (same decision) | spec-session-agent.md §Standalone Mode (K8s) — "Runs as a single-container Deployment per project in the `mclaude-{userSlug}` namespace." | REFLECTED | — | |
| Decisions:4 | Namespace migration: controller creates new slug-named namespace; old UUID namespace left for manual cleanup. No automatic PVC migration. | spec-controller.md §Reconciler loop step 2 — "ADR-0062: namespace is derived from the user slug, not the user UUID. On migration, the controller creates the new slug-named namespace if it does not exist; the old UUID-named namespace (mclaude-{userId}) is left for manual cleanup. No automatic PVC migration — fresh PVCs are created in the new namespace." | REFLECTED | — | Verbatim match of ADR migration strategy. |
| Decisions:5 | MCProject CR naming: `{uslug}-{pslug}` — no change needed. | spec-state-schema.md §CRD MCProject — "Name: `{userSlug}-{projectSlug}`" | REFLECTED | — | Already correct. |
| Decisions:6 | PVC naming: keep `project-{projectId}` and `nix-{projectId}` (UUIDs). | spec-state-schema.md §PVC — "PVC: `project-{projectId}`" and "PVC: `nix-{projectId}`" | REFLECTED | — | No change required; spec is consistent. |
| Integration:4 | Session agent receives correct USER_SLUG env var. | spec-controller.md §Reconciler loop step 7 — "Pod env vars include USER_ID, USER_SLUG, HOST_SLUG, PROJECT_ID, PROJECT_SLUG" | REFLECTED | — | |

### Phase 0b — Cross-spec consistency check

**Shared concept: User slug derivation algorithm**

| Spec file | Line | Current text | Expected (per ADR-0062) | Verdict | Direction | Notes |
|-----------|------|-------------|------------------------|---------|-----------|-------|
| spec-control-plane.md | 188 | "slugifies the full email address" | Full-email slugification | OK | — | |
| spec-common.md | 44 | "slugify(full-email)" | Full-email slugification | OK | — | |
| spec-state-schema.md | 18 | `lower(regexp_replace(split_part(email, '@', 1), '[^a-zA-Z0-9]+', '-', 'g'))` (email local-part only, slugified) | Full-email slugification per ADR-0062 | GAP | SPEC→FIX | The `users.slug` column description still documents the old local-part-only algorithm. A developer implementing from this table spec would produce incorrect slugs. |

**Shared concept: K8s namespace naming (`mclaude-{userSlug}`)**

All specs now consistently use `mclaude-{userSlug}`. No cross-spec inconsistencies found.

### Summary

- Reflected: 10
- Gap: 1
- Partial: 0

---

GAP [SPEC→FIX]: ADR-0062 changes user slug derivation to `slugify(full-email)` but spec-state-schema.md `users` table (line 18) still describes the old algorithm: `lower(regexp_replace(split_part(email, '@', 1), '[^a-zA-Z0-9]+', '-', 'g'))` (email local-part only). The description should be updated to: "Derived at user creation via `slugify(full-email)` — lowercase, replace all non-`[a-z0-9]` runs with `-`, trim leading/trailing `-`, truncate to 63 chars (ADR-0062). Full domain included to prevent collisions." with examples `dev@mclaude.local` → `dev-mclaude-local`.

---

## Run: 2026-05-01T01:00:00Z (final verification)

**ADR:** `docs/adr-0062-fix-user-slug-derivation-and-k8s-namespace-naming.md`
**Status:** accepted
**Specs evaluated:** spec-control-plane.md, spec-controller.md, spec-state-schema.md, spec-session-agent.md, spec-common.md

### Prior gap status

The GAP from the prior run (spec-state-schema.md `users.slug` column describing the old local-part-only algorithm) has been **resolved**. The current text reads: `lower(regexp_replace(email, '[^a-zA-Z0-9]+', '-', 'g'))` — applied to the full email, not `split_part(email, '@', 1)`. Examples match ADR-0062.

### Phase 1 — ADR → Spec (forward pass)

| ADR (section) | ADR text | Spec location | Verdict | Direction | Notes |
|---------------|----------|---------------|---------|-----------|-------|
| Decisions:1 | User slug derivation: `slugify(full-email)` — lowercase, replace all non-`[a-z0-9]` runs with `-`, trim leading/trailing `-`, truncate to 63 chars. `dev@mclaude.local` → `dev-mclaude-local`. `richard.song@gmail.com` → `richard-song-gmail-com`. | spec-control-plane.md §Postgres — "computeUserSlug(email) slugifies the full email address — lowercase, replace all non-`[a-z0-9]` runs with `-`, trim leading/trailing `-`, truncate to 63 chars." with matching examples. Cites ADR-0024, ADR-0062. | REFLECTED | — | |
| Decisions:1 | (same) | spec-common.md §DeriveUserSlug — "Produces `slugify(full-email)` — lowercase, replace all non-`[a-z0-9]` runs with `-`, trim leading/trailing `-`, truncate to 63 chars." with matching examples. Cites ADR-0062. | REFLECTED | — | |
| Decisions:1 | (same) | spec-state-schema.md §`users` table, `slug` column — "Derived at user creation by slugifying the full email: `lower(regexp_replace(email, '[^a-zA-Z0-9]+', '-', 'g'))` trimmed of leading/trailing `-`, truncated to 63 chars (ADR-0062)." with matching examples. | REFLECTED | — | Prior gap now fixed. |
| Decisions:2 | SQL backfill: idempotent migration applying same algorithm to existing rows. | spec-control-plane.md §Postgres — "SQL backfill applies the same algorithm to existing rows." | REFLECTED | — | |
| Decisions:3 | K8s namespace format: `mclaude-{userSlug}` instead of `mclaude-{userId}`. | spec-controller.md §Kubernetes resources — "Per-user namespace `mclaude-{userSlug}` (ADR-0062)". | REFLECTED | — | |
| Decisions:3 | (same) | spec-state-schema.md §Kubernetes Resources — "Namespace: `mclaude-{userSlug}`"; all 7 sub-resource headers use `(in mclaude-{userSlug})`. | REFLECTED | — | |
| Decisions:3 | (same) | spec-session-agent.md §Standalone Mode — "Runs as a single-container Deployment per project in the `mclaude-{userSlug}` namespace." | REFLECTED | — | |
| Decisions:4 | Namespace migration: controller creates new slug-named namespace; old UUID namespace left for manual cleanup. No automatic PVC migration. | spec-controller.md §Reconciler loop step 2 — verbatim: "ADR-0062: namespace is derived from the user slug, not the user UUID. On migration, the controller creates the new slug-named namespace if it does not exist; the old UUID-named namespace (`mclaude-{userId}`) is left for manual cleanup. No automatic PVC migration — fresh PVCs are created in the new namespace." | REFLECTED | — | |
| Decisions:5 | MCProject CR naming: `{uslug}-{pslug}` — no change needed. | spec-state-schema.md §CRD MCProject — "Name: `{userSlug}-{projectSlug}`" | REFLECTED | — | Already correct. |
| Decisions:6 | PVC naming: keep `project-{projectId}` and `nix-{projectId}` (UUIDs). | spec-state-schema.md §PVC — "PVC: `project-{projectId}`" and "PVC: `nix-{projectId}`" | REFLECTED | — | No change required. |
| Integration:4 | Session agent receives correct USER_SLUG env var. | spec-controller.md §Reconciler loop step 7 — "Pod env vars include `USER_ID`, `USER_SLUG`, `HOST_SLUG`, `PROJECT_ID`, `PROJECT_SLUG`" | REFLECTED | — | |

### Phase 0b — Cross-spec consistency check

**Shared concept: User slug derivation algorithm**

| Spec file | Text | Verdict |
|-----------|------|---------|
| spec-control-plane.md | `computeUserSlug(email)` slugifies the full email address | OK |
| spec-common.md | `DeriveUserSlug` produces `slugify(full-email)` | OK |
| spec-state-schema.md | `lower(regexp_replace(email, '[^a-zA-Z0-9]+', '-', 'g'))` — full email | OK |

All three specs describe the same algorithm applied to the full email. Consistent.

**Shared concept: K8s namespace naming (`mclaude-{userSlug}`)**

| Spec file | Text | Verdict |
|-----------|------|---------|
| spec-controller.md | `mclaude-{userSlug}` (ADR-0062) | OK |
| spec-state-schema.md | `mclaude-{userSlug}` (7 references) | OK |
| spec-session-agent.md | `mclaude-{userSlug}` namespace | OK |

All consistent. No residual `mclaude-{userId}` references in any spec (the migration note in spec-controller.md correctly mentions the old format in a backward-compat context only).

### Summary

- Reflected: 11
- Gap: 0
- Partial: 0

**CLEAN — 11 ADR decisions reflected in specs, 0 gaps.**
