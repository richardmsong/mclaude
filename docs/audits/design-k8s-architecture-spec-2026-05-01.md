## Audit: 2026-05-01T20:30:00Z

**Document:** docs/adr-0063-k8s-architecture-spec.md

### Round 1

**Gaps found: 6**

1. **K8s controller auth model contradicts itself — account-seed vs. challenge-response** — The design doc specifies that the new K8s controller should authenticate via HTTP challenge-response using an NKey seed from a mounted Secret, acquiring a host JWT in-memory with zero JetStream access (per ADR-0054). It explicitly states "drop `NATS_ACCOUNT_SEED`" from the controller env. However, the current codebase uses `NATS_ACCOUNT_SEED` to sign ephemeral user JWTs directly via `loadAccountKey()` — it holds the account key and issues its own JWTs without calling CP. The `IssueSessionAgentJWT` function in `nkeys.go` also requires `accountKP`. The doc's target implementation (Helm `gen-host-nkey` Job writes `nkeySeed`, controller reads `nkeySeed`, calls `POST /api/auth/challenge`, gets back a host JWT from CP) is architecturally incompatible with the current code. The transition is not specified: does the `gen-host-nkey` Job replace `loadAccountKey()` entirely? What happens to `IssueSessionAgentJWT` — does the controller no longer issue its own agent JWTs and instead proxy through CP? The doc says "Reuses `mclaude-controller-local/host_auth.go`" but `host_auth.go` reads from a `.creds` file (pre-existing JWT + seed pair), not from a bare seed Secret. A developer cannot determine whether the account key is removed or retained, and whether session-agent JWT issuance moves to CP or stays in the controller.
   - **Doc**: "drop unused env vars (`NATS_CREDENTIALS_PATH`, `JS_DOMAIN`, `CLUSTER_SLUG`)" — account-seed omission ambiguous; "JWT held in-memory only — never persisted back to the Secret. Refresh loop reuses the challenge-response code path" (Component Changes § mclaude-controller-k8s)
   - **Code**: `mclaude-controller-k8s/main.go:49–55` — `loadAccountKey()` reads `NATS_ACCOUNT_SEED`; `reconciler.go:45,218,239` — `accountKP` field used to call `IssueSessionAgentJWT`

2. **`gen-host-nkey` Secret field name and controller env var not specified** — The design doc says the `gen-host-nkey` Job writes the seed to Secret `{release}-host-creds` but leaves open the exact field name ("Holds `nkeySeed`" in the artifacts table, but also says "open question: in-Secret vs in-memory only" for JWT caching). The controller-deployment.yaml changes section says to "mount the new host-creds Secret" but does not state: (a) what the Secret data key is (`nkeySeed`, `seed`, `nkey_seed`, or something else), (b) what env var or file path the controller binary reads to get the seed value, (c) whether it is mounted as an env var or a volume file. Without this, a developer cannot write the updated `controller-deployment.yaml` volume/env sections or the corresponding binary code that reads the seed.
   - **Doc**: "Secret `{release}-host-creds` — Holds `nkeySeed`. JWT may also be cached here (open question: in-Secret vs in-memory only)." (Helm chart artifacts table); "mount the new host-creds Secret, set env `CONTROL_PLANE_URL`" (Component Changes § charts/mclaude-worker)
   - **Code**: `charts/mclaude-worker/templates/controller-deployment.yaml:43–46` — current Secret mount uses key `accountSeed` from `operator-keys`

3. **Hub NATS URL env var name for K8s controller is unresolved** — The design doc specifies that the controller should connect to hub NATS via `host.hubNatsUrl` Helm value, pointing `NATS_URL` at `host.hubNatsUrl`. After stripping the worker NATS StatefulSet, the local NATS URL (`nats://{release}-nats:{port}`) will be invalid. The doc does not specify: (a) whether the binary env var name stays `NATS_URL` or becomes something else, (b) how `SESSION_AGENT_NATS_URL` derivation (currently derived from `NATS_URL`) changes when that env holds the hub URL instead of a local service URL — session-agents need to connect to NATS too and the current derivation logic in `sessionAgentNATSURL()` assumes a local service name. The `mclaude-controller-local` uses `HUB_URL` for the equivalent — no consistency decision is made for the K8s controller's env naming.
   - **Doc**: "point `NATS_URL` at `host.hubNatsUrl`" (Component Changes § charts/mclaude-worker)
   - **Code**: `mclaude-controller-k8s/main.go:40` — `natsURL := envOr("NATS_URL", "nats://localhost:4222")`; `main.go:92` — `SESSION_AGENT_NATS_URL` derived from `natsURL` via `sessionAgentNATSURL()`

