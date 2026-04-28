# Spec: Common Library

## Role

`mclaude-common` is a zero-dependency Go library (module `mclaude.io/common`) that provides typed slug identifiers and NATS subject/KV key construction helpers shared by all mclaude components. It enforces compile-time type safety: every subject and key helper accepts only typed slug wrappers, making it impossible to pass a raw string where a validated slug is expected.

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
`UserHostProjectAPISessionsInput`, `UserHostProjectAPISessionsControl`, `UserHostProjectAPISessionsCreate`, `UserHostProjectAPISessionsDelete`, `UserHostProjectAPITerminal`, `UserHostProjectEvents`, `UserHostProjectLifecycle`

All accept `(u UserSlug, h HostSlug, p ProjectSlug, ...)` -- the `h HostSlug` parameter is required for all project-scoped subjects.

**KV key helpers (ADR-0035):**

| Helper           | Signature                                          | Pattern                          | Bucket             |
|------------------|----------------------------------------------------|----------------------------------|--------------------|
| `SessionsKVKey`  | `(u, h, p, s)`                                     | `{uslug}.{hslug}.{pslug}.{sslug}` | mclaude-sessions   |
| `ProjectsKVKey`  | `(u, h, p)`                                        | `{uslug}.{hslug}.{pslug}`        | mclaude-projects   |
| `HostsKVKey`     | `(u, h)`                                           | `{uslug}.{hslug}`                | mclaude-hosts      |
| `JobQueueKVKey`  | `(u, jobId string)`                                | `{uslug}.{jobId}`                | mclaude-job-queue  |

Note: `ClustersKVKey` exists in code as dead code but the `mclaude-clusters` KV bucket was removed per ADR-0035. `LaptopsKVKey` (pre-ADR-0035) is also removed. Use `HostsKVKey` with a typed `HostSlug`.

## Dependencies

- `golang.org/x/text` -- Unicode normalization (NFD decomposition, combining-mark detection) used by the slug algorithm.
- Go standard library only (`encoding/base32`, `fmt`, `strings`, `unicode`).
