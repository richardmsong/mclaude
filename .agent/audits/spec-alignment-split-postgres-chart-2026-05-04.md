## Run: 2026-05-04T00:00:00Z

ADR: docs/adr-0094-split-postgres-chart.md  
Status: draft  
Specs evaluated:
- docs/charts-mclaude/spec-helm.md
- docs/mclaude-control-plane/spec-control-plane.md
- docs/spec-state-schema.md

| # | ADR (section/line) | ADR text | Spec location | Verdict | Direction | Notes |
|---|-------------------|----------|---------------|---------|-----------|-------|
| 1 | Decisions table — Packaging | "New top-level `charts/mclaude-postgres/` chart with its own Helm release" | spec-helm.md lines 1–11 (intro table + single-cluster install order) | REFLECTED | — | Spec intro table lists mclaude-postgres as its own chart; install order section shows it installed before mclaude-cp. |
| 2 | Decisions table — DSN handoff | "`database.host` (string) + `database.port` (int, default 5432) + `database.user` (default `mclaude`) + `database.name` (default `mclaude`) + `database.passwordSecretRef` (Secret name in cp namespace, key `postgres-password`)" | spec-helm.md lines 108–113 (Configuration knobs) | REFLECTED | — | All five knobs listed with correct defaults and descriptions. |
| 3 | Decisions table — DSN handoff | "`database.sslmode` (default `disable`; managed-DB users set to `require`)" | spec-helm.md line 113 | REFLECTED | — | database.sslmode knob documented. |
| 4 | Decisions table — Bootstrap admin location | "Stay in `init-keys` pre-install hook" | spec-helm.md line 29; spec-control-plane.md line 220 | REFLECTED | — | Both specs describe bootstrap admin as part of the init-keys pre-install Job. |
| 5 | Decisions table — Bootstrap admin idempotence | "UPSERT on `users.email`: insert if not exists; update `is_admin=true` and (if `BOOTSTRAP_ADMIN_PASSWORD` non-empty) refresh `password_hash`" | spec-control-plane.md lines 42–43, 238 | REFLECTED | — | Spec documents UPSERT SQL with exact semantics including password preserve/rotate logic. |
| 6 | Decisions table — mclaude-postgres image | "`postgres:16-alpine`" | spec-helm.md line 182 (`image.tag: 16-alpine`) and line 174 (StatefulSet Notes) | REFLECTED | — | Image and tag documented. |
| 7 | Decisions table — mclaude-postgres persistence | "Keep current defaults: `enabled: true`, `size: 20Gi`, `storageClass: ""` (cluster default). AKS overrides via values-aks.yaml." | spec-helm.md lines 185–187 | REFLECTED | — | All three knobs and AKS note documented. |
| 8 | Decisions table — Existing PVC migration | "Clean-slate cutover. Operator runbook: `helm uninstall mclaude-cp` + `kubectl delete pvc data-mclaude-cp-postgres-0` + install both new charts fresh." | spec-helm.md lines 200–211 (Cutover section) | REFLECTED | — | Runbook and rationale documented verbatim. |
| 9 | Decisions table — Removed knobs | "`postgres.enabled`, `postgres.persistence.size`, and all StatefulSet/Service templates for Postgres" removed from mclaude-cp | spec-helm.md lines 46–49 (PostgreSQL section states Postgres is NOT part of mclaude-cp) | REFLECTED | — | Spec states Postgres is removed from mclaude-cp and replaced with database.* contract. |
| 10 | Decisions table — Postgres password Secret | "`existingSecret` is **required** (chart fails fast if empty). … No chart-generated random password." | spec-helm.md line 188 | REFLECTED | — | existingSecret marked Required; "no chart-generated password fallback" documented. |
| 11 | Component Changes — mclaude-postgres StatefulSet resources | StatefulSet `{release}-postgres` + Service `{release}-postgres` (ClusterIP, 5432) + Service `{release}-postgres-headless` | spec-helm.md lines 173–177 (Resources table) | REFLECTED | — | All three resources listed. |
| 12 | Component Changes — mclaude-postgres VolumeClaimTemplate `data` | "VolumeClaimTemplate `data` (RWO, configurable size + storageClass)" | spec-helm.md line 174 (StatefulSet Notes: "VolumeClaimTemplate `data` (RWO)") | REFLECTED | — | Documented. |
| 13 | Component Changes — mclaude-postgres replicas knob | "`replicas`: 1. v1 does not support HA replication." | spec-helm.md line 184 | REFLECTED | — | Documented with HA note. |
| 14 | Component Changes — mclaude-cp env block: `POSTGRES_PASSWORD` via secretKeyRef + DATABASE_URL interpolation | "POSTGRES_PASSWORD valueFrom secretKeyRef … DATABASE_URL composed at pod runtime via env-var interpolation" | spec-helm.md line 55 (Deployment Notes: "Postgres env contract (per ADR-0094): `POSTGRES_PASSWORD` is loaded via `valueFrom.secretKeyRef` … `DATABASE_URL` is composed at pod runtime via env-var interpolation …") | REFLECTED | — | Round 1 PARTIAL fixed. Full env composition pattern now in spec-helm.md Deployment Notes. |
| 15 | Component Changes — mclaude-cp `database.sslmode` default `disable` | "`database.sslmode` (default `disable`)" | spec-helm.md line 113 | REFLECTED | — | Documented. |
| 16 | Component Changes — dbmate init container must use new `database.*` values (not legacy hardcoded) | "Both must be updated to consume the new `database.*` values exactly like the main control-plane container — same `POSTGRES_PASSWORD` env-from-secret-ref against `database.passwordSecretRef`, same `DATABASE_URL` interpolation … No hardcoded Postgres Secret name or hostname." | spec-helm.md line 55 ("uses the same `database.*` env contract as the main container (no hardcoded Postgres Secret name or DSN)") + line 124 (`controlPlane.migrations.enabled` knob: "The init container inherits the same `database.*` env contract … No hardcoded Postgres Secret name or hostname.") | REFLECTED | — | Round 1 GAP fixed. Both Deployment Notes and migrations knob now explicitly state the dbmate init container uses the same database.* env contract with no legacy hardcoding. |
| 17 | Component Changes — mclaude-control-plane: decouple key-gen early-return from admin bootstrap | "Restructure so the early-return is scoped to the key-generation path only — admin bootstrap runs unconditionally when `BOOTSTRAP_ADMIN_EMAIL` is set, regardless of whether keys were just generated or already existed." | spec-control-plane.md lines 220–222 ("This step's early-return is scoped — it does NOT short-circuit step 2. … Runs **unconditionally on every `helm upgrade`**, regardless of whether keys were just generated or already existed — the only gate is `BOOTSTRAP_ADMIN_EMAIL`.") | REFLECTED | — | Round 1 PARTIAL fixed. Spec now uses explicit numbered steps, states the early-return is scoped, and explicitly calls out unconditional execution on every helm upgrade. |
| 18 | Component Changes — mclaude-control-plane: replace conditional INSERT with UPSERT | "Replace with the SQL above (`INSERT ... ON CONFLICT (email) DO UPDATE`), which … preserves `password_hash` when `$2` is empty." | spec-control-plane.md lines 42–43, 222, 242 | REFLECTED | — | Spec documents the full UPSERT SQL and semantics. |
| 19 | Component Changes — CI workflow: two helm calls | "The `Deploy control-plane` step splits into two helm calls: `helm upgrade --install mclaude-postgres` then `helm upgrade --install mclaude-cp`" | spec-helm.md line 11 (install order: mclaude-postgres → mclaude-cp → mclaude-worker) | REFLECTED | — | CI workflow is infrastructure, not spec scope. Install ordering decision is reflected. |
| 20 | User Flow — `BOOTSTRAP_ADMIN_EMAIL` empty → skip bootstrap | "`BOOTSTRAP_ADMIN_EMAIL` empty → skip the bootstrap entirely." | spec-control-plane.md line 42 | REFLECTED | — | Documented via the "No" Required column and empty default. |
| 21 | Error Handling — `database.passwordSecretRef` Secret missing | "init-keys and CP pod both fail to start (env-from references a nonexistent Secret). Helm install fails fast." | spec-helm.md line 55 ("If the Secret named by `database.passwordSecretRef` does not exist, the pod stays in `CreateContainerConfigError` … helm install with `--wait` fails fast.") + spec-control-plane.md line 224 ("if the Secret named by `database.passwordSecretRef` is missing, the Job's pod stays in `CreateContainerConfigError`") | REFLECTED | — | Round 1 GAP fixed. Both specs now explicitly state the CreateContainerConfigError failure mode and helm install fast-failure. |
| 22 | Data Model — UNIQUE (email) on users table | "The `users` table gains an explicit `UNIQUE (email)` constraint … Spec already documents `email TEXT UNIQUE NOT NULL` so this is a no-op." | spec-state-schema.md line 19 | REFLECTED | — | spec-state-schema.md documents `email | TEXT | UNIQUE NOT NULL`. |

### Summary

- Reflected: 22
- Gap: 0
- Partial: 0

---

## Run: 2026-05-04T(round-2)

Round 2 verification: all 4 round-1 findings (rows 14, 16, 17, 21) confirmed fixed. No new gaps found.

**Result: CLEAN — 22 ADR decisions reflected in specs, 0 gaps, 0 partials.**