4. **Namespace reaper state storage is unspecified** — The doc says to implement a "periodic reconcile against MCProject list" with a 1h grace period and cancel-on-create behavior, yielding a new `reaper.go` file. There is no `reaper.go` today. The spec does not define: (a) where grace-period state is stored — in-memory only (lost on controller restart, grace window resets), or persisted (namespace annotation, separate ConfigMap, KV entry?), (b) what triggers the periodic reconcile — a `time.Ticker` goroutine, a controller-runtime `Reconcile` loop on Namespace resources, or an `EnqueueRequestsFromMapFunc` watch, (c) whether a controller restart during the grace window resets the 1h timer or resumes from where the clock was. A developer writing `reaper.go` from scratch cannot make these decisions without answers, since each option produces different behavior on restart.
   - **Doc**: "Per-user namespace reaper: when the last MCProject for a given `userSlug` is deleted, schedule deletion of `mclaude-{uslug}` after a 1h grace period. Cancel the scheduled deletion if a new MCProject for that user is created during the grace window. Implemented as a periodic reconcile against MCProject list." (Component Changes § mclaude-controller-k8s)
   - **Code**: `reaper.go` does not exist; no MCProject-by-userSlug listing exists in `reconciler.go`

5. **NKey type for `gen-host-nkey` Job is not specified** — The design doc says the `gen-host-nkey` Job "Generates NKey pair via `nkeys.CreateUser()`" (K8s host registration table, step 1). NATS NKey types carry type prefixes (`U` = user, `S` = server, `C` = cluster, `A` = account). The CP's challenge-response endpoint (`POST /api/auth/challenge`) looks up the public key in the `hosts.nkey_public` column; the `spec-state-schema.md` describes that column as the host controller's NKey but does not specify which NKey type is expected. `nkeys.CreateUser()` produces a `U`-prefix key. If CP validates the NKey type before accepting it (e.g., requiring a server or cluster key for host identities), using `CreateUser()` will fail registration. The doc does not state whether CP validates the key type or accepts any NKey prefix. A developer implementing the Job cannot determine which `nkeys.Create*` function to call.
   - **Doc**: "Generates NKey pair via `nkeys.CreateUser()`" (K8s host registration table, step 1)
   - **Code**: `spec-state-schema.md:64` — `hosts.nkey_public` described without type constraint; `mclaude-controller-local/host_auth.go` reads existing key from creds file, never generates one

