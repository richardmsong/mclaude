# ADR: Split Postgres into a standalone chart

**Status**: accepted
**Status history**:
- 2026-05-05: draft
- 2026-05-05: accepted — paired with charts-mclaude/spec-helm.md, mclaude-control-plane/spec-control-plane.md

## Overview

Postgres moves from `charts/mclaude-cp` (subordinate StatefulSet) into a new top-level `charts/mclaude-postgres` chart with its own Helm release. `mclaude-cp` becomes a pure application chart that consumes a database via a `database.host` + `database.passwordSecretRef` contract. Operators install the two charts independently; managed-DB users skip `mclaude-postgres` entirely and point `mclaude-cp` at their RDS/Cloud SQL endpoint with the same Secret-ref shape.

This also fixes the `init-keys` chicken-and-egg: by the time `mclaude-cp` is installed, Postgres is already up and reachable, so the pre-install hook can complete its admin-bootstrap step in one shot. The bootstrap is also made idempotent (UPSERT on email; safe to re-run on every helm upgrade).

## Motivation

Two issues with the current shape:

1. **Bootstrap chicken-and-egg.** `init-keys` is a Helm pre-install hook in `mclaude-cp`. It generates NKeys *and* tries to create the bootstrap admin row in Postgres — but pre-install hooks run *before* any chart resource is applied, so Postgres doesn't exist yet. The Job fatals on `lookup mclaude-cp-postgres ... no such host`. Earlier deploys masked this because Postgres pods/PVCs survived prior helm uninstalls; a clean wipe surfaces the bug. Reproduced today on run 25375488901.

2. **Managed-DB swap requires forking the chart.** Today, switching to AKS-managed Postgres / RDS / Cloud SQL means deleting templates from `mclaude-cp` (Postgres StatefulSet, Service, PVC) and rewriting the env-var path. There's no clean op to "skip Postgres in this environment."

A separate chart solves both: `mclaude-cp` no longer has Postgres in its install ordering, and "use a managed DB" becomes "don't install `mclaude-postgres`."

