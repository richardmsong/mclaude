## Run: 2026-04-16T03:06:36Z

**Gaps found: 13**

1. **`POST /api/clusters` auth mechanism conflicts with existing admin pattern** — The doc says "admin-only (admin bearer token)" for the new cluster endpoints, but the current codebase serves ALL admin endpoints on a separate break-glass port (`:9091`, `127.0.0.1` only). `RegisterRoutes` (the main mux) has no admin-auth middleware wired to it. A developer must decide: are cluster endpoints on the admin port (`:9091`) or the main port (`:8080`)? If on the main port, no admin middleware currently exists there. If on the admin port, the endpoint is unreachable from outside the cluster pod. The doc calls it "admin-only" and shows the path as `/api/clusters` but does not specify which port.
   - **Doc**: "`POST /api/clusters` — Auth: admin-only (admin bearer token)"
   - **Code**: `server.go:39` — `AdminMux()` only handles `/admin/` prefix routes on port `:9091`, bound to `127.0.0.1`. `RegisterRoutes` (`:8080`) has no admin bearer check.

2. **`POST /api/clusters/{id}/members` response body not specified** — The endpoint doc describes behavior but provides no response body schema. A developer must decide: does it return the created `user_clusters` record? 201 with body, or 204 with no body? The pattern for all other mutation endpoints in the codebase uses `201 Created` with a JSON body for creates and `204 No Content` for deletes, but this is not stated.
   - **Doc**: "`POST /api/clusters/{id}/members` — Behavior: creates the `user_clusters` record. If this is the user's first cluster grant, sets `is_default = TRUE`." (no response format)
   - **Code**: No existing endpoint to follow for this pattern.

3. **`DELETE /api/clusters/{id}/members/{userId}` — "sets another grant as default (or none if last)" is ambiguous** — When a user loses their default cluster via revocation and has other grants, the doc says "sets another grant as default." It does not specify which grant becomes default: oldest? newest? alphabetically first? A developer must pick an ordering and there is no hint.
   - **Doc**: "If this was the user's default, sets another grant as default (or none if last)."
   - **Code**: `user_clusters` has `created_at` but no explicit ordering is mandated.