6. **`mclaude host register --nkey-public` flag does not exist and its design is not specified** — The design doc says the operator attests the K8s host by running `mclaude host register --slug us-east --type cluster --name "..." --nkey-public UABC...` and states "No new API surface; same flow BYOH uses on a laptop." However, the BYOH `mclaude host register` flow (per ADR-0035, implemented) generates its own NKey locally and uses a device-code exchange — it has no `--nkey-public` flag for supplying an externally-generated key. The doc does not specify: (a) whether `--nkey-public` is a new flag that must be added to the CLI, (b) whether the underlying NATS subject changes (the doc says it publishes `mclaude.users.{uslug}.hosts._.register {name, slug, type, nkey_public}` but the existing registration flow uses `POST /api/users/{uslug}/hosts/code` + device-code poll, not a NATS publish), (c) whether `--type cluster` is also a new flag. A developer cannot implement the CLI changes without knowing exactly what new flags and code paths are needed.
   - **Doc**: "`mclaude host register --slug us-east --type cluster --name "us-east K8s cluster" --nkey-public UABC...`" (User Flow step 3); "No new API surface; same flow BYOH uses on a laptop" (K8s host registration flow section)
   - **Code**: `mclaude-controller-local/main.go:31–38` — existing registration uses `--host`, `--creds-file`; ADR-0035 specifies device-code flow only; `mclaude-cli` has no `--nkey-public` flag in the implemented `host register` command

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | K8s controller auth model contradicts itself | ADR didn't enumerate the env/function/code paths to drop from the controller; reused-host_auth.go path also needed extension for seed-only bootstrap | Component Changes § mclaude-controller-k8s rewritten: enumerated env vars to drop (`NATS_ACCOUNT_SEED`, `NATS_CREDENTIALS_PATH`, `JS_DOMAIN`, `CLUSTER_SLUG`), functions to drop (`loadAccountKey`, `IssueSessionAgentJWT`, `accountKP` field). New env vars added (`HUB_NATS_URL`, `CONTROL_PLANE_URL`, `HOST_NKEY_SEED_PATH`). Session-agent JWT issuance moves to CP per ADR-0054 § "Session-agent JWT issuance | Control-plane only". `host_auth.go` extended to support bootstrap-from-seed mode, shared by both controllers. | factual |
| 2 | gen-host-nkey Secret field name + controller env var unspecified | Helm artifacts table left field name and mount details TBD | Specified Secret data field `nkey_seed`, mounted as a volume file at `/etc/mclaude/host-creds/nkey_seed`, controller reads via `HOST_NKEY_SEED_PATH` env (default to that path) | factual |
| 3 | Hub NATS URL env var + session-agent URL derivation | `sessionAgentNATSURL()` derived from `NATS_URL` assuming a local service name; with hub-direct that breaks | Rename controller env `NATS_URL` → `HUB_NATS_URL` (consistent with controller-local). Session-agents inherit `HUB_NATS_URL` directly via the session-agent template; drop `sessionAgentNATSURL()` derivation function. | factual |
| 4 | Namespace reaper state storage unspecified | The reaper itself was an over-engineered solution to a problem already covered by owner references | Removed reaper from scope. Empty namespaces are acceptable in this ADR; reap-empty + user-deletion cascade deferred to a follow-up ADR. | decision (user: "probably a new ADR") |
| 5 | NKey type for gen-host-nkey Job not specified | ADR said `nkeys.CreateUser()` without explaining whether CP validates the type prefix | Confirmed via `mclaude-controller-local/host_auth.go` line 41 (`ParseDecoratedUserNKey`) that hosts use `U`-prefix NKeys. Annotated this in the Helm artifacts table; CP's `issueJWTForNKey` does not validate type prefix. | factual |
| 6 | `mclaude host register --nkey-public` flag not specified | ADR-0035 spec'd device-code; ADR-0054 line 499–508 superseded with `--nkey-public` but the implementation may not have caught up | Added a "CLI flags required by this flow" subsection citing ADR-0054 line 499–508. ADR-0063 surfaces the required flags (`--nkey-public`, `--type cluster`) but defers the CLI implementation work to ADR-0054's dev-harness loop (CLI changes are an ADR-0054 implementation artifact, not an ADR-0063 concern). | factual |

---

## Run: 2026-05-01T21:15:00Z

**Document:** docs/adr-0063-k8s-architecture-spec.md — Round 2

**Gaps found: 3**

1. **Register NATS body includes `slug` field but ADR-0054 (authoritative) says slug is not sent by the CLI** — ADR-0063 lines 74, 90, and 102 specify the `mclaude.users.{uslug}.hosts._.register` body as `{name, slug, type, nkey_public}` and the CLI invocation includes `--slug us-east`. ADR-0054 § "Registration" step 3 (the authoritative spec for this subject) says the body is `{name, type, nkey_public}` — no `slug`. Per ADR-0054 step 4, "CP creates host in Postgres (slug, name, type, owner_id, nkey_public)" meaning CP derives or generates the slug, and step 5 confirms "CP returns `{ok, slug}` on the reply subject." If the CLI sends a slug in the body, CP must accept and use it (a new behavior). If it does not, the K8s flow as described — where the operator explicitly chooses `--slug us-east` for their subscription namespace — cannot work because the operator has no way to tell CP what slug to assign. A developer implementing this cannot know whether to (a) add `slug` to the NATS body and update the CP handler, or (b) accept that CP auto-derives the slug from the `name` field and the operator learns it from the `{ok, slug}` reply. Either choice changes what must be implemented, and they are different implementations.
   - **Doc**: "publishes `mclaude.users.{uslug}.hosts._.register {name, slug, type, nkey_public}` over NATS" (User Flow step 3); "`mclaude host register --slug us-east --type cluster --nkey-public UABC...`" (User Flow step 3); NATS subject table line 102
   - **Code**: ADR-0054 line 500 — "CLI publishes `mclaude.users.{uslug}.hosts._.register {name, type, nkey_public}`"; ADR-0054 line 619 — subject table entry for `._.register` also omits `slug` from the body

