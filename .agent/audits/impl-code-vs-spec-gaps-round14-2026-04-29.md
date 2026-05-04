# Audit Round 14 — Code vs Spec Gaps

**Date:** 2026-04-29
**Auditor:** Subagent (worker)
**Scope:** All 6 spec files × all listed code files × 12 audit dimensions

---

## Gaps Found

### GAP 1: Session-agent-template ConfigMap Helm file exists but spec says it's missing

| Field | Value |
|-------|-------|
| **Summary** | Spec-helm.md describes the session-agent-template ConfigMap as a "Known bug" with the Helm template file missing — but the file now exists and correctly renders all `sessionAgent.*` values. |
| **Code** | `charts/mclaude-worker/templates/session-agent-template.yaml` (entire file — renders image, imagePullPolicy, terminationGracePeriodSeconds, resourcesJson, projectPvcSize, projectPvcStorageClass, nixPvcSize, nixPvcStorageClass, corporateCAEnabled, corporateCAConfigMapName, corporateCAConfigMapKey) |
| **Spec** | `docs/charts-mclaude/spec-helm.md` § "Session-Agent Template" — states: "**Known bug:** the Helm template file for this ConfigMap is missing from `charts/mclaude-worker/templates/` — `values.yaml` `sessionAgent.*` settings (image tag, 50Gi PVC, 86400s grace, corporate CA) are never rendered. The controller falls back to `defaultTemplate()` with wrong image (`:latest`), undersized PVCs (10Gi), short grace period (30s), and no corporate CA." |
| **Detail** | The template file exists and renders correctly. The "Known bug" description is stale — the bug has been fixed but the spec still describes it as present. The controller WILL read the ConfigMap with correct values (50Gi PVC, 86400s grace, corporate CA settings) when deployed via Helm. The spec should remove this Known bug entry. |

### GAP 2: `PROVIDER_SECRET_{ID}` env var pattern not documented in spec env var table

| Field | Value |
|-------|-------|
| **Summary** | Control-plane `providers.go` reads `PROVIDER_SECRET_{ID}` environment variables (e.g. `PROVIDER_SECRET_GITHUB`) to load OAuth provider client secrets, but this env var pattern is not documented in the spec-control-plane.md Environment Variables table. |
| **Code** | `mclaude-control-plane/providers.go` : `LoadProviders()` function — `envKey := "PROVIDER_SECRET_" + strings.ToUpper(strings.ReplaceAll(providers[i].ID, "-", "_"))` / `if v := os.Getenv(envKey); v != "" { providers[i].ClientSecret = v; continue }` |
| **Spec** | `docs/mclaude-control-plane/spec-control-plane.md` § "Environment Variables" — table has no entry for `PROVIDER_SECRET_*`. The spec says "Loads OAuth provider config from `PROVIDERS_CONFIG_PATH`, resolving client secrets from K8s Secrets" but code actually resolves from env vars or files at `/etc/mclaude/secrets/{clientSecretRef}/client-secret`. |
| **Detail** | Per ADR-0035 the control-plane is K8s-free, so client secrets cannot be read from K8s Secrets. The code instead reads from `PROVIDER_SECRET_{ID}` env vars (uppercased, dashes→underscores) with a fallback to file paths. Neither mechanism is documented in the env var table. The Helm template would need to inject these env vars (e.g. from a Secret) for OAuth providers to work in production. |

### GAP 3: Several CP Helm values consumed by templates but missing from spec-helm config table

| Field | Value |
|-------|-------|
| **Summary** | The `mclaude-cp` control-plane Deployment template injects env vars from three `values.yaml` keys that are not listed in the spec-helm.md `mclaude-cp` Configuration knobs table: `controlPlane.config.natsWsUrl`, `controlPlane.config.logLevel`, and `controlPlane.devOAuthToken`. |
| **Code** | `charts/mclaude-cp/templates/control-plane-deployment.yaml` — env entries: `NATS_WS_URL` from `.Values.controlPlane.config.natsWsUrl`, `LOG_LEVEL` from `.Values.controlPlane.config.logLevel`, `DEV_OAUTH_TOKEN` from `.Values.controlPlane.devOAuthToken` |
| **Spec** | `docs/charts-mclaude/spec-helm.md` § "Chart: mclaude-cp / Configuration knobs" — table does not include `controlPlane.config.natsWsUrl`, `controlPlane.config.logLevel`, or `controlPlane.devOAuthToken` |
| **Detail** | `controlPlane.config.logLevel` controls the LOG_LEVEL env var (documented as "Not read by Go code" in spec-control-plane, but still a Helm knob). `controlPlane.config.natsWsUrl` sets NATS_WS_URL (the external WebSocket URL returned to browser clients on login — functional). `controlPlane.devOAuthToken` sets DEV_OAUTH_TOKEN (injected into per-user secrets for dev). All three are consumed by the Helm template and should appear in the config table. The deploy-main.yml workflow explicitly passes `--set controlPlane.devOAuthToken=...`, confirming it's a real knob. |