The bootstrap admin step is also made idempotent: it currently exits silently (or fatals) depending on whether the `operator-keys` Secret exists, conflating Secret state with admin-user state. With Postgres always reachable at install time and an UPSERT on `users.email`, the step becomes safe to re-run on every helm upgrade.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Packaging | New top-level `charts/mclaude-postgres/` chart with its own Helm release | Cleanest managed-DB swap (don't install the chart). User-confirmed framing question 1. |
| DSN handoff | `database.host` (string) + `database.port` (int, default 5432) + `database.user` (default `mclaude`) + `database.name` (default `mclaude`) + `database.passwordSecretRef` (Secret name in cp namespace, key `postgres-password`) | Same Secret shape CI already uses (`mclaude-postgres` Secret with `postgres-password` key). CP composes the DSN at runtime via env-from. Managed-DB users create their own Secret with the same shape. User-confirmed framing question 2. |
| Bootstrap admin location | Stay in `init-keys` pre-install hook | Smallest change; one Job, one log to investigate. With Postgres now installed first as a separate release, the pre-install hook of `mclaude-cp` always finds Postgres reachable. Failure surfaces immediately as a helm install failure, not a CP crashloop. |
| Bootstrap admin idempotence | UPSERT on `users.email`: insert if not exists; update `is_admin=true` and (if `BOOTSTRAP_ADMIN_PASSWORD` non-empty) refresh `password_hash` | Spec already declares "idempotent — if the user already exists, promotes them to admin" — implementation needs to match. Each helm upgrade attempts admin creation; second+ runs are no-ops. |
| `mclaude-postgres` chart image | `postgres:16-alpine` (track the same image used today) | Drop-in replacement for the current StatefulSet template |
| `mclaude-postgres` chart persistence | Keep current defaults: `enabled: true`, `size: 20Gi`, `storageClass: ""` (cluster default). AKS overrides via values-aks.yaml. | Drop-in replacement; no need to redesign defaults. |
| Existing PVC migration | Clean-slate cutover. Operator runbook: `helm uninstall mclaude-cp` + `kubectl delete pvc data-mclaude-cp-postgres-0` + install both new charts fresh. | No live prod deployments today. Auto-adoption would require chart machinery that pays off zero migrations. |
| Removed knobs in `mclaude-cp` values | `postgres.enabled`, `postgres.persistence.size`, and all StatefulSet/Service templates for Postgres | These move to `mclaude-postgres` chart. |
| Postgres password Secret | `existingSecret` is **required** (chart fails fast if empty). Operator/CI pre-creates the Secret with the `postgres-password` key. No chart-generated random password. | Single source of truth: matches CI's Bitwarden flow. Avoids the helm-managed-Secret + outliving-PVC footgun where `helm uninstall` deletes the password but the PVC keeps the data, stranding it. Local dev runs `kubectl create secret generic mclaude-postgres --from-literal=postgres-password=$(openssl rand -base64 24)` once. |

## User Flow

### Self-hosted (in-cluster Postgres) install

```bash
helm install mclaude-postgres charts/mclaude-postgres -n mclaude-system \
  --set persistence.size=20Gi \
  --set existingSecret=mclaude-postgres   # Secret with postgres-password, pre-created (Bitwarden)

# After Postgres is Ready:
helm install mclaude-cp charts/mclaude-cp -n mclaude-system \
  --set database.host=mclaude-postgres.mclaude-system.svc \
  --set database.passwordSecretRef=mclaude-postgres
  # ... other values ...
```

### Managed-DB install (e.g., RDS)

```bash
# Operator creates the Secret with the postgres-password key:
kubectl create secret generic my-rds-creds \
  --from-literal=postgres-password=<rds master password> \
  -n mclaude-system

helm install mclaude-cp charts/mclaude-cp -n mclaude-system \
  --set database.host=foo.us-east-1.rds.amazonaws.com \
  --set database.passwordSecretRef=my-rds-creds \
  --set database.user=mclaude \
  --set database.name=mclaude
```

No `mclaude-postgres` release. CP picks up the credentials and connects.

### Bootstrap admin

The `init-keys` Job (pre-install hook) executes the following SQL after Postgres is reachable (which it always is now, because `mclaude-postgres` is installed first):

```sql
INSERT INTO users (email, is_admin, password_hash, ...)
VALUES ($1, true, bcrypt($2, 10), ...)
ON CONFLICT (email) DO UPDATE
  SET is_admin = true,
      password_hash = CASE WHEN $2 = '' THEN users.password_hash ELSE bcrypt($2, 10) END;
```

`BOOTSTRAP_ADMIN_PASSWORD` empty → preserve existing hash. `BOOTSTRAP_ADMIN_EMAIL` empty → skip the bootstrap entirely.

## Component Changes

### `charts/mclaude-postgres` (NEW)

Top-level chart. Installs:
- `StatefulSet` `{fullname}` (single replica, runs as UID 999, `postgres:16-alpine`)
- `Service` `{fullname}` (ClusterIP, port 5432)
- `Service` `{fullname}-headless` (headless, for StatefulSet DNS)
- VolumeClaimTemplate `data` (RWO, configurable size + storageClass)

Values:

| Key | Default | Description |
|---|---|---|
| `image.repository` | `postgres` | Postgres image. |
| `image.tag` | `16-alpine` | Pinned tag. |
| `persistence.enabled` | `true` | When false, uses `emptyDir` (dev/CI only). |
| `persistence.size` | `20Gi` | PVC size. |
| `persistence.storageClass` | `""` | Empty = cluster default. AKS overrides via `values-aks.yaml`. |
| `existingSecret` | `""` | Name of an existing Secret with key `postgres-password`. **Required** in production — CI's Bitwarden flow pre-creates `mclaude-postgres` Secret. |
| `replicas` | `1` | Single replica. v1 does not support HA replication; managed-DB is the prod HA path. |

The chart **does not generate** the postgres password. The pre-existing CI flow (Bitwarden fetch → `kubectl create secret`) is the canonical creator. If `existingSecret` is empty, helm install fails fast.

### `charts/mclaude-cp` (changes)

**Removed:**
- `templates/postgres-statefulset.yaml`
- `templates/postgres-services.yaml`
- All `postgres.*` values keys

**Added:**
- `database.host` (required string)
- `database.port` (default 5432)
- `database.user` (default `mclaude`)
- `database.name` (default `mclaude`)
- `database.passwordSecretRef` (required string — Secret name in cp namespace with key `postgres-password`)
- `database.sslmode` (default `disable`; managed-DB users set to `require`)

**Changed:**
- Control-plane Deployment env block:
  ```yaml
  - name: POSTGRES_PASSWORD
    valueFrom:
      secretKeyRef:
        name: {{ .Values.database.passwordSecretRef }}
        key: postgres-password
  - name: DATABASE_URL
    value: "postgres://{{ .Values.database.user }}:$(POSTGRES_PASSWORD)@{{ .Values.database.host }}:{{ .Values.database.port }}/{{ .Values.database.name }}?sslmode={{ .Values.database.sslmode }}"
  ```
- `templates/init-keys-job.yaml`: env block uses the new `database.*` shape (same Secret ref, same DSN composition).

### `mclaude-control-plane`

The `init-keys` subcommand requires a control-flow restructure to make admin bootstrap run on every helm upgrade (not just when the `operator-keys` Secret is being created):

1. **Decouple key generation from admin bootstrap.** The current `runInitKeys()` returns early if the `operator-keys` Secret already exists, skipping `bootstrapAdminUser()` entirely on every upgrade. Restructure so the early-return is scoped to the key-generation path only — admin bootstrap runs unconditionally when `BOOTSTRAP_ADMIN_EMAIL` is set, regardless of whether keys were just generated or already existed.

2. **Replace the conditional INSERT with UPSERT.** The current `bootstrapAdminUser()` only updates `password_hash` when the existing hash is empty (`existing.PasswordHash == ""`), so password rotation is impossible for users that already have a hash. Replace with the SQL above (`INSERT ... ON CONFLICT (email) DO UPDATE`), which:
   - Inserts the user if they don't exist (with `is_admin=true`).
   - Updates `is_admin=true` and rotates `password_hash` if `$2` (the new password) is non-empty.
   - Preserves `password_hash` when `$2` is empty.

3. **DSN composition is value-only.** The binary already reads `DATABASE_URL`; the chart change rewrites how that env var is composed (from new `database.*` values). No Go change for DSN reading.

### `charts/mclaude-cp/templates/control-plane-deployment.yaml` — `dbmate` init container

The optional `dbmate` migrations init-container (gated by `controlPlane.migrations.enabled`) currently hardcodes the legacy `mclaude-postgres` Secret name and the in-chart postgres DSN (`{release}-postgres.{namespace}.svc:5432`). Both must be updated to consume the new `database.*` values exactly like the main control-plane container — same `POSTGRES_PASSWORD` env-from-secret-ref against `database.passwordSecretRef`, same `DATABASE_URL` interpolation against `database.host`/`database.port`/`database.user`/`database.name`/`database.sslmode`. Without this fix, `controlPlane.migrations.enabled=true` deployments break.

### `.github/workflows/deploy-main.yml`

The `Deploy control-plane` step splits into two helm calls:

```yaml
- name: Helm deploy mclaude-postgres
  run: |
    helm upgrade --install mclaude-postgres ./charts/mclaude-postgres \
      --set existingSecret=mclaude-postgres \
      --namespace mclaude-system \
      --wait --timeout 5m

- name: Helm deploy mclaude-cp
  run: |
    helm upgrade --install mclaude-cp ./charts/mclaude-cp \
      -f ./charts/mclaude-cp/values-k3d-ghcr.yaml \
      --set controlPlane.image.tag="${IMAGE_TAG}" \
      --set spa.image.tag="${IMAGE_TAG}" \
      --set database.host=mclaude-postgres.mclaude-system.svc \
      --set database.passwordSecretRef=mclaude-postgres \
      --set "controlPlane.bootstrapAdminEmail=${BOOTSTRAP_ADMIN_EMAIL}" \
      --set "controlPlane.bootstrapAdminPassword=${BOOTSTRAP_ADMIN_PASSWORD}" \
      --set "ingress.externalDnsTarget=${TS_IP}" \
      --namespace mclaude-system \
      --force-conflicts \
      --wait --timeout 5m
```

The Bitwarden fetch step (`mclaude-postgres` Secret with `postgres-password`) is unchanged and runs before either helm command.

## Data Model

No new tables, KV entries, or NATS subjects.

The `users` table gains an explicit `UNIQUE (email)` constraint if it doesn't already have one (UPSERT requires it). Spec already documents `email TEXT UNIQUE NOT NULL` so this is a no-op.

## Error Handling

- **`mclaude-postgres` not installed before `mclaude-cp`** in self-hosted setup: `helm install mclaude-cp` fails with `init-keys` Job fatal on Postgres connection refused (same failure as today, but now an operator error rather than a chart bug — they ran the wrong order).
- **`database.passwordSecretRef` Secret missing**: `init-keys` and CP pod both fail to start (env-from references a nonexistent Secret). Helm install fails fast.
- **`database.host` unreachable** (managed-DB endpoint typo): `init-keys` fatal with the same DNS / connection error message — fast feedback.
- **Bootstrap admin UPSERT conflict on a non-`email` field**: shouldn't happen — UPSERT keys on email which is the unique column.
- **Existing deployments upgrading from in-chart Postgres**: TODO (open question on PVC migration).

## Security

- Postgres password remains in a K8s Secret. Same security posture as today.
- DSN constructed at pod runtime via env-from interpolation — password never in `values.yaml`, never in helm command line, never in helm history.
- Managed-DB SSL: `database.sslmode=require` (or `verify-full` with custom CA) is the operator's responsibility to set per environment.

## Impact

- `docs/charts-mclaude/spec-helm.md`: split `## Chart: mclaude-cp` to remove Postgres section + `postgres.*` knobs; add `## Chart: mclaude-postgres` with its own resource table + values; add `database.*` knobs to mclaude-cp.
- `docs/mclaude-control-plane/spec-control-plane.md`: clarify that `DATABASE_URL` env var is composed from `database.*` Helm values; update startup-sequence text that references "the chart's Postgres StatefulSet"; document the `bootstrap-admin` UPSERT idempotence semantics.
- `docs/spec-state-schema.md`: add UNIQUE constraint note to `users.email` if missing (likely already there).

## Scope

**In v1:**
- New `charts/mclaude-postgres` chart with values for image, persistence, existing Secret.
- `mclaude-cp` removes Postgres templates + `postgres.*` knobs; adds `database.*` knobs.
- `mclaude-cp/init-keys` runs unchanged structurally (still pre-install hook), but its bootstrap-admin SQL becomes UPSERT.
- CI workflow updated to run two `helm upgrade --install` commands.
- `values-aks.yaml`, `values-e2e.yaml`, `values-k3d-ghcr.yaml` adjusted: `mclaude-postgres` gets its own value file(s); `mclaude-cp` files lose Postgres knobs and gain `database.*` overrides.

**Deferred:**
- Postgres HA / replication.
- Automatic schema migrations across major Postgres versions.
- Connection pooling (PgBouncer).

## Open questions

_(none — all resolved)_

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| Fresh install, in-cluster Postgres | `helm install mclaude-postgres` then `helm install mclaude-cp` succeeds; bootstrap admin row present | Wipe `mclaude-system` ns + PVCs; Bitwarden Secret pre-created | mclaude-postgres, mclaude-cp, init-keys |
| Bootstrap idempotence on re-upgrade | `helm upgrade mclaude-cp` rerun with same values — admin row unchanged, no duplicate insert errors | After fresh install above, run helm upgrade twice | init-keys (UPSERT path) |
| Bootstrap idempotence on password rotation | `helm upgrade mclaude-cp --set bootstrapAdminPassword=newpass` — admin row updated; old password no longer authenticates; new password does | Run helm upgrade with rotated password | init-keys, CP `/auth/login` |
| Managed-DB shape (no `mclaude-postgres` release) | Skip `mclaude-postgres`; create a Postgres elsewhere (testcontainers or external) + Secret with `postgres-password`; install only `mclaude-cp` with `database.host` pointing at it; bootstrap admin succeeds, login works | External Postgres (testcontainers in CI) | mclaude-cp only |
| Wrong install order self-hosted | Install `mclaude-cp` *before* `mclaude-postgres` — must fail fast with a clear DNS/connection error in init-keys logs (no infinite retry, no hidden state) | mclaude-postgres release absent | mclaude-cp init-keys |

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| `charts/mclaude-postgres` (new chart) | ~120 lines | 50k | StatefulSet, two Services, values.yaml, values-aks.yaml, values-e2e.yaml, NOTES.txt |
| `charts/mclaude-cp` (subtract Postgres, add database.*) | ~-80 / +50 lines | 35k | Delete Postgres templates, update init-keys-job + control-plane-deployment env (including dbmate init container), strip `postgres.*` from values |
| `mclaude-control-plane` (init-keys restructure + UPSERT) | ~30 lines | 50k | Decouple admin-bootstrap from key-gen early-return; replace conditional INSERT with `INSERT ... ON CONFLICT (email) DO UPDATE`; update existing `TestIntegration_BootstrapAdminUser_*` tests for new password-rotation semantics |
| `.github/workflows/deploy-main.yml` | ~30 lines | 20k | Two helm commands instead of one |
| Spec edits | ~80 lines | 20k | spec-helm.md split, spec-control-plane.md clarification |

**Total estimated tokens:** 150k
**Estimated wall-clock:** 2–3h