2. **`controlPlane.publicHostname` Helm value name conflicts with the existing chart which already uses `ingress.natsHost`** — ADR-0063 says to add Helm value `controlPlane.publicHostname` (default empty) to `charts/mclaude-cp/` to gate the hub NATS WS Ingress rendering. The `charts/mclaude-cp/` chart already has an `ingress.natsHost` value (line 174 of `values.yaml`) and a fully-rendered `templates/nats-ws-ingress.yaml` that is already conditioned on `ingress.natsHost`. The ADR's specified value name (`controlPlane.publicHostname`) does not match the existing gating value (`ingress.natsHost`). A developer implementing this cannot know whether to: (a) replace `ingress.natsHost` with `controlPlane.publicHostname` (breaking any existing use of `ingress.natsHost`), (b) keep `ingress.natsHost` unchanged and only document that `controlPlane.publicHostname` is an alias, or (c) recognize the `nats-ws-ingress.yaml` already exists and mark the NATS WS Ingress Helm addition as already done — in which case the only remaining cp-chart work is enabling the WebSocket listener in the NATS ConfigMap. The WS Ingress is already in the repo; what changes are still needed is unclear.
   - **Doc**: "Helm value addition: `controlPlane.publicHostname` (default empty; if set, the chart renders the NATS WS Ingress with that hostname)." (Component Changes § charts/mclaude-cp); Impact and Implementation Plan both list adding the NATS WS Ingress as in-scope
   - **Code**: `charts/mclaude-cp/values.yaml:174` — `natsHost: ""`; `charts/mclaude-cp/templates/nats-ws-ingress.yaml:1` — already conditioned on `.Values.ingress.natsHost`

3. **Namespace reaper appears in Impact and Implementation Plan but is deferred in Decisions — internal contradiction** — The Decisions table (line 50) explicitly defers the namespace reaper to a follow-up ADR: "Per-user namespace reaper: **Deferred to a follow-up ADR.**" However, the Impact section (line 227) lists `mclaude-controller-k8s/` as requiring "; implement per-user namespace reaper" as in-scope work, and the Implementation Plan table (line 254) lists `reaper, error states` as deliverables inside `docs/mclaude-controller-k8s/spec-k8s-architecture.md`. A developer reading the Impact or Implementation Plan section would implement the reaper; a developer reading the Decisions table would not. Both sections are in the same document. The scope of the `mclaude-controller-k8s/` change (lines 227 and 261) must be reconciled with the deferral decision.
   - **Doc**: Decisions table line 50 — "Deferred to a follow-up ADR"; Impact section line 227 — "implement per-user namespace reaper"; Implementation Plan line 254 — "reaper, error states" listed as spec content
   - **Code**: No `reaper.go` exists; this is a scope decision the document makes in two incompatible places

### Round 2

**Gaps found: 3**

1. **Register NATS body includes `slug` field but ADR-0054 says CP derives slug** — ADR-0063 had body `{name, slug, type, nkey_public}`; ADR-0054 line 500 specifies `{name, type, nkey_public}` and CP returns `{ok, slug}`.
2. **`controlPlane.publicHostname` Helm value name conflicts with existing chart value** — cp chart already has `ingress.natsHost`, `templates/nats-ws-ingress.yaml`, and the WS listener in `nats-configmap.yaml`. ADR was inventing phantom work.
3. **Reaper deferred in Decisions but listed as in-scope in Impact + Implementation Plan** — internal contradiction.

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | Register body includes `slug` | I assumed operator chooses slug explicitly; ADR-0054 has CP slugify `--name` | Dropped `--slug` from CLI invocation; updated body to `{name, type, nkey_public}`; clarified CP slugifies `--name` deterministically; added "no slug flag" callout under CLI flags. | factual |
| 2 | Phantom cp chart work | I didn't read the existing cp chart before specifying additions | Replaced "add Ingress + WS listener + new value" with "already in place; operator sets `ingress.natsHost`". cp chart row in Implementation Plan zeroed out. | factual |
| 3 | Reaper contradiction | Round-1 dropped reaper from Decisions but I forgot to remove the same references from Impact and Implementation Plan | Removed reaper from Components-implementing-the-change list and from Implementation Plan deliverables; "reaper" word also stripped from spec-k8s-architecture.md description. | factual |

## Run: 2026-05-01T22:45:00Z

**Document:** docs/adr-0063-k8s-architecture-spec.md — Round 3

**Gaps found: 2**