### GAP 4: `init-keys` subcommand env vars `NAMESPACE` and `OPERATOR_KEYS_SECRET` not documented

| Field | Value |
|-------|-------|
| **Summary** | The `init-keys` subcommand reads `NAMESPACE` (default `mclaude-system`) and `OPERATOR_KEYS_SECRET` (default `operator-keys`) env vars to determine where to write the operator-keys Secret, but these are not documented in the spec. |
| **Code** | `mclaude-control-plane/init_keys.go` : `runInitKeys()` — `namespace := envOr("NAMESPACE", "mclaude-system")` / `secretName := envOr("OPERATOR_KEYS_SECRET", "operator-keys")` |
| **Spec** | `docs/mclaude-control-plane/spec-control-plane.md` § "control-plane init-keys" — prose says "writes them to the operator-keys Secret in mclaude-system" but does not list `NAMESPACE` or `OPERATOR_KEYS_SECRET` as configurable env vars. The `gen-leaf-creds` section does document its env vars (`NAMESPACE`, `LEAF_CREDS_SECRET`, etc.) in the same file, but init-keys does not receive the same treatment. |
| **Detail** | Minor documentation gap. An operator customizing the namespace or secret name for init-keys would need to read the code. For consistency with the gen-leaf-creds section, these should be documented. |

---

## Borderline Items Considered and Excluded

| # | Item | Exclusion Reason |
|---|------|-----------------|
| 1 | `handleVersion` not visible in read files, `MIN_CLIENT_VERSION`/`SERVER_VERSION` env vars | Function likely in an unread file; not in the assigned file list — cannot confirm gap without reading it. |
| 2 | `clearPendingControlsForResume` sets state to `idle` not `restarting` | Already flagged in spec-session-agent ("Known bug: clearPendingControlsForResume() sets state to 'idle' instead of 'restarting'"). |
| 3 | `handleSideEffect` only accumulates 3 of 5 usage fields | Already flagged: "Known bug: handleSideEffect only accumulates inputTokens, outputTokens, and costUsd." |
| 4 | QuotaMonitor uses `userID` (UUID) for quota subject | Already flagged: "Known bug: newQuotaMonitor() receives userID (UUID)…" |
| 5 | `handleRefresh` returns `s.natsURL` not `s.natsWsURL` | Already flagged: "Known bug: returns s.natsURL (internal broker URL)." |
| 6 | `IssueUserJWT` double-expiry (now+16h instead of now+8h) | Already flagged: "Known bug: IssueUserJWT is called with expiresAt + expirySecs…" |
| 7 | `IssueHostJWT` called with UUID instead of slug in multiple places | Already flagged for handleHostRegister, adminGrantCluster, handleCreateHost. |
| 8 | `CreateProjectWithIdentity` doesn't set `slug` or `host_id` | Already flagged as Known bug in spec-control-plane. |
| 9 | Session-agent doesn't create `MCLAUDE_LIFECYCLE` stream | Already flagged: "the session-agent code does not currently create this stream on startup." |
| 10 | DEL handler ghost sessions in SPA session-store | Already flagged: "Known bug: DEL handler extracts session slug…this._sessions.delete(sessionSlug) is a no-op." |
| 11 | CRD `userSlug`/`projectSlug` not in `required` list | Already flagged: "present in CRD schema but not in required list; should be required." |
| 12 | Session-agent `natsConnect()` lacks `MaxReconnects(-1)` | Already flagged: "Known bug: natsConnect() uses NATS defaults (MaxReconnects=60…)." |
| 13 | Daemon `dispatchQueuedJob` casts `job.ProjectID` UUID to `ProjectSlug` | Already flagged in spec-state-schema. |
| 14 | `controlPlane.service.adminPort` default 9090 vs Go default 9091 | Already flagged: "chart default is 9090, Go code default is 9091." |
| 15 | `JWT_REFRESH_THRESHOLD_SECONDS` phantom env var | Already flagged: "Not read by Go code — phantom env var with no effect." |
| 16 | `adminGrantCluster` calls `GetUserByEmail` with slug | Already flagged: "handler calls GetUserByEmail(req.UserSlug) instead of GetUserBySlug(req.UserSlug)." |
| 17 | `computeUserSlug` doesn't check blocklist | Already flagged: "Known bug: computeUserSlug() in db.go does not check against the blocklist." |
| 18 | `adminStopSession` writes to non-existent `sessions` table | Already flagged as "non-functional." |
| 19 | Admin user creation doesn't create default host row | Already flagged: "Known bug: POST /admin/users and seedDev CreateUser do not create the default host row." |
| 20 | `HOST_SLUG` fallback to `h-unknown` instead of fatal exit | Already flagged: "Known bug: code does not fatal on absence — silently falls back through slugify(hostname) → h-unknown." |

---

## Confidence: **MEDIUM-HIGH**

Four unflagged gaps found. GAP 1 (stale Known bug for session-agent-template) is the most operationally significant — the spec incorrectly describes a broken state that has been fixed. GAPs 2–4 are documentation gaps in env var and Helm value tables that could cause operator confusion. No critical behavioral bugs were found that aren't already flagged.
