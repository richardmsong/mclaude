# Spec: Common Library

## Role

`mclaude-common` is a shared Go library (module `mclaude.io/common`) that provides typed slug identifiers, NATS subject/KV key construction helpers, NATS credential/key management utilities, and shared KV payload and lifecycle event types used by all mclaude components. It enforces compile-time type safety: every subject and key helper accepts only typed slug wrappers, making it impossible to pass a raw string where a validated slug is expected.

## Interfaces

### Package `slug`

Typed slug wrappers and the slugification algorithm defined by ADR-0024.

**Typed wrappers** (each is a distinct `type T string`, not an alias):

| Type            | Kind constant   | Fallback prefix |
|-----------------|-----------------|-----------------|
| `UserSlug`      | `KindUser`      | `u-`            |
| `ProjectSlug`   | `KindProject`   | `p-`            |
| `SessionSlug`   | `KindSession`   | `s-`            |
| `HostSlug`      | `KindHost`      | `h-`            |
| `ClusterSlug`   | `KindCluster`   | `c-`            |

**Slug algorithm (`Slugify`):**

1. Lowercase the input.
2. NFD Unicode decomposition (via `golang.org/x/text/unicode/norm`).
3. Strip combining marks (Unicode category Mn) -- accented characters become their base form.
4. Replace runs of non-`[a-z0-9]` characters with a single hyphen.
5. Trim leading and trailing hyphens.
6. Truncate to 63 characters (re-trimming any trailing hyphen from the cut).

Returns empty string if no valid characters remain. Callers use `ValidateOrFallback` to handle empty/reserved results.

**Validation (`Validate`):**

Charset: `[a-z0-9][a-z0-9-]{0,62}`. Max length 63. Must not start with `_` or `-`. Rejects the reserved-word blocklist: `users`, `hosts`, `projects`, `sessions`, `clusters`, `api`, `events`, `lifecycle`, `quota`, `terminal`, `create`, `delete`, `input`, `config`, `control`. Returns typed errors: `ErrEmpty`, `ErrCharset`, `ErrTooLong`, `ErrLeadingUnderscore`, `ErrReserved`.

**Fallback (`ValidateOrFallback`):**

When a candidate fails validation, generates a deterministic slug `{prefix}-{6 base32 chars}` using the first 4 bytes of a UUID seed. The base32 alphabet (`a-z2-7`, no padding) is always within the slug charset.

**User slug derivation (`DeriveUserSlug`):**

Produces `{slugify(displayName)}-{first domain segment}` from a display name and email. Falls back to the email local-part when the display name slugifies to empty.

**MustParse helpers:** `MustParseUserSlug`, `MustParseProjectSlug`, `MustParseSessionSlug`, `MustParseHostSlug`, `MustParseClusterSlug` -- validate and return the typed wrapper or panic.

### Package `subj`

Typed NATS subject and KV key builders. Every function accepts only typed slug wrappers from `pkg/slug`. See `spec-state-schema.md` for canonical subject and key formats.

ADR-0035 (Unified Host Architecture) extends ADR-0024 by inserting `.hosts.{hslug}.` between the user and project levels in all project-scoped subjects, KV keys, and JetStream filter constants.

**JetStream stream filter (ADR-0054 — consolidated sessions stream):**

| Constant                  | Pattern                                                      |
|---------------------------|--------------------------------------------------------------|
| `FilterMclaudeSessions`   | `mclaude.users.*.hosts.*.projects.*.sessions.>`              |

The three legacy filters (`FilterMclaudeAPI`, `FilterMclaudeEvents`, `FilterMclaudeLifecycle`) are removed. ADR-0054 consolidates all session activity (events, commands, lifecycle) into a single per-user stream `MCLAUDE_SESSIONS_{uslug}` with the unified `sessions.>` subject hierarchy.

**User-scoped subject helpers (no host level):**
`UserAPIProjectsCreate`, `UserAPIProjectsUpdated`, `UserQuota`

**Host-scoped subject helpers:**
`UserHostStatus(u UserSlug, h HostSlug) string` -- host presence heartbeat.

**Host-scoped provisioning subject helpers (ADR-0054 — fan-out from CP to host controllers):**

| Helper                      | Signature              | Pattern                                                                  |
|-----------------------------|------------------------|--------------------------------------------------------------------------|
| `HostUserProjectsCreate`    | `(h HostSlug, u UserSlug, p ProjectSlug)` | `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create`  |
| `HostUserProjectsDelete`    | `(h HostSlug, u UserSlug, p ProjectSlug)` | `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.delete`  |

CP publishes to these subjects after validating the HTTP project creation/deletion request. Host controllers receive them via their `mclaude.hosts.{hslug}.>` subscription. See ADR-0054 §BYOH Host / Platform Controller for the fan-out scheme.

**User+host+project-scoped subject helpers (ADR-0054 — consolidated `sessions.>` hierarchy):**
`UserHostProjectSessionsCreate`, `UserHostProjectSessionsInput`, `UserHostProjectSessionsControl`, `UserHostProjectSessionsConfig`, `UserHostProjectSessionsDelete`, `UserHostProjectSessionsEvents`, `UserHostProjectSessionsLifecycle`

All accept `(u UserSlug, h HostSlug, p ProjectSlug, ...)` -- the `h HostSlug` parameter is required for all project-scoped subjects.

**Terminal subject helper (retained per ADR-0054 — terminal subjects are not renamed):**
`UserHostProjectAPITerminal(u UserSlug, h HostSlug, p ProjectSlug) string` — returns `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal`.

**KV key helpers (ADR-0054 — literal type-tokens, per-user buckets):**