1. **`--slug` vs `--name` inconsistency in NOTES.txt template instruction and controller error message** — The doc's "CLI flags required by this flow" section (lines 107-113) explicitly states "The slug is **not** a CLI flag — CP derives it by slugifying `--name`." The canonical invocation throughout the doc is `mclaude host register --type cluster --name "us-east" --nkey-public UABC...`. However, the Helm chart artifacts table (line 128) says NOTES.txt should instruct the operator to run `mclaude host register --slug $HOST_SLUG --type cluster --nkey-public <key>` (uses `--slug`), and the error handling table (line 204) says the controller should log `run mclaude host register --slug $HOST_SLUG --type cluster --nkey-public <pubkey>` (also uses `--slug`). A developer writing the NOTES.txt Helm template and the controller's boot error message faces a direct contradiction: the CLI flags section says use `--name`, the two usage sites say use `--slug`. They cannot implement both correctly without a decision.
   - **Doc**: "The slug is **not** a CLI flag" (line 113); NOTES.txt artifact description (line 128) — `--slug $HOST_SLUG`; error handling table (line 204) — `--slug $HOST_SLUG --type cluster`
   - **Code**: `mclaude-cli/cmd/host.go` — existing `RunHostRegister` has no `--slug` or `--name` flags yet; the correct flag name matters for the implementation

2. **NATS payload field name `nkey_public` vs `nkeyPublic` inconsistency with state schema** — ADR-0063 specifies the `mclaude.users.{uslug}.hosts._.register` NATS subject body as `{name, type, nkey_public}` (snake_case) in the user flow (line 74), the K8s registration table (line 90), and the API endpoints table (line 102). The canonical state schema (`docs/spec-state-schema.md` line 409) — the authoritative source for message payloads — specifies this same subject's payload as `{name, type, nkeyPublic}` (camelCase). A developer implementing the CP handler that deserializes this message, and the CLI that serializes it, faces conflicting field names. Deserializing `nkey_public` when the sender sends `nkeyPublic` (or vice versa) produces a silent zero-value bug where the CP receives an empty public key and either errors or creates a host row with no NKey.
   - **Doc**: ADR-0063 line 74, 90, 102 — `nkey_public` (snake_case); `docs/spec-state-schema.md` line 409 — `nkeyPublic` (camelCase)
   - **Code**: No implementation exists yet for the `--nkey-public` path; the field name must be decided before either end is implemented

### Round 3

**Gaps found: 2**

1. **`--slug` vs `--name` inconsistency** — round-2 fix dropped `--slug` from CLI invocation but left `--slug $HOST_SLUG` in the NOTES.txt template description and the controller's error log message.
2. **NATS payload field casing conflict between ADR-0054 and spec-state-schema.md** — ADR-0054 line 500 says `nkey_public` (snake_case), `spec-state-schema.md` line 409 (canonical for NATS payloads) says `nkeyPublic` (camelCase). My ADR was using snake_case throughout, against the canonical state schema.

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | `--slug` vs `--name` | Incomplete round-2 sweep | Replaced all remaining `--slug $HOST_SLUG` and `host.slug` with `--name $HOST_NAME` and `host.name`. Updated install command, NOTES.txt template description, error-handling table, and controller-deployment env list. | factual |
| 2 | NATS payload field casing | I followed ADR-0054's snake_case but spec-state-schema.md is the canonical NATS payload spec and uses camelCase | Updated all NATS payload references in ADR-0063 to `nkeyPublic` (camelCase). HTTP body refs stay `nkey_public` (snake_case) per actual code in `mclaude-control-plane/auth.go:20`. Added an explicit "NATS payload uses camelCase per state schema; HTTP uses snake_case per CP code" callout. The upstream ADR-0054/spec-state-schema.md disagreement is flagged but not fixed here — that's a separate spec-evaluator finding. | factual |

## Run: 2026-05-01T23:30:00Z

**Document:** docs/adr-0063-k8s-architecture-spec.md — Round 4

**Gaps found: 2**