4. **Login response field name mismatch: `natsJwt` vs `jwt`** — The design shows the modified login response using `"natsJwt"` for the JWT field, but the existing `LoginResponse` struct uses `json:"jwt"` and the SPA `AuthTokens` interface references the field as `jwt`. The existing `AuthClient.login()` reads `tokens.jwt`. A developer implementing the modified login response must decide whether to rename the field (breaking the existing SPA) or keep `jwt` (diverging from the doc).
   - **Doc**: `"natsJwt": "..."` in the modified login response JSON example.
   - **Code**: `auth.go:30` — `JWT string \`json:"jwt"\`\`; `types.ts:43` — `jwt: string` in `AuthTokens`.

5. **`INATSClient` interface has no domain-qualified KV watch method** — The SPA dashboard design requires domain-qualified KV watches (`js.jetstream({ domain: cluster.jsDomain })`). The current `INATSClient` interface (`types.ts:24-38`) has only `kvWatch(bucket, key, callback)` with no `domain` parameter. The design's TypeScript snippet calls `nats.jetstream({ domain: cluster.jsDomain })` directly on a raw connection object, bypassing the `INATSClient` abstraction. A developer must either extend `INATSClient` with a domain parameter or bypass the interface — the design does not say which.
   - **Doc**: "const js = nats.jetstream({ domain: cluster.jsDomain })\nconst kv = js.keyValue('mclaude-sessions')\nkv.watch(`${userId}.>`)"
   - **Code**: `types.ts:32` — `kvWatch(bucket: string, key: string, callback: (entry: KVEntry) => void): () => void` — no domain parameter. `NATSClient.kvWatch` calls `js.views.kv(bucket)` with no domain option.

6. **`SessionStore` has no multi-cluster aggregation capability** — The design says "The SessionStore aggregates entries from all cluster watchers into a single list, tagged with cluster metadata." The current `SessionStore` constructor takes a single `INATSClient` and a single `userId`. It has no concept of multiple watchers keyed by cluster, no cluster tag on `SessionKVState`, and no API for registering per-cluster watchers. The required structural change to `SessionStore` is not specified: does it take a map of clients? Does it create one instance per cluster? How are cluster tags added to `SessionKVState` entries when `SessionKVState` has no `clusterId` field?
   - **Doc**: "The SessionStore aggregates entries from all cluster watchers into a single list, tagged with cluster metadata."
   - **Code**: `session-store.ts:8-19` — `SessionStore` constructor takes `natsClient: INATSClient, userId: string`. `SessionKVState` (`types.ts:91-104`) has no `clusterId` field.

7. **`projects` table migration strategy not specified** — The design adds `cluster_id TEXT NOT NULL FK->clusters ON DELETE RESTRICT` to the existing `projects` table. The `projects` table already has rows in production. A NOT NULL column with a foreign key cannot be added without providing a default value or a migration strategy for existing rows. The design does not specify what `cluster_id` value existing projects should get, nor whether the migration uses a two-step (add nullable, backfill, add constraint) approach. The codebase uses a single `schema` constant with `CREATE TABLE IF NOT EXISTS` — there is no migration framework in use (`db.go:53` runs `Migrate` which just executes the schema string).
   - **Doc**: "Added to existing table via migration. `ON DELETE RESTRICT` prevents deleting a cluster with active projects."
   - **Code**: `db.go:53-56` — `Migrate` runs a single schema string with `CREATE TABLE IF NOT EXISTS`. No ALTER TABLE migration path exists.

8. **Backwards-compatible single-cluster mode — auto-registration trigger is unspecified** — The doc says "The control plane auto-registers the local cluster on startup if no clusters exist in Postgres." It does not specify: what `jsDomain` and `natsUrl` are used for the auto-registered cluster, what `name` it gets, what `nats_ws_url` it gets, or whether existing `projects` rows are backfilled with the auto-registered cluster's ID. Without these specifics, a developer cannot implement the auto-registration logic.
   - **Doc**: "The control plane auto-registers the local cluster on startup if no clusters exist in Postgres."
   - **Code**: `main.go:26-178` — no auto-registration logic exists; startup reads env vars `NATS_URL` and `NATS_WS_URL` but there is no cluster registry yet.

9. **Worker controller NATS connection credentials not specified** — The worker controller connects to the "local worker NATS" but the design does not specify what NATS credentials the worker controller uses. The session-agent uses user JWTs scoped to `mclaude.{userId}.>`. The control plane uses an account-level key. The worker controller must subscribe to `mclaude.clusters.{clusterId}.projects.provision` — a subject outside any user namespace. What JWT/NKey is issued for the worker controller? Who issues it? The `leafnodes` config section on the worker NATS also needs an account key context, but the design only mentions leaf credentials for the leaf node transport, not for the controller's own NATS auth.
   - **Doc**: "NATS connection: Connects to the local worker NATS. Messages reach the hub transparently via the leaf node."
   - **Code**: `nkeys.go` — only `IssueUserJWT` and `IssueSessionAgentJWT` exist. No mechanism to issue a controller-level JWT or worker-service credential.

10. **Hub NATS JetStream domain name is specified as `"hub"` but existing config has no domain** — The design specifies the hub NATS should have `jetstream { domain: hub }`. The existing NATS config (`nats-configmap.yaml`) has `jetstream { store_dir: /data/jetstream; max_file_store: ... }` with no `domain` field. Adding a domain to an existing JetStream instance that already has streams/KVs changes the stream addressing. A developer needs to know: does enabling `domain: hub` on the existing NATS break existing KV watchers and stream consumers in the current single-cluster deployment? The design is silent on this.
    - **Doc**: "Hub NATS: `jetstream { store_dir: /data/jetstream; domain: hub }`"
    - **Code**: `charts/mclaude/templates/nats-configmap.yaml:19-22` — no `domain` line in the existing jetstream block.

11. **`mclaude.clusters.{clusterId}.status` heartbeat subject — subscriber and behavior not specified** — The NATS subjects table lists `mclaude.clusters.{clusterId}.status` as a periodic heartbeat from the worker controller to the control plane. However: no handler in the control plane is described that subscribes to this subject. No behavior on receipt is described (does it update `clusters.status` in Postgres?). No heartbeat interval is given. No detection threshold for "missed heartbeats = cluster offline" is specified. Without these, the subject is listed but unimplementable.
    - **Doc**: "`mclaude.clusters.{clusterId}.status` | worker controller | control-plane | `{ clusterId, status, sessionCount, capacity }` | Core NATS (periodic heartbeat)"
    - **Code**: No subscriber exists for any cluster status subject.

12. **`POST /api/clusters` — JetStream domain name generation algorithm not specified** — The doc says the control plane "generates a unique JetStream domain name from the cluster name." The algorithm is not specified. Domain names must be valid NATS identifiers (no dots, spaces, special chars). If two clusters have similar names (e.g., "us-west" and "us-west-2"), collision avoidance logic is needed. Without a specified algorithm (e.g., slugify + UUID suffix, sanitize + sequence number), a developer must invent one.
    - **Doc**: "generates a unique JetStream domain name from the cluster name"
    - **Code**: No existing slug/sanitize utility in the codebase.

13. **SPA direct-to-worker connection fallback — resubscription mechanics for in-flight sessions not specified** — When the SPA falls back from a failed direct connection to hub-based domain-qualified JetStream, it must resubscribe to the event stream and KV. The design does not specify: (a) what `startSeq` to use when resubscribing via hub (must use `_lastSequence` from `EventStore`), (b) whether the hub-based `jsSubscribe` call requires a domain-qualified stream name (the current `jsSubscribe` in `NATSClient` takes a stream name like `MCLAUDE_EVENTS` with no domain option), and (c) whether the `kvWatch` call via hub needs a domain parameter. The `INATSClient` interface has no domain-qualified variants of `jsSubscribe` or `kvWatch`.
    - **Doc**: "If direct connection fails or no `directNatsUrl`: use the hub connection with domain-qualified JetStream subscriptions."
    - **Code**: `nats-client.ts:81-115` — `jsSubscribe` has no domain parameter; `nats-client.ts:138-176` — `kvWatch` has no domain parameter.
## Run: 2026-04-16T00:00:00Z

**Gaps found: 11**

1. **`AuthTokens` type lacks `projects` and `clusters` fields** — The design requires the login response to include `projects` and `clusters` arrays, and states "The `AuthTokens` type gains `projects` and `clusters` arrays." The existing `AuthTokens` interface in `mclaude-web/src/types.ts` (line 41-45) has only `jwt`, `nkeySeed`, `userId`, and `natsUrl?`. `AuthClient.login()` deserializes the server response directly into `AuthTokens`. Without specifying the exact TypeScript field names and types added to `AuthTokens`, a developer cannot implement the login plumbing or any downstream code that reads from `AuthTokens`. The doc names the JSON fields (`clusterId`, `clusterName`, `jsDomain`, `directNatsUrl`, etc.) but does not give the TypeScript interface definitions.
   - **Doc**: "The `AuthTokens` type gains `projects` and `clusters` arrays matching the JSON above. `AuthStore` stores these after login and exposes them to other stores."
   - **Code**: `AuthTokens` at `mclaude-web/src/types.ts:41` has no `projects` or `clusters` fields. `AuthStore` has no getter exposing these.

2. **`INATSClient` interface extension not specified for `domain` parameter** — The design says `kvWatch`, `kvGet`, and `jsSubscribe` gain an optional `domain` parameter. The existing `INATSClient` interface in `types.ts` (lines 24-38) has fixed signatures. The design must specify the updated TypeScript interface signatures (parameter names, types, optionality, and return types) for all three methods since all callers (SessionStore, HeartbeatMonitor, EventStore) are typed against this interface. Without these, a developer cannot update the interface and the implementations without breaking existing call sites.
   - **Doc**: "`INATSClient` gains an optional `domain` parameter as the last argument on JetStream methods"
   - **Code**: `INATSClient` at `mclaude-web/src/types.ts:24-38`; `NATSClient.kvWatch` at `nats-client.ts:138`; `NATSClient.jsSubscribe` at `nats-client.ts:81`. Current signatures have no `domain` parameter.

3. **`SessionStore` domain-qualified watch initialization is unspecified** — The design states "`SessionStore` reads `clusters` from `AuthStore` on initialization to open per-cluster KV watches," but does not specify: (a) how `SessionStore` receives `AuthStore` or the clusters list (currently `SessionStore` constructor takes only `natsClient` and `userId`, see `session-store.ts:17-19`), (b) whether the existing single-connection `startWatching()` method is replaced or supplemented, (c) what happens to the existing `mclaude-sessions` KV watch when upgraded from single-cluster to multi-cluster, and (d) how `handleKVUpdate` callback with the cluster metadata argument is routed — the design shows `sessionStore.handleKVUpdate(entry, cluster)` but `SessionStore` has no such method.
   - **Doc**: "Opens one domain-qualified KV watch per cluster the user has projects on (`mclaude-sessions` bucket, key prefix `{userId}.>`, domain `{jsDomain}`)"
   - **Code**: `SessionStore` constructor at `session-store.ts:17-19`; `startWatching()` at `session-store.ts:29`.

4. **`EventStore` direct connection ownership and initialization are unspecified** — The design says "`EventStore` reads `projects` to look up `jsDomain` and `directNatsUrl` for the active session's project" and "`EventStore` creates a new `NATSClient` instance and connects to the worker's `directNatsUrl`." The current `EventStore` constructor takes only `EventStoreOptions` (`natsClient`, `userId`, `projectId`, `sessionId`). The design does not specify: (a) the updated constructor signature, (b) where the `projects` list comes from (injected at construction? read from `AuthStore`?), (c) what the `directNatsUrl` and `jsDomain` lookup interface looks like, and (d) who calls `close()` on the secondary `NATSClient` when the session closes.
   - **Doc**: "`EventStore` reads `projects` to look up `jsDomain` and `directNatsUrl`... `EventStore` creates a new `NATSClient` instance"
   - **Code**: `EventStore` constructor/options at `event-store.ts:21-42`.

5. **Worker controller NKey is absent from `POST /admin/clusters` registration response** — The design specifies that the `POST /admin/clusters` response includes `leafCreds` (leaf node NKey credential) but the Worker Controller section says "The control plane generates a controller NKey pair during cluster registration... The NKey seed is included in the `POST /admin/clusters` registration response (alongside `leafCreds`)." The response schema in the Component Changes section does not include `controllerNkeySeed` or equivalent. A developer implementing the endpoint would not know to include it.
   - **Doc**: Response `{ id, name, jsDomain, leafCreds, hubNatsUrl, hubLeafPort, accountPubKey, status }` (Component Changes) vs. "The NKey seed is included in the `POST /admin/clusters` registration response (alongside `leafCreds`)" (Worker Controller section).

6. **`accounts` block required in hub NATS config, but design only shows `authorization` block** — The NATS account-only auth mode (JWTs signed by a shared account key, no operator) requires an `accounts` block in `nats.conf` that declares the account public key. The design shows only `authorization { account: $MCLAUDE_ACCOUNT_PUBLIC_KEY }`. This is NATS's simple user/password authorization syntax, not the JWT-based account authorization. The correct NATS config for account-JWT auth is `accounts: { MCLAUDE: { users: [{ jwt: ... }] } }` or the resolver-based approach. Without the correct NATS config syntax, a developer cannot configure hub or worker NATS.
   - **Doc**: "Hub NATS configuration — `authorization { account: $MCLAUDE_ACCOUNT_PUBLIC_KEY }` — configures NATS to verify user JWTs signed by the mclaude account NKey"
   - **Code**: Existing NATS config at `charts/mclaude/templates/nats-configmap.yaml` uses no `authorization` block at all — it currently has no account-level auth.

7. **Provisioning NATS subject routing requires leaf node subject-import configuration** — The design describes the control plane publishing `mclaude.clusters.{clusterId}.projects.provision` as a NATS request/reply, with the message routing "through the hub's leaf node to the target worker." NATS leaf nodes by default only import/export subjects matching `>` for core NATS, but JetStream subjects and _INBOX reply subjects require explicit import rules or `deny`/`allow` configuration in the leaf node `remotes` block. The design does not specify the leaf node `remotes` configuration for subject permissions (which subjects the worker exports to the hub and imports from the hub), which is required for the provisioning request/reply and worker controller subscription to work.
   - **Doc**: "Worker NATS — `leafnodes { remotes [{ url: ..., credentials: ... }] }`" (Data Model section shows no subject allow/deny)
   - **Code**: No existing leaf node config in codebase.

8. **User JWT permissions must include `$KV.*` subjects for domain-qualified KV watches** — The existing `UserSubjectPermissions` in `nkeys.go` (lines 22-28) grants `$KV.mclaude-projects.{userId}.>` and `$KV.mclaude-sessions.{userId}.>`. Domain-qualified JetStream KV accesses go through `$JS.{domain}.API.>` (API plane) and `$KV.{domain}.mclaude-sessions.{userId}.>` (or equivalent). The design does not specify how user JWT permissions are updated to allow cross-domain KV access through the hub, or whether additional `$JS.{domain}.API.>` allow entries are needed.
   - **Doc**: "User JWTs: Unchanged — signed by the account seed, verified by the account public key. Valid on hub and all workers."
   - **Code**: `UserSubjectPermissions` at `mclaude-control-plane/nkeys.go:22-28` grants only local `$KV` subjects, no cross-domain API subjects.

9. **Migration step 4 (`ALTER TABLE ... SET NOT NULL`) is not idempotent** — The design specifies that step 4 of the migration is `ALTER TABLE projects ALTER COLUMN cluster_id SET NOT NULL`. This DDL statement is not idempotent — running it a second time (e.g., on a restart after a crash between steps 3 and 4) will succeed only if the column is already NOT NULL, but it will also fail if rerun after auto-registration on an already-migrated database because Postgres will error if the column already has the NOT NULL constraint... Actually the failure mode is the opposite: Postgres silently accepts `SET NOT NULL` on an already-NOT NULL column. The real gap is that the design does not specify what happens if the control plane crashes between steps 3 and 4 and restarts — the backfill (step 3) has already run, but auto-registration (step 2) would attempt to re-insert the cluster row, which would fail with a unique constraint. The design does not specify how auto-registration is made idempotent (INSERT ... ON CONFLICT DO NOTHING, or check-first).
   - **Doc**: "Auto-registration runs — inserts a cluster row if the table is empty." Steps 2-4 described as procedural Go code in startup path.
   - **Code**: No existing migration mechanism beyond `db.Migrate()` which runs `schema` as a single DDL block at `db.go:53-56`.

10. **`DELETE /admin/clusters/{id}/members/{userId}` — 409 conflict check scope is underspecified** — The design says 409 is returned "if user has projects on this cluster with `status IN ('active', 'pending')`." The `projects` table currently has only `status` values `active` and `archived` (see `db.go:166`). The design introduces `pending` as a new status value in the error handling section, but does not specify where else `pending` is checked, how projects transition from `pending` to `active`, or what the full set of valid `status` values is after this change.
   - **Doc**: "409 if user has projects on this cluster with `status IN ('active', 'pending')`" and "Project record created in Postgres with `status = 'pending'`"
   - **Code**: `projects` table schema at `db.go:161-167` has only `'active'` as default; no enum or check constraint — but the `CreateProject` function hardcodes `'active'` at `db.go:121`. No `pending` status is defined anywhere.

11. **Single-cluster backwards compatibility: "no domain qualification" contradicts hub JetStream domain config** — The design states that in single-cluster mode, "The SPA connects to this NATS directly without domain qualification (since all KV and streams are local)." But the Data Model section specifies the hub NATS has `jetstream { domain: hub }`. If the JetStream domain is set to `hub`, then clients accessing KV buckets without domain qualification will use the default (empty) JetStream API prefix, but the server is now configured with domain `hub`. Existing clients and the SPA would still work because the server accepts both the domain-qualified and unqualified API subjects in single-node mode, but the design does not explain this behavior or specify how the SPA detects it is in single-cluster mode to skip domain qualification.
    - **Doc**: "SPA connects to this NATS directly without domain qualification" (Backwards Compatibility) vs. hub NATS having `domain: hub` (Data Model — NATS Configuration).
    - **Code**: Existing NATS configmap at `charts/mclaude/templates/nats-configmap.yaml` has no `domain` field currently.

## Round 3: Fixes applied (2026-04-16T04:30:00Z)

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | AuthTokens missing TS interface | Added ClusterInfo, ProjectInfo, and extended AuthTokens interfaces | factual |
| 2 | INATSClient domain signatures | False positive — already specified at lines 211-217 of doc | none |
| 3 | SessionStore multi-cluster init | Added constructor changes, handleKVUpdate method, single/multi detection | factual |
| 4 | EventStore constructor changes | Added jsDomain/directNatsUrl options, secondary client lifecycle | factual |
| 5 | POST /admin/clusters missing controllerNkeySeed | Added controllerNkeySeed, operatorJwt, accountJwt to response | factual |
| 6 | NATS auth config syntax wrong | Replaced authorization{account} with operator JWT + resolver MEMORY + preload | factual |
| 7 | Leaf node subject routing | Added clarification: NATS leaves import/export all subjects by default | factual |
| 8 | JWT permissions for cross-domain JS | Added $JS.*.API.> to UserSubjectPermissions pub/sub | factual |
| 9 | Auto-registration idempotency | Made all startup steps idempotent (ON CONFLICT, unconditional backfill) | factual |
| 10 | pending project status | Added CHECK constraint, heartbeat-triggered pending→active transition | factual |
| 11 | Single-cluster SPA detection | clusters.length === 1 → non-qualified watches; LOCAL_CLUSTER_ID for CP | factual |

## Run: 2026-04-16T08:00:00Z

**Gaps found: 3**

1. **`EventStore` method names are wrong — doc references `connect()` and `disconnect()` which do not exist** — The doc specifies "EventStore creates a secondary NATSClient in `connect()` and attempts a direct connection" and "The secondary client is closed in `EventStore.disconnect()`, called when the user navigates away from the session detail view." The actual `EventStore` in `mclaude-web/src/stores/event-store.ts` has `start()` and `stop()`, not `connect()` and `disconnect()`. A developer must decide whether to rename the existing methods (breaking all call sites), add new wrapper methods, or treat the doc's method names as the spec for a rename — none of which is stated.
   - **Doc**: "EventStore creates a secondary NATSClient in `connect()`... secondary client is closed in `EventStore.disconnect()`" (line 292)
   - **Code**: `event-store.ts:68` — `start(replayFromSeq?: number): void`; `event-store.ts:147` — `stop(): void`. No `connect()` or `disconnect()` method.

2. **`EventStoreOptions` does not specify how JWT/NKey credentials are supplied for the direct NATS connection** — The doc says "EventStore creates a secondary NATSClient in connect() and attempts a direct connection using the same JWT/NKey from AuthStore." `EventStoreOptions` gains `jsDomain?` and `directNatsUrl?` but not `jwt`, `nkeySeed`, or a reference to `AuthStore`. Without credentials, a `NATSClient.connect(opts: NATSConnectionOptions)` call cannot be made. The doc says the credentials come "from AuthStore" but does not specify whether `AuthStore` is injected into `EventStoreOptions`, or whether the caller extracts `jwt`/`nkeySeed` from `AuthTokens` and passes them directly. A developer cannot wire this without stopping to ask.
   - **Doc**: "EventStoreOptions gains optional `jsDomain?: string` and `directNatsUrl?: string` fields... attempts a direct connection using the same JWT/NKey from AuthStore" (line 292)
   - **Code**: `EventStoreOptions` at `event-store.ts:21-26` — fields: `natsClient`, `userId`, `projectId`, `sessionId`. `NATSConnectionOptions` at `types.ts:3-7` requires `url`, `jwt`, `nkeySeed`.

3. **`AuthStore.getProjects()` and `getClusters()` accessors are referenced but never specified** — The doc states "`AuthStore` stores these after login and exposes `getProjects()` and `getClusters()` accessors" (line 173). The current `AuthStore` class in `auth-store.ts` has no such methods. The doc gives the updated `AuthTokens` TypeScript interface but never specifies the `AuthStore` body changes: accessor return types, whether `_tokens` (typed as `AuthTokens | null`) is sufficient since `AuthTokens` now optionally carries `projects` and `clusters`, or whether separate fields are needed. Without the accessor signatures, code in `EventStore` and `SessionStore` that calls `authStore.getProjects()` cannot be typed.
   - **Doc**: "`AuthStore` stores these after login and exposes `getProjects()` and `getClusters()` accessors" (line 173)
   - **Code**: `AuthStore` at `mclaude-web/src/stores/auth-store.ts:14-113` — no `getProjects()` or `getClusters()` method. `_tokens: AuthTokens | null` is stored but no typed accessors exist.

## Round 4: Fixes applied (2026-04-16T04:45:00Z)

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | EventStore connect()/disconnect() wrong names | Changed to start()/stop() matching existing code | factual |
| 2 | EventStoreOptions missing JWT/NKey for direct connection | Added jwt/nkeySeed fields to EventStoreOptions interface | factual |
| 3 | AuthStore accessor signatures undefined | Added getProjects(), getClusters(), getJwt(), getNkeySeed() signatures | factual |

## Run: 2026-04-16T10:00:00Z

**Gaps found: 2**

1. **`ConversationVM` constructor not updated to accept `AuthStore`** — The doc says "The caller (conversation viewmodel) reads `jwt`, `nkeySeed`, `jsDomain`, and `directNatsUrl` from `AuthStore` when constructing `EventStoreOptions`." This means `ConversationVM` must hold a reference to `AuthStore` to call `authStore.getJwt()`, `authStore.getNkeySeed()`, etc. The current `ConversationVM` constructor takes `(eventStore, sessionStore, natsClient, userId, projectId, sessionId)` — no `AuthStore`. The doc does not update the `ConversationVM` constructor signature. A developer implementing the direct-connection wiring cannot determine how to thread `AuthStore` into the viewmodel without stopping to ask.
   - **Doc**: "The caller (conversation viewmodel) reads `jwt`, `nkeySeed`, `jsDomain`, and `directNatsUrl` from `AuthStore` when constructing `EventStoreOptions`." (line 317)
   - **Code**: `conversation-vm.ts:17-31` — constructor has no `AuthStore` parameter; `AuthStore` is not imported.

2. **`EventStore.start()` needs a `NATSClient` factory or concrete import — neither is specified** — The doc says "EventStore creates a secondary `NATSClient` in `start()`." `EventStore` currently imports only from `@/types` (the `INATSClient` interface). To instantiate a concrete `NATSClient`, `EventStore` must either import the `NATSClient` class directly or receive a factory via `EventStoreOptions`. The doc specifies neither. A developer cannot implement the secondary connection in `start()` without resolving this.
   - **Doc**: "When `directNatsUrl` is present, `EventStore` creates a secondary `NATSClient` in `start()` and attempts a direct connection" (line 317)
   - **Code**: `event-store.ts:1-18` — imports only `INATSClient` interface from `@/types`. `EventStoreOptions` (lines 21-26) has no factory field. `NATSClient` concrete class is in `nats-client.ts` but not imported.

## Round 5: Fixes applied (2026-04-16T05:00:00Z)

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | ConversationVM missing AuthStore parameter | Added authStore param and wiring code | factual |
| 2 | EventStore needs NATSClient factory | Added createNATSClient factory to EventStoreOptions | factual |

## Run: 2026-04-16T12:00:00Z

**Gaps found: 1**

1. **`SessionStore` per-cluster watch ownership is contradictory — pseudocode and spec text disagree** — The pseudocode snippet (lines 276-285) shows the SPA caller iterating clusters and calling `natsClient.kvWatch(...)` directly, passing `sessionStore.handleKVUpdate(entry, cluster)` as the callback. This means the loop lives in app-level glue code outside `SessionStore`. But line 289 says `startWatching()` "opens one domain-qualified KV watch per cluster" when `clusters` is provided — meaning the loop lives *inside* `SessionStore`. A developer implementing this must pick one architecture: (a) `startWatching()` does the per-cluster loops internally (using the `natsClient` and `clusters` passed to the constructor), or (b) the SPA caller does the loop externally and calls `handleKVUpdate` as a callback. These cannot both be true simultaneously. If (a), the pseudocode is wrong and should not appear in the doc. If (b), `startWatching()` does not need the `clusters` parameter and should not be described as opening per-cluster watches.
   - **Doc**: Lines 276-285 (pseudocode loop in SPA caller calling `sessionStore.handleKVUpdate(entry, cluster)`) vs. line 289 ("`SessionStore.startWatching()` opens one domain-qualified KV watch per cluster instead of a single non-qualified watch")
   - **Code**: `session-store.ts:29-78` — `startWatching()` currently opens a single non-qualified KV watch. No `handleKVUpdate` method. No `clusters` parameter on constructor.

## Round 6: Fixes applied (2026-04-16T05:10:00Z)

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | SessionStore pseudocode vs spec contradiction | Moved loop inside startWatching(); pseudocode is now internal to SessionStore | factual |

## Run: 2026-04-16T14:00:00Z

**Gaps found: 1**

1. **Multi-cluster `kvWatch` unsubscribers are not stored — `stopWatching()` cannot clean up per-cluster watchers** — The pseudocode inside `startWatching()` (lines 280-288) calls `this._natsClient.kvWatch(...)` for each cluster but does not capture the returned `() => void` unsubscribe function. The existing `session-store.ts` pattern (lines 35-59, 61-78) pushes every unsubscribe function into `this._unwatchers`, which `_stopWatching()` iterates to stop all active watchers. The multi-cluster loop as written drops each unsubscriber on the floor — the per-cluster KV watchers can never be stopped (on logout, re-auth, or navigation away). A developer implementing this verbatim would produce un-stoppable background subscriptions. The doc does not say to push to `_unwatchers` or any other collection.
   - **Doc**: Lines 280-288 — `this._natsClient.kvWatch('mclaude-sessions', ..., ...)` with no assignment of the return value.
   - **Code**: `session-store.ts:35` — `const unwatch1 = this.natsClient.kvWatch(...)` followed by `this._unwatchers.push(unwatch1)` at line 59. `_stopWatching()` at line 84-87 iterates `_unwatchers` to unsubscribe all watchers.

## Round 7: Fixes applied (2026-04-16T05:20:00Z)

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | kvWatch unsubscribers not captured | Added unsub capture and push to _unwatchers in pseudocode | factual |

## Run: 2026-04-16T06:00:00Z

**Gaps found: 1**

1. **`POST /admin/clusters` response is missing `controllerJwt` — worker controller cannot authenticate to NATS** — The registration response (line 92) includes `controllerNkeySeed` but not `controllerJwt`. A NATS credentials file (used by `nats.UserCredentials(credsFilePath)`) requires both the signed JWT and the NKey seed — the `.creds` format embeds both. The `IssueControllerJWT` function (lines 231-239) returns both `jwt string` and `seed []byte`. Without the JWT in the response, the operator receiving the registration result cannot write a valid credentials file for the worker controller, and the controller cannot authenticate to the worker NATS. A developer implementing the registration handler would include the seed but not the JWT, and the controller would fail to connect.
   - **Doc**: Line 92 — `Response (201): { id, name, jsDomain, leafCreds, controllerNkeySeed, hubNatsUrl, hubLeafPort, accountPubKey, operatorJwt, accountJwt, status }` — no `controllerJwt` field. Line 242 — "The controller connects with `nats.UserCredentials(credsFilePath)`."
   - **Code**: `IssueControllerJWT` signature (doc, lines 231-238) returns `(jwt string, seed []byte, err error)`. NATS `.creds` file format requires both. `controllerJwt` absent from response schema.

## Round 8: Fixes applied (2026-04-16T05:30:00Z)

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | controllerJwt missing from response | Changed to controllerCreds (full .creds file via FormatNATSCredentials); updated Worker Controller section | factual |

## Run: 2026-04-16T00:00:00Z (Round 9)

**Gaps found: 1**

1. **`leafCreds` generation is unspecified — contradiction between Security section and response description** — Line 92 states both `leafCreds` and `controllerCreds` are "NATS `.creds` file contents (JWT + NKey seed, formatted by `FormatNATSCredentials`)". But the Security section (line 517) describes `leaf_creds` as storing "the private key" (i.e., just the NKey seed, not a full JWT+seed credential). The Behavior description at line 93 says "Creates a leaf NKey pair" — a raw key pair, not a JWT-backed credential. If `leafCreds` truly is a full `.creds` file, the control plane must issue a NATS user JWT for the leaf node signed by the account key (analogous to `IssueControllerJWT` but for a leaf). No such function is specified — there is no `IssueLeafJWT` in the doc or in `nkeys.go`. A developer implementing the handler cannot determine: (a) whether `leaf_creds` stores a raw NKey seed or a full `.creds` file, (b) what subject permissions the leaf node JWT would carry, and (c) which function generates it.
   - **Doc**: Line 92 — both `leafCreds` and `controllerCreds` are "NATS `.creds` file contents (JWT + NKey seed, formatted by `FormatNATSCredentials`)". Line 93 — "Creates a leaf NKey pair." Line 517 — "`leaf_creds`: the private key is stored in Postgres."
   - **Code**: `nkeys.go:148-164` — `FormatNATSCredentials(jwt string, seed []byte)` requires a JWT, not just a seed. No `IssueLeafJWT` or equivalent function exists. `clusters.leaf_creds` column type is `TEXT NOT NULL` — consistent with either a raw seed string or a full `.creds` file.

## Round 9: Fixes applied (2026-04-16T05:40:00Z)

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | leafCreds format contradiction | Renamed to leafNkeySeed (raw NKey seed); clarified leaf auth uses NKey challenge-response, not JWT; updated Behavior and Security sections | factual |

## Run: 2026-04-16T00:00:00Z (Round 10)

**Gaps found: 1**

1. **Worker NATS leaf config key is `credentials` but Security section specifies an NKey seed file at a different path** — The worker NATS config block (Data Model section) shows `credentials: /etc/nats/leaf.creds`, which is the NATS config key for a full `.creds` file (JWT + NKey seed in PEM format). But the Security section states "Leaf node auth uses raw NKey challenge-response — no JWT is needed" and says the file is referenced as `/etc/nats/leaf.nk`. In NATS leaf node config, a raw NKey seed file is referenced via the `nkey` key (not `credentials`), and uses a `.nk` extension. The `credentials` key only applies to a full `.creds` file containing a JWT. These two sections disagree on both the config key name and the file path. A developer cannot determine whether to write `credentials: /etc/nats/leaf.creds` or `nkey: /etc/nats/leaf.nk` in the worker NATS config, and which K8s secret format to provision.
   - **Doc**: Data Model — Worker NATS config block (line 464): `credentials: /etc/nats/leaf.creds`. Security section (line 517): "references it in the NATS leaf node config (`credentials: /etc/nats/leaf.nk`)" and "Leaf node auth uses raw NKey challenge-response — no JWT is needed."
   - **Code**: No existing leaf node NATS config in codebase to verify against; gap is a direct internal contradiction in the document.

## Round 10: Fixes applied (2026-04-16T05:50:00Z)

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Worker NATS config uses credentials instead of nkey | Changed to `nkey: /etc/nats/leaf.nk` matching NKey-only auth | factual |

## Run: 2026-04-16T00:00:00Z (Round 11)

**Gaps found: 1**

1. **Round 10 fix incomplete — Security section and Helm values still use `credentials:` after rename to `nkey:`** — Round 10 changed the worker NATS config block (Data Model section, line 464) from `credentials: /etc/nats/leaf.creds` to `nkey: /etc/nats/leaf.nk`. But two other locations in the document were not updated and still conflict with this change:
   - **Line 517** (Security section prose): "references it in the NATS leaf node config (`credentials: /etc/nats/leaf.nk`)" — still uses the `credentials:` key name. A developer reading the Security section gets the wrong NATS config key.
   - **Line 388** (Helm worker chart `values.yaml` block): `credentials: ""  # path to leaf credentials file` — the Helm value is named `credentials`, but the NATS config template must render `nkey:`. If the Helm value is named `credentials`, it implies a `.creds` file, not a raw NKey seed file (`.nk`). The value key should be `nkey` (or `nkeyFile`) to match the config it drives.
   - **Doc**: Line 517 — "`credentials: /etc/nats/leaf.nk`"; Line 388 — `credentials: ""  # path to leaf credentials file`
   - **Code**: No existing leaf node config in codebase; gap is a direct internal contradiction within the document introduced by the partial Round 10 fix.

## Round 11: Fixes applied (2026-04-16T06:00:00Z)

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | Two stale `credentials:` references after rename | Fixed Security section prose and Helm values block to use `nkey:` | factual |

## Run: 2026-04-16T12:00:00Z (Round 12)

CLEAN — no blocking gaps found.

The two stale `credentials:` references from Round 11 are both resolved:
- Security section (line 517): now reads `nkey: /etc/nats/leaf.nk` — correct.
- Helm worker chart values block (line 388): now reads `nkey: ""  # path to leaf NKey seed file (.nk)` — correct.

No remaining `credentials:` references appear in leaf node configuration context. The document is internally consistent on NKey-based leaf node auth throughout.

## Round 12

CLEAN — no blocking gaps found.

### Result

**CLEAN** after 12 rounds, 19 total gaps resolved (19 factual fixes, 0 design decisions).
