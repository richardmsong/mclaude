## Audit: 2026-04-26T22:45:00Z

**Document:** docs/adr-0035-unified-host-architecture.md

### Round 1

**Gaps found: 9**

1. `users` table missing `is_admin` and `oauth_id` columns in canonical state schema.
2. `hosts` table DDL missing `online` column / unclear whether `online` is Postgres or KV.
3. `$SYS` presence mapping — NKey-to-host resolution unspecified; SPA/control-plane self-events not handled; `client.kind` (Leafnode vs Client) discrimination not specified.
4. `directNatsUrl` has no storage column on `hosts` and no admin-cluster-register parameter.
5. Leaf-node remote auth: ADR shows `nkey: /etc/nats/leaf.nk`, state-schema shows `credentials: /etc/nats/leaf.creds`. Conflict.
6. Provisioning reply payload not specified in ADR (controller-spec defines it; ADR doesn't).
7. BYOH device-code: ADR step 3 says control-plane generates NKey; control-plane spec says host generates and submits public_key. Pick one.
8. `mclaude-common/pkg/subj` still exports `ClusterAPIProjectsProvision`, `ClusterAPIStatus`, `UserHostStatus`. ADR scope omits removal.
9. `mclaude-session-agent/daemon.go` carries laptop/heartbeat code — `UserAPIProjectsCreate`, `LaptopsKVKey`, `laptopsKV`, `runLaptopHeartbeat` — not in ADR's enumerated fix list.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | users.is_admin / oauth_id missing | Added both columns to spec-state-schema.md `users` table; documented bootstrap-admin OAuth-link flow. | factual |
| 2 | hosts.online vs KV | Pinned `online` as KV-only (mclaude-hosts) backed by $SYS; Postgres holds `last_seen_at` for the historical record. SPA reads online from the KV watch, not from Postgres. | factual |
| 3 | $SYS NKey-to-host resolution | Documented full handler: subscribe to $SYS.ACCOUNT.<accountKey>.CONNECT/DISCONNECT; switch on payload `client.kind`. `kind="Client"` → lookup `hosts.public_key = client.nkey` and update single row. `kind="Leafnode"` → lookup `hosts.public_key = client.nkey AND type='cluster'` and update **all** rows where slug matches and type='cluster' (cluster-shared liveness). No match → ignore (covers SPA's ephemeral NKeys and control-plane's own connection). | factual |
| 4 | directNatsUrl storage | Added `direct_nats_url` cluster-shared column on `hosts` (NULL for machine, duplicated across user rows for cluster). Added `--direct-nats-url` to `mclaude cluster register` and `directNatsUrl` to admin POST/GET endpoints. Login response sources directly from this column. | factual |
| 5 | nkey vs credentials | Pinned `credentials: /etc/nats/leaf.creds` everywhere (3-tier JWT chain requires `.creds` format). Removed the `nkey:` form from ADR-0035 Helm decision. | factual |
| 6 | Provisioning reply payload | Added explicit reply schema (`{ok, projectSlug?, error?, code?}`) to ADR-0035 Component Changes section, mirroring spec-controller.md. | factual |
| 7 | Device-code NKey: host or CP | Pinned host-generated. Updated ADR User Flow A step 3 to: "Host generates NKey pair locally (private seed never leaves machine); CLI POSTs `{code, name, publicKey}`; control-plane stores public_key, mints JWT against it, returns `{slug, jwt, hubUrl}`. The seed stays on the host." | factual |
| 8 | mclaude-common dead helpers | Added removal of `ClusterAPIProjectsProvision`, `ClusterAPIStatus`, `UserHostStatus` to ADR-0035 Component Changes for mclaude-common. | factual |
| 9 | session-agent daemon.go scope | Enumerated daemon.go fix sites in ADR-0035: `UserAPIProjectsCreate` removal (line 126), `LaptopsKVKey` removal (lines 148, 170), `laptopsKV` field + `mclaude-laptops` bucket reference removal (lines 57, 85), `runLaptopHeartbeat` goroutine removal, `DaemonConfig.HostSlug` field add. | factual |

### Round 2

**Gaps found: 4**

1. session-agent compile gap is not closed in code — call sites in agent.go / state.go / daemon_jobs.go still use 2-arg / 3-arg helpers.
2. daemon.go fixes not applied in code — laptopsKV field, runLaptopHeartbeat goroutine, etc. all still present.
3. ADR `CREATE TABLE hosts` block missing `direct_nats_url`.
4. CLI flag naming contradiction: User Flow B uses `--slug us-east`; Component Changes uses `--name …`.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | session-agent compile gap (code) | **Not a design gap.** ADR Component Changes already enumerates every fix site (agent.go lines 285/547/1136/1149/1163/1179; state.go updates; daemon_jobs.go lines 342/377/438/491/635). Implementation is exactly what the post-acceptance `/feature-change` run will execute (Stage 3 of the Implementation Plan). The codebase remaining unfixed is the very motivation for this ADR — not a flaw in the design. Logged here for transparency; reframed evaluator prompt for round 3 to scope on design completeness, not codebase compliance. | not-a-gap |
| 2 | daemon.go fixes not applied (code) | **Not a design gap.** Same reasoning as #1; ADR enumerates every removal/addition. Implementation is the `/feature-change` run that follows ADR acceptance. | not-a-gap |
| 3 | hosts DDL missing direct_nats_url | Updated `CREATE TABLE hosts` block in ADR Data Model to include `direct_nats_url TEXT`; added clarifying notes about `users.is_admin` / `users.oauth_id` columns landing in the same migration. | factual |
| 4 | --slug vs --name in cluster register CLI | Pinned `--slug <cslug>` as the required flag in CLI Component Changes; `--name <display>` is optional and defaults to the slug. Matches User Flow B and spec-helm.md. | factual |

### Round 3

**Gaps found: 6**

1. Login response shape: Decisions table lists `clusters: [...]` as a top-level field; Data Model and spec-state-schema.md have no `clusters` key. Data Model projects show only `{slug, name, hostSlug}` — missing `hostType`, `jsDomain`, `directNatsUrl`.
2. spec-control-plane.md `POST /admin/clusters/{cslug}/grants` copies only `(js_domain, leaf_url, account_jwt)` — missing `direct_nats_url` and `public_key`.
3. Device-code status-check endpoint for CLI polling is undefined — no `GET` endpoint exists for the CLI to poll after generating a code.
4. `POST /api/users/{uslug}/hosts/code` does not accept or store `publicKey`; dashboard cannot forward it at register time.
5. BYOH startup command: User Flow A step 5 says `mclaude controller --host`; CLI Component Changes says `mclaude daemon --host`; Scope/Impact lists only `daemon --host`.
6. User Flow B step 2 says "generates the cluster's account JWT" but there is one account per install; should return the existing `accountJwt`.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Login response `clusters` array | Removed `clusters: [...]` from Decisions table row. Rewrote row to: `{ user, jwt, nkeySeed, hubUrl, hosts: [...], projects: [...] }` with no top-level `clusters` — SPA derives via `hosts.filter(h => h.type === 'cluster')`. Added `hostType`, `jsDomain?`, `directNatsUrl?` to Data Model projects examples to match spec-state-schema.md. | factual |
| 2 | Grant endpoint missing fields | Updated spec-control-plane.md `POST /admin/clusters/{cslug}/grants` to copy all 5 cluster-shared fields: `js_domain`, `leaf_url`, `account_jwt`, `direct_nats_url`, `public_key`. Matches ADR line 73/187 and spec-state-schema.md line 81. | factual |
| 3 | Device-code poll endpoint | Added `GET /api/users/{uslug}/hosts/code/{code}` to spec-control-plane.md and ADR (Component Changes + Scope). Returns `{status: "pending", expiresAt}` or `{status: "completed", slug, jwt, hubUrl}`. 410 Gone if expired. Updated ADR User Flow A step 4 to reference the polling endpoint. | factual |
| 4 | publicKey on code generation | CLI now submits `{publicKey}` in `POST /api/users/{uslug}/hosts/code`; server stores it with the code record. Dashboard calls `POST /api/hosts/register` with just `{code, name}` — server looks up the stored publicKey. Updated ADR User Flow A steps 2-3 and spec-control-plane.md endpoint descriptions. | factual |
| 5 | controller vs daemon command | Standardized on `mclaude daemon --host <hslug>` everywhere. Fixed ADR User Flow A step 5 from `mclaude controller --host` to `mclaude daemon --host`. CLI Component Changes and Scope already used `daemon`. | factual |
| 6 | "generates" cluster account JWT | Reworded ADR User Flow B step 2 to clarify: existing deployment-level `accountJwt` and `operatorJwt` are read from the Secret and returned (not regenerated). One operator and one account per install. | factual |

### Round 4

**Gaps found: 2**

1. CLI spec does not document `host`, `cluster`, or `daemon` subcommands — ADR Component Changes lists them but spec-cli.md only had `attach` and `session list`.
2. Control-plane NATS provisioning request payload missing `gitIdentityId` — ADR includes it, spec-control-plane.md and spec-state-schema.md omitted it.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | CLI spec missing host/cluster/daemon | Added full command documentation to spec-cli.md: `host register`, `host list`, `host use`, `host rm`, `cluster register`, `cluster grant`, `daemon --host` — with flags, descriptions, and flow references matching ADR-0035. | factual |
| 2 | Provisioning payload missing gitIdentityId | Added `gitIdentityId` to the NATS provisioning request payload in spec-control-plane.md (Project Creation Flow step 6) and spec-state-schema.md (NATS Subjects table). Matches ADR-0035 provisioning request/reply contract. | factual |

### Round 5

**Gaps found: 1**

1. spec-controller.md says `mclaude controller --host <hslug>`; ADR and spec-cli.md say `mclaude daemon --host <hslug>`.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | controller vs daemon in spec-controller.md | Changed spec-controller.md line 91 from `mclaude controller --host` to `mclaude daemon --host`. All docs now agree on `daemon`. | factual |

### Round 6

**CLEAN — no design-level contradictions found.**