| Helper           | Signature    | Pattern                                                        | Bucket                       |
|------------------|--------------|----------------------------------------------------------------|------------------------------|
| `SessionsKVKey`  | `(h, p, s)`  | `hosts.{hslug}.projects.{pslug}.sessions.{sslug}`              | mclaude-sessions-{uslug}     |
| `ProjectsKVKey`  | `(h, p)`     | `hosts.{hslug}.projects.{pslug}`                               | mclaude-projects-{uslug}     |
| `HostsKVKey`     | `(h)`        | `{hslug}`                                                      | mclaude-hosts (shared)       |

Key format changes from ADR-0054: Keys now include literal type-tokens (`hosts.`, `projects.`, `sessions.`) to form a hierarchical, human-readable structure. The user slug prefix is removed from keys because buckets are now per-user (`mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}`). `HostsKVKey` takes only a host slug — the shared `mclaude-hosts` bucket uses flat `{hslug}` keys (read access scoped per-host in user JWT). `JobQueueKVKey` is removed — job queue KV was eliminated by ADR-0044; quota-managed sessions use the session KV with extended fields.

Note: `ClustersKVKey` and `LaptopsKVKey` (pre-ADR-0035) have been removed. Use `HostsKVKey` with a typed `HostSlug`.

### Package `nats`

NATS credential formatting and key management helpers shared across mclaude components. Moved from `mclaude-control-plane` per ADR-0035 so the CLI can reuse `FormatNATSCredentials` for BYOH bootstrap.

**`FormatNATSCredentials(jwt string, seed []byte) []byte`**

Formats a NATS credentials file (`.creds` format) from a JWT and NKey seed. The output is the standard NATS credentials file format understood by `nats.UserCredentials()`.

**`GenerateOperatorAccount(operatorName, accountName string) (*OperatorAccount, error)`**

Generates a fresh operator + account NKey pair and the corresponding JWTs for the NATS 3-tier trust chain (operator → system account → application account). The operator JWT is self-signed; the account JWT is signed by the operator. Called once by the `mclaude-cp` init-keys Helm Job.

**`OperatorAccount` struct:**

| Field                 | Type     | Description                                           |
|-----------------------|----------|-------------------------------------------------------|
| `OperatorSeed`        | `[]byte` | Operator NKey seed                                    |
| `OperatorPublicKey`   | `string` | Operator NKey public key                              |
| `AccountSeed`         | `[]byte` | Application account NKey seed                         |
| `AccountPublicKey`    | `string` | Application account NKey public key                   |
| `OperatorJWT`         | `string` | Self-signed operator JWT                              |
| `AccountJWT`          | `string` | Application account JWT (signed by operator)          |
| `SysAccountSeed`      | `[]byte` | System account NKey seed (used by CP to publish `$SYS.REQ.CLAIMS.UPDATE` for JWT revocation — ADR-0054) |
| `SysAccountPublicKey` | `string` | NATS system account public key                        |
| `SysAccountJWT`       | `string` | System account JWT (signed by operator; no JetStream) |

### Package `types`

Shared Go struct types for NATS KV bucket payloads and lifecycle event constants. These types define the canonical wire format for data stored in per-user `mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}` KV buckets, the shared `mclaude-hosts` bucket, as well as lifecycle events published on `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.lifecycle` subjects (ADR-0054 consolidated `sessions.>` hierarchy). See `spec-state-schema.md` for the full schema reference.

**KV payload structs:** `SessionState`, `Capabilities`, `UsageStats`, `ProjectState` (includes `importRef` field — import ID string, nullable; set when a project is created via `mclaude import`, cleared by session-agent after unpack), `HostState`, `QuotaStatus`.

**Import and attachment types (ADR-0053):**

| Type              | Fields                                                                                      | Description                                                                 |
|-------------------|---------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------|
| `ImportRequest`   | `slug`, `sizeBytes`                                                                          | Payload for the `import.request` NATS request/reply (CLI → CP).             |
| `ImportMetadata`  | `cwd`, `gitRemote`, `gitBranch`, `importedAt`, `sessionIds`, `claudeCodeVersion`             | Contents of `metadata.json` inside an import archive.                       |
| `AttachmentRef`   | `id`, `filename`, `mimeType`, `sizeBytes`                                                    | Lightweight reference carried in NATS messages. No S3 key — agents resolve via `attachments.download` request/reply. |
| `AttachmentMeta`  | `id`, `s3Key`, `filename`, `mimeType`, `sizeBytes`, `userSlug`, `hostSlug`, `projectSlug`, `createdAt` | Full attachment metadata (Postgres row). Internal to CP — never sent over NATS.           |

**Lifecycle event type constants:** `LifecycleSessionCreated`, `LifecycleSessionStopped`, `LifecycleSessionRestarting`, `LifecycleSessionResumed`, `LifecycleSessionFailed`, `LifecycleSessionUpgrading`, `LifecycleSessionJobPaused`, `LifecycleSessionJobComplete`, `LifecycleSessionJobCancelled`, `LifecycleSessionJobFailed`, `LifecycleSessionPermissionDenied`, `LifecycleSessionQuotaInterrupted`.

**`LifecycleEvent` struct:** Envelope for all lifecycle event payloads with common fields (`Type`, `SessionID`, `TS`) and optional per-event fields.

## Dependencies

- `golang.org/x/text` -- Unicode normalization (NFD decomposition, combining-mark detection) used by the slug algorithm.
- `github.com/nats-io/jwt/v2` -- NATS JWT claims encoding for operator and account JWTs (used by `pkg/nats`).
- `github.com/nats-io/nkeys` -- NATS NKey pair generation (used by `pkg/nats`).
- Go standard library (`encoding/base32`, `encoding/json`, `fmt`, `strings`, `time`, `unicode`).