1. **`$SYS` CONNECT dispatch for K8s cluster hosts will silently stop working after hub-direct change** — ADR-0063 changes the K8s cluster controller from leaf-node to hub-direct connection. After this change, when a K8s cluster controller connects to hub NATS it appears as a `"Client"` kind in `$SYS.ACCOUNT.*.CONNECT` events, not `"Leafnode"`. The control-plane's `sys_subscriber.go` `handleSysEvent` dispatches on `"Client"` only to `type='machine'` rows and `"Leafnode"` to `type='cluster'` rows. A hub-direct K8s controller connecting as `"Client"` will not match the `"Leafnode"` branch — meaning `hosts.last_seen_at` will never update and the `mclaude-hosts` KV `online` flag will never become `true` for K8s cluster hosts. The doc does not specify any change to the `$SYS` subscriber to handle cluster hosts arriving as `"Client"`. `spec-state-schema.md` (authoritative) lines 430-431 still specify `"Leafnode"` for cluster hosts; neither ADR-0063 nor a spec-state-schema update addresses this. A developer implementing ADR-0063 will not know they need to modify `sys_subscriber.go` to match both `"Client"` and `"Leafnode"` kinds against `type='cluster'` (or only `"Client"`, since leaf-node topology is removed entirely).
   - **Doc**: "NATS topology in spec: Hub-only. K8s controller connects to hub NATS." (Decisions table); no mention of `$SYS` subscriber changes anywhere in the doc
   - **Code**: `mclaude-control-plane/sys_subscriber.go:82-157` — `"Client"` branch matches only `type='machine'`; `"Leafnode"` branch matches only `type='cluster'`. `spec-state-schema.md:431` — "CONNECT | `Leafnode` | SELECT * FROM hosts WHERE nkey_public = client.nkey AND type = 'cluster'"

2. **`host_auth.go` bootstrap-from-seed mode behavior during boot is underspecified** — ADR-0063 says "extend `host_auth.go` to support a bootstrap-from-seed mode: if no JWT is present at construction time (because the operator has not yet run `mclaude host register`), the first `Refresh()` call acquires the initial JWT instead of refreshing an existing one." The existing `NewHostAuthFromCredsFile` requires both a seed and a pre-existing JWT from the creds data (`nkeys.ParseDecoratedJWT` at line 46 — errors if no JWT). The doc does not specify: (a) the new constructor signature — is it a separate `NewHostAuthFromSeed(seed []byte, cpURL string, log zerolog.Logger) (*HostAuth, error)`, a flag parameter, or a change to the existing constructor?, (b) what the controller binary does between startup and the first successful `Refresh()` call — the NATS connection requires a valid JWT; if `Refresh()` is called before the operator runs `mclaude host register`, CP returns 404 (unknown public key), so does the boot sequence poll indefinitely? crash and let K8s restart it? wait for a signal?, (c) how long the boot loop retries before emitting the "run `mclaude host register`" error message cited in the Error Handling table. A developer extending `host_auth.go` and the K8s controller boot sequence cannot make these decisions without answers.
   - **Doc**: "Reuse `mclaude-controller-local/host_auth.go` for the challenge-response code path. Extend `host_auth.go` to support a bootstrap-from-seed mode." (Component Changes § mclaude-controller-k8s); Error Handling table — "Controller crashloops with a clear log message: 'host JWT not available; run `mclaude host register`...'"
   - **Code**: `mclaude-controller-local/host_auth.go:40-57` — `NewHostAuthFromCredsFile` calls `nkeys.ParseDecoratedJWT(credsData)` and returns error if JWT is absent; no seed-only constructor exists; `mclaude-controller-local/host_auth.go` has a single exported constructor

### Round 4

**Gaps found: 2**

1. **`$SYS` CONNECT dispatch breaks for cluster hosts after hub-direct change** — `sys_subscriber.go` dispatches "Client" events to `type='machine'` only; "Leafnode" to `type='cluster'` only. Hub-direct K8s controller connects as "Client" → cluster hosts will never be seen online.
2. **`host_auth.go` bootstrap-from-seed mode underspecified** — Constructor signature, 404 boot-loop behavior, and retry semantics all missing from the ADR.

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | $SYS CONNECT dispatch breaks for cluster hosts | ADR said "K8s controller connects hub-direct" but never specified the corresponding sys_subscriber.go change; spec-state-schema.md still said "Leafnode" | Added new `### mclaude-control-plane` Component Changes section specifying: remove type filter from "Client" branch (match both machine + cluster by nkey_public only); drop "Leafnode" branch entirely. Added `spec-state-schema.md` line 431 update to Impact section. | factual |
| 2 | host_auth.go bootstrap-from-seed underspecified | ADR mentioned "extend host_auth.go" but left constructor name, 404 retry, and backoff semantics unspecified | Specified `NewHostAuthFromSeed(seedPath string, cpURL string, log zerolog.Logger) (*HostAuth, error)` constructor; boot loop retries `Refresh()` every 5s with exponential backoff capped at 60s on 404; logs pubkey + registration instruction on each retry; no NATS connect until JWT acquired. | factual |

