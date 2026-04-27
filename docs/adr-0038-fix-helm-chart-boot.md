# ADR: Fix mclaude-cp Helm Chart Bootstrap (NATS JWT Auth + Postgres Connectivity)

**Status**: implemented
**Status history**:
- 2026-04-27: accepted
- 2026-04-27: implemented — all mclaude-cp pods Running (NATS, Postgres, control-plane, SPA)

## Overview

Fix six boot failures in the `mclaude-cp` Helm chart that prevent the control-plane, hub NATS, and Postgres from starting after the ADR-0035 chart split.

## Motivation

After ADR-0035 and ADR-0037 landed, the deploy workflow triggers but all pods except the SPA crash-loop:

1. **NATS** — `"system account not setup"`. The NATS JWT auth chain requires a `system_account` claim in the operator JWT so JetStream can create internal subscriptions. The `GenerateOperatorAccount()` helper doesn't set `SystemAccount` on the operator claims. Additionally, `resolver_preload` needs format `"<ACCOUNT_PUB_KEY>": "<ACCOUNT_JWT>"` but the Secret doesn't store a preformatted conf line, and the `include` path resolves incorrectly.
2. **Control-plane** — `"hostname resolving error: lookup mclaude-postgres"`. The control-plane Deployment reads `DATABASE_URL` from Secret `mclaude-postgres` key `database-url`, but the chart-created Postgres service is named `{release}-postgres` (i.e. `mclaude-cp-postgres`), and the pre-existing Secret only has `postgres-password` — no `database-url` key. The chart must construct the DATABASE_URL itself from the known service name + password Secret.
3. **Init-keys Secret** — missing `accountPublicKey` needed by NATS `resolver_preload`. The Secret needs the account public key stored so NATS can map it to the account JWT.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| NATS system_account | Set `opClaims.SystemAccount = acctPub` in `GenerateOperatorAccount()` before encoding the operator JWT | NATS requires the operator JWT to declare which account is the system account for JetStream internal subscriptions. Without it, JetStream refuses to start. |
| NATS resolver_preload | Store a preformatted `resolverPreload` key in the operator-keys Secret containing `"<acctPub>": "<acctJWT>"`. NATS configmap uses `include` with absolute path `/etc/nats/operator-keys/resolverPreload`. | Avoids runtime construction of the preload line. The include path must be absolute because NATS resolves relative includes from the config file's directory. |
| DATABASE_URL construction | Chart template constructs `DATABASE_URL` as an env var: `postgres://mclaude:$(POSTGRES_PASSWORD)@{release}-postgres.{namespace}.svc:5432/mclaude?sslmode=disable`. Password sourced from Secret `mclaude-postgres` key `postgres-password`. | Eliminates the need for a separate `database-url` key in the Secret. The service name is deterministic from the Helm release name. |
| operator-keys Secret keys | Add `accountPublicKey` and `resolverPreload` to the Secret created by init-keys. Full key set: `operatorJwt`, `operatorSeed`, `accountJwt`, `accountSeed`, `accountPublicKey`, `resolverPreload`. | NATS needs the account public key for resolver_preload. Storing the preformatted line avoids shell/template gymnastics. |
| Control-plane NATS auth | Helm template sets `NATS_ACCOUNT_SEED` from operator-keys Secret. Go code generates a user JWT signed by the account key and connects with `nats.UserJWT()` credentials. | With JWT auth on the server, all clients must present valid credentials. |
| SQL backfill DO block | Alias `users` table as `usr` in the FOR...IN SELECT to avoid PL/pgSQL variable shadowing. | PL/pgSQL resolves `u.id` as the loop variable before assignment. |
| NATS resolver_preload path | Relative path `operator-keys/resolverPreload`, not absolute. | Absolute path gets doubled: `/etc/nats/etc/nats/...`. |
| Account JetStream limits | Set `JetStreamLimits{MemoryStorage: -1, DiskStorage: -1, Streams: -1, Consumer: -1}` on the application account claims. | NATS disables JetStream for accounts that don't explicitly set JetStream limits. Without this, the control-plane gets `jetstream not enabled for account` when creating KV buckets. |
| Separate system account | Generate a dedicated system account NKey pair (no JetStream). Set `opClaims.SystemAccount` to the system account's public key, not the application account. Both account JWTs are preloaded in `resolverPreload`. | NATS forbids JetStream on the system account (`Not allowed to enable JetStream on the system account`). The system account is used for internal NATS subscriptions ($SYS); the application account carries all mclaude traffic and JetStream state. |

## Impact

**No specs updated** — these are bug fixes restoring compliance with spec-helm.md's described behavior. The spec already says the init-keys Job creates the operator-keys Secret and NATS uses it for the trust chain.

**Components implementing the change:**
- `mclaude-common/pkg/nats/operator_keys.go` — add `SystemAccount` to operator claims, set JetStream limits on account
- `mclaude-control-plane/init_keys.go` — add `accountPublicKey` + `resolverPreload` to Secret
- `charts/mclaude-cp/templates/nats-configmap.yaml` — fix resolver_preload include path, use absolute path
- `charts/mclaude-cp/templates/control-plane-deployment.yaml` — construct DATABASE_URL from service name + password Secret
- `charts/mclaude-cp/templates/init-keys-job.yaml` — same DATABASE_URL fix for the init-keys Job
- `mclaude-control-plane/main.go` — NATS JWT auth connection
- `charts/mclaude-cp/templates/control-plane-deployment.yaml` — NATS_ACCOUNT_SEED env

## Scope

Bug fix only. No new features, no spec changes.
