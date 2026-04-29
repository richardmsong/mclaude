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

Charset: `[a-z0-9][a-z0-9-]{0,62}`. Max length 63. Must not start with `_` or `-`. Rejects the reserved-word blocklist: `users`, `hosts`, `projects`, `sessions`, `clusters`, `api`, `events`, `lifecycle`, `quota`, `terminal`. Returns typed errors: `ErrEmpty`, `ErrCharset`, `ErrTooLong`, `ErrLeadingUnderscore`, `ErrReserved`.

**Fallback (`ValidateOrFallback`):**

When a candidate fails validation, generates a deterministic slug `{prefix}-{6 base32 chars}` using the first 4 bytes of a UUID seed. The base32 alphabet (`a-z2-7`, no padding) is always within the slug charset.

**User slug derivation (`DeriveUserSlug`):**

Produces `{slugify(displayName)}-{first domain segment}` from a display name and email. Falls back to the email local-part when the display name slugifies to empty.

**MustParse helpers:** `MustParseUserSlug`, `MustParseProjectSlug`, `MustParseSessionSlug`, `MustParseHostSlug`, `MustParseClusterSlug` -- validate and return the typed wrapper or panic.

### Package `subj`

Typed NATS subject and KV key builders. Every function accepts only typed slug wrappers from `pkg/slug`. See `spec-state-schema.md` for canonical subject and key formats.

ADR-0035 (Unified Host Architecture) extends ADR-0024 by inserting `.hosts.{hslug}.` between the user and project levels in all project-scoped subjects, KV keys, and JetStream filter constants.

**JetStream stream filters (ADR-0035):**

| Constant                 | Pattern                                                    |
|--------------------------|------------------------------------------------------------|
| `FilterMclaudeAPI`       | `mclaude.users.*.hosts.*.projects.*.api.sessions.>`        |
| `FilterMclaudeEvents`    | `mclaude.users.*.hosts.*.projects.*.events.*`              |
| `FilterMclaudeLifecycle` | `mclaude.users.*.hosts.*.projects.*.lifecycle.*`           |

**User-scoped subject helpers (no host level):**
`UserAPIProjectsCreate`, `UserAPIProjectsUpdated`, `UserQuota`

**Host-scoped subject helper:**
`UserHostStatus(u UserSlug, h HostSlug) string` -- host presence heartbeat.

**User+host+project-scoped subject helpers (ADR-0035):**
`UserHostProjectAPISessionsInput`, `UserHostProjectAPISessionsControl`, `UserHostProjectAPISessionsCreate`, `UserHostProjectAPISessionsDelete`, `UserHostProjectAPISessionsRestart`, `UserHostProjectAPITerminal`, `UserHostProjectEvents`, `UserHostProjectLifecycle`

All accept `(u UserSlug, h HostSlug, p ProjectSlug, ...)` -- the `h HostSlug` parameter is required for all project-scoped subjects.

**KV key helpers (ADR-0035):**

| Helper           | Signature                                          | Pattern                          | Bucket             |
|------------------|----------------------------------------------------|----------------------------------|--------------------|
| `SessionsKVKey`  | `(u, h, p, s)`                                     | `{uslug}.{hslug}.{pslug}.{sslug}` | mclaude-sessions   |
| `ProjectsKVKey`  | `(u, h, p)`                                        | `{uslug}.{hslug}.{pslug}`        | mclaude-projects   |
| `HostsKVKey`     | `(u, h)`                                           | `{uslug}.{hslug}`                | mclaude-hosts      |
| `JobQueueKVKey`  | `(u, jobId string)`                                | `{uslug}.{jobId}`                | mclaude-job-queue  |

Note: `ClustersKVKey` exists in code as dead code but the `mclaude-clusters` KV bucket was removed per ADR-0035. `LaptopsKVKey` (pre-ADR-0035) is also removed. Use `HostsKVKey` with a typed `HostSlug`.

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
| `SysAccountPublicKey` | `string` | NATS system account public key                        |
| `SysAccountJWT`       | `string` | System account JWT (signed by operator; no JetStream) |

### Package `types`

Shared Go struct types for NATS KV bucket payloads and lifecycle event constants. These types define the canonical wire format for data stored in `mclaude-sessions`, `mclaude-projects`, `mclaude-hosts`, and `mclaude-job-queue` KV buckets, as well as lifecycle events published on `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle.{sslug}` subjects. See `spec-state-schema.md` for the full schema reference.

**KV payload structs:** `SessionState`, `Capabilities`, `UsageStats`, `ProjectState`, `HostState`, `QuotaStatus`, `JobEntry`.

**Lifecycle event type constants:** `LifecycleSessionCreated`, `LifecycleSessionStopped`, `LifecycleSessionRestarting`, `LifecycleSessionResumed`, `LifecycleSessionFailed`, `LifecycleSessionUpgrading`, `LifecycleSessionJobPaused`, `LifecycleSessionJobComplete`, `LifecycleSessionJobCancelled`, `LifecycleSessionJobFailed`, `LifecycleSessionPermissionDenied`, `LifecycleSessionQuotaInterrupted`.

**`LifecycleEvent` struct:** Envelope for all lifecycle event payloads with common fields (`Type`, `SessionID`, `TS`) and optional per-event fields.

## Dependencies

- `golang.org/x/text` -- Unicode normalization (NFD decomposition, combining-mark detection) used by the slug algorithm.
- `github.com/nats-io/jwt/v2` -- NATS JWT claims encoding for operator and account JWTs (used by `pkg/nats`).
- `github.com/nats-io/nkeys` -- NATS NKey pair generation (used by `pkg/nats`).
- Go standard library (`encoding/base32`, `encoding/json`, `fmt`, `strings`, `time`, `unicode`).