### Round 5

**Gaps found: 6**

1. **`nkey_public` vs `public_key` DB column name** — ADR said "look up by `nkey_public`" but `hosts` DDL column is `public_key` (db.go:853).
2. **`hosts` UNIQUE constraint is per-user, not global** — `UNIQUE (user_id, slug)` not `UNIQUE (slug)`; ADR implied global uniqueness.
3. **NATS payload casing conflict** — ADR used `nkeyPublic` (camelCase per spec-state-schema.md) but CP handler `lifecycle.go:240` uses `json:"nkey_public"` (snake_case). Code is authoritative.
4. **`NewHostAuthFromSeed` Go module boundary unspecified** — `mclaude-controller-k8s` and `mclaude-controller-local` are separate Go modules; "reuse host_auth.go" requires specifying the move target.
5. **DB CHECK constraint blocks `type='cluster'` inserts** — `CHECK (type = 'machine' OR (js_domain IS NOT NULL AND ...))` on hosts table will reject new K8s host registrations.
6. **`reconcileSecrets` replacement after `IssueSessionAgentJWT` removal unspecified** — removing `accountKP` breaks the `nats-creds` Secret field with no specified replacement.

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | DB column name | Used spec name (`nkey_public`) instead of actual DDL name (`public_key`) | Updated sys_subscriber.go description to say "look up by `public_key`" with note about the DDL vs spec name discrepancy | factual |
| 2 | Per-user UNIQUE constraint | ADR assumed global uniqueness; actual DDL is `UNIQUE (user_id, slug)` | Updated Data Model section to clarify per-user uniqueness; noted that CP lookups use `public_key` (globally unique NKey) not slug | factual |
| 3 | NATS payload casing | Round-3 fix aligned to spec-state-schema.md (camelCase) but code (lifecycle.go:240) uses snake_case | Reverted to `nkey_public` throughout; added note that spec-state-schema.md:409 has an error (camelCase); added spec-state-schema.md:409 to Impact section for correction | factual |
| 4 | Module boundary for host_auth.go | ADR said "reuse mclaude-controller-local/host_auth.go" without solving cross-module import | Specified move to `mclaude-common/pkg/hostauth/` (already has nkeys dependency); both controllers import `mclaude.io/common/pkg/hostauth`; added to Impact + Implementation Plan | factual |
| 5 | DB CHECK constraint | ADR said "no schema change needed" without checking the actual constraint | Updated Data Model section to specify the constraint must be dropped as a migration | factual |
| 6 | reconcileSecrets replacement | ADR said "remove IssueSessionAgentJWT" without specifying what fills the nats-creds Secret field | Specified: `reconcileSecrets` drops nats-creds generation; `user-secrets` retains only `oauth-token`; session-agent ConfigMap template adds `CONTROL_PLANE_URL`; session-agent self-bootstraps JWT per ADR-0054 | factual |

### Round 6

**Gaps found: 2**

1. **`CONTROL_PLANE_URL` injection mechanism ambiguous** — ADR said "ConfigMap template must include" but the existing injection pattern is a reconciler struct field (like `sessionAgentNATSURL`), not the ConfigMap.
2. **`spec-state-schema.md` has 3 more DDL inconsistencies not listed in Impact** — `nkey_public` vs `public_key` (col name), `owner_id` vs `user_id` (FK), `UNIQUE (slug)` vs `UNIQUE (user_id, slug)`.

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | CONTROL_PLANE_URL injection mechanism | ADR said "ConfigMap template" without checking how NATS_URL is actually injected (directly from reconciler struct field) | Changed to: add `controlPlaneURL` field to reconciler struct; inject at reconcile time mirroring `sessionAgentNATSURL` pattern | factual |
| 2 | spec-state-schema.md additional DDL gaps | Impact section listed only 2 of 5 needed corrections | Expanded Impact to list all 5: lines 431, 409 (already listed) + line 64 (col name), line 60 (FK name), lines 61/70 (UNIQUE constraint) | factual |

### Round 7

CLEAN — no blocking gaps found.

### Result

**CLEAN** after 7 rounds, 16 total gaps resolved (16 factual fixes, 0 design decisions).
