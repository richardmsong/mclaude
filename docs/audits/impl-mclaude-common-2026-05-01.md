## Run: 2026-05-01T00:00:00Z

Component: mclaude-common
Directory: /Users/rsong/work/mclaude/mclaude-common
Spec: docs/mclaude-common/spec-common.md
Cross-cutting specs: docs/spec-state-schema.md
ADRs evaluated (accepted/implemented only): ADR-0035, ADR-0042, ADR-0025, ADR-0038, ADR-0046, ADR-0049, ADR-0050, ADR-0052
ADRs skipped (draft): ADR-0054, ADR-0053, ADR-0044, ADR-0057, ADR-0005
ADRs skipped (superseded): ADR-0024

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-common:5 | `mclaude-common` is a shared Go library (module `mclaude.io/common`) | pkg/slug/slug.go, pkg/subj/subj.go, pkg/nats/creds.go, pkg/nats/operator_keys.go, pkg/types/types.go | IMPLEMENTED | — | Module path confirmed in imports |
| spec-common:5 | provides typed slug identifiers, NATS subject/KV key construction helpers, NATS credential/key management utilities, and shared KV payload and lifecycle event types | all 5 production files | IMPLEMENTED | — | All four packages present |
| spec-common:5 | enforces compile-time type safety: every subject and key helper accepts only typed slug wrappers | pkg/subj/subj.go:all functions | IMPLEMENTED | — | All subj functions accept typed slugs (UserSlug, HostSlug, etc.); raw strings are compile-time errors |
| spec-common:9 | `UserSlug` typed wrapper (distinct `type T string`, not an alias) | pkg/slug/slug.go:22 | IMPLEMENTED | — | `type UserSlug string` |
| spec-common:9 | `ProjectSlug` typed wrapper | pkg/slug/slug.go:25 | IMPLEMENTED | — | `type ProjectSlug string` |
| spec-common:9 | `SessionSlug` typed wrapper | pkg/slug/slug.go:28 | IMPLEMENTED | — | `type SessionSlug string` |
| spec-common:9 | `HostSlug` typed wrapper | pkg/slug/slug.go:31 | IMPLEMENTED | — | `type HostSlug string` |
| spec-common:9 | `ClusterSlug` typed wrapper | pkg/slug/slug.go:34 | IMPLEMENTED | — | `type ClusterSlug string` |
| spec-common:14 | Kind constants: KindUser, KindProject, KindSession, KindHost, KindCluster | pkg/slug/slug.go:40-47 | IMPLEMENTED | — | All 5 Kind constants via iota |
| spec-common:14 | Fallback prefixes: u-, p-, s-, h-, c- | pkg/slug/slug.go:51-65 | IMPLEMENTED | — | `kindPrefix()` returns correct prefixes |
| spec-common:18 | Slugify step 1: Lowercase the input | pkg/slug/slug.go:133 | IMPLEMENTED | — | `strings.ToLower` |
| spec-common:18 | Slugify step 2: NFD Unicode decomposition (via `golang.org/x/text/unicode/norm`) | pkg/slug/slug.go:136 | IMPLEMENTED | — | `norm.NFD.String(s)` |
| spec-common:18 | Slugify step 3: Strip combining marks (Unicode category Mn) | pkg/slug/slug.go:139-146 | IMPLEMENTED | — | `unicode.Is(unicode.Mn, r)` check |
| spec-common:18 | Slugify step 4: Replace runs of non-`[a-z0-9]` characters with a single hyphen | pkg/slug/slug.go:149-161 | IMPLEMENTED | — | Run-length replacement logic |
| spec-common:18 | Slugify step 5: Trim leading and trailing hyphens | pkg/slug/slug.go:164 | IMPLEMENTED | — | `strings.Trim(s, "-")` |
| spec-common:18 | Slugify step 6: Truncate to 63 characters (re-trimming any trailing hyphen from the cut) | pkg/slug/slug.go:167-170 | IMPLEMENTED | — | `MaxLen` check + `TrimRight` |
| spec-common:22 | Returns empty string if no valid characters remain | pkg/slug/slug.go:164 | IMPLEMENTED | — | Trim can produce empty string |
| spec-common:24 | Validate charset: `[a-z0-9][a-z0-9-]{0,62}` | pkg/slug/slug.go:213-230 | IMPLEMENTED | — | Character-by-character check + first-char check |
| spec-common:24 | Max length 63 | pkg/slug/slug.go:118 (MaxLen) + slug.go:210 | IMPLEMENTED | — | `const MaxLen = 63` |
| spec-common:24 | Must not start with `_` or `-` | pkg/slug/slug.go:206,226-228 | IMPLEMENTED | — | Leading `_` → ErrLeadingUnderscore; leading `-` → ErrCharset |
| spec-common:24 | Rejects reserved-word blocklist: `users`, `hosts`, `projects`, `sessions`, `clusters`, `api`, `events`, `lifecycle`, `quota`, `terminal`, `create`, `delete`, `input`, `config`, `control` | pkg/slug/slug.go:76-106 | IMPLEMENTED | — | All 15 words in `reservedSet` map |
| spec-common:24 | Returns typed errors: `ErrEmpty`, `ErrCharset`, `ErrTooLong`, `ErrLeadingUnderscore`, `ErrReserved` | pkg/slug/slug.go:176-200 | IMPLEMENTED | — | All 5 error types defined with Error() methods |
| spec-common:28 | ValidateOrFallback generates deterministic slug `{prefix}-{6 base32 chars}` using first 4 bytes of UUID seed | pkg/slug/slug.go:243-265 | IMPLEMENTED | — | base32NoPad encoding, first 6 chars of encoded 4 bytes |
| spec-common:28 | base32 alphabet (`a-z2-7`, no padding) is always within slug charset | pkg/slug/slug.go:235 | IMPLEMENTED | — | Custom encoding `"abcdefghijklmnopqrstuvwxyz234567"` with NoPadding |
| spec-common:30 | DeriveUserSlug produces `{slugify(displayName)}-{first domain segment}` | pkg/slug/slug.go:285-327 | IMPLEMENTED | — | Matches described algorithm |
| spec-common:30 | Falls back to email local-part when display name slugifies to empty | pkg/slug/slug.go:295-299 | IMPLEMENTED | — | `if namePart == ""` falls back to local-part |
| spec-common:32 | MustParseUserSlug, MustParseProjectSlug, MustParseSessionSlug, MustParseHostSlug, MustParseClusterSlug — validate and return or panic | pkg/slug/slug.go:271-296 | IMPLEMENTED | — | All 5 MustParse helpers present with panic on invalid |
| spec-common:36 | FilterMclaudeSessions = `mclaude.users.*.hosts.*.projects.*.sessions.>` | pkg/subj/subj.go:22 | IMPLEMENTED | — | Exact match |
| spec-common:36 | Three legacy filters (FilterMclaudeAPI, FilterMclaudeEvents, FilterMclaudeLifecycle) are removed | pkg/subj/subj.go (absent) | IMPLEMENTED | — | Only FilterMclaudeSessions exists |
| spec-common:42 | UserAPIProjectsCreate(u UserSlug) → `mclaude.users.{uslug}.api.projects.create` | pkg/subj/subj.go:33-35 | IMPLEMENTED | — | Exact match |
| spec-common:42 | UserAPIProjectsUpdated(u UserSlug) → `mclaude.users.{uslug}.api.projects.updated` | pkg/subj/subj.go:42-44 | IMPLEMENTED | — | Exact match |
| spec-common:42 | UserQuota(u UserSlug) → `mclaude.users.{uslug}.quota` | pkg/subj/subj.go:51-53 | IMPLEMENTED | — | Exact match |
| spec-common:44 | UserHostStatus(u UserSlug, h HostSlug) → `mclaude.users.{uslug}.hosts.{hslug}.status` | pkg/subj/subj.go:61-63 | IMPLEMENTED | — | Exact signature and pattern match |
| spec-common:48 | HostUserProjectsCreate(h HostSlug, u UserSlug, p ProjectSlug) → `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create` | pkg/subj/subj.go:74-76 | IMPLEMENTED | — | Exact signature and pattern match |
| spec-common:48 | HostUserProjectsDelete(h HostSlug, u UserSlug, p ProjectSlug) → `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.delete` | pkg/subj/subj.go:83-85 | IMPLEMENTED | — | Exact signature and pattern match |
| spec-common:54 | UserHostProjectSessionsCreate(u, h, p) → `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.create` | pkg/subj/subj.go:96-98 | IMPLEMENTED | — | |
| spec-common:54 | UserHostProjectSessionsInput(u, h, p, s) → `...sessions.{sslug}.input` | pkg/subj/subj.go:115-117 | IMPLEMENTED | — | |
| spec-common:54 | UserHostProjectSessionsControl(u, h, p, s, suffix) → `...sessions.{sslug}.control.{suffix}` | pkg/subj/subj.go:133-135 | IMPLEMENTED | — | |
| spec-common:54 | UserHostProjectSessionsConfig(u, h, p, s) → `...sessions.{sslug}.config` | pkg/subj/subj.go:142-144 | IMPLEMENTED | — | |
| spec-common:54 | UserHostProjectSessionsDelete(u, h, p, s) → `...sessions.{sslug}.delete` | pkg/subj/subj.go:124-126 | IMPLEMENTED | — | |
| spec-common:54 | UserHostProjectSessionsEvents(u, h, p, s) → `...sessions.{sslug}.events` | pkg/subj/subj.go:105-107 | IMPLEMENTED | — | |
| spec-common:54 | UserHostProjectSessionsLifecycle(u, h, p, s, suffix) → `...sessions.{sslug}.lifecycle.{suffix}` | pkg/subj/subj.go:151-153 | IMPLEMENTED | — | |
| spec-common:58 | SessionsKVKey(h, p, s) → `hosts.{hslug}.projects.{pslug}.sessions.{sslug}` | pkg/subj/subj.go:181-183 | IMPLEMENTED | — | Bucket: mclaude-sessions-{uslug} |
| spec-common:58 | ProjectsKVKey(h, p) → `hosts.{hslug}.projects.{pslug}` | pkg/subj/subj.go:190-192 | IMPLEMENTED | — | Bucket: mclaude-projects-{uslug} |
| spec-common:58 | HostsKVKey(h) → `{hslug}` | pkg/subj/subj.go:199-201 | IMPLEMENTED | — | Bucket: mclaude-hosts (shared) |
| spec-common:58 | JobQueueKVKey is removed | pkg/subj/subj.go (absent) | IMPLEMENTED | — | Not present in code |
| spec-common:58 | ClustersKVKey exists in code as dead code | pkg/subj/subj.go (absent) | GAP | SPEC→FIX | Spec says ClustersKVKey "exists in code as dead code" but it has been removed from the code. Spec note is stale and should be updated to reflect that ClustersKVKey has been cleaned up. |
| spec-common:62 | FormatNATSCredentials(jwt string, seed []byte) []byte | pkg/nats/creds.go:6-17 | IMPLEMENTED | — | Exact signature match; formats standard .creds file |
| spec-common:62 | Formats a NATS credentials file (.creds format) from a JWT and NKey seed | pkg/nats/creds.go:6-17 | IMPLEMENTED | — | Output includes BEGIN/END markers for JWT and NKey seed |
| spec-common:64 | GenerateOperatorAccount(operatorName, accountName string) (*OperatorAccount, error) | pkg/nats/operator_keys.go:37-102 | IMPLEMENTED | — | Exact signature match |
| spec-common:64 | Generates operator + account NKey pair and JWTs for 3-tier trust chain | pkg/nats/operator_keys.go:37-102 | IMPLEMENTED | — | Creates operator, account, and system account |
| spec-common:64 | Operator JWT is self-signed; account JWT is signed by operator | pkg/nats/operator_keys.go:73,90 | IMPLEMENTED | — | `opClaims.Encode(opKP)` and `acctClaims.Encode(opKP)` |
| spec-common:66 | OperatorAccount.OperatorSeed []byte | pkg/nats/operator_keys.go:12 | IMPLEMENTED | — | |
| spec-common:66 | OperatorAccount.OperatorPublicKey string | pkg/nats/operator_keys.go:13 | IMPLEMENTED | — | |
| spec-common:66 | OperatorAccount.AccountSeed []byte | pkg/nats/operator_keys.go:14 | IMPLEMENTED | — | |
| spec-common:66 | OperatorAccount.AccountPublicKey string | pkg/nats/operator_keys.go:15 | IMPLEMENTED | — | |
| spec-common:66 | OperatorAccount.OperatorJWT string | pkg/nats/operator_keys.go:16 | IMPLEMENTED | — | |
| spec-common:66 | OperatorAccount.AccountJWT string | pkg/nats/operator_keys.go:17 | IMPLEMENTED | — | |
| spec-common:66 | OperatorAccount.SysAccountSeed []byte (for $SYS.REQ.CLAIMS.UPDATE JWT revocation) | pkg/nats/operator_keys.go:21 | IMPLEMENTED | — | |
| spec-common:66 | OperatorAccount.SysAccountPublicKey string | pkg/nats/operator_keys.go:22 | IMPLEMENTED | — | |
| spec-common:66 | OperatorAccount.SysAccountJWT string (signed by operator; no JetStream) | pkg/nats/operator_keys.go:23 | IMPLEMENTED | — | System account JWT encoded with no JetStream limits |
| spec-common:70 | SessionState KV payload struct | pkg/types/types.go:60-83 | IMPLEMENTED | — | All baseline fields present |
| spec-common:70 | Capabilities struct (Skills, Tools, Agents) | pkg/types/types.go:87-91 | IMPLEMENTED | — | Three []string fields |
| spec-common:70 | UsageStats struct (InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens, CostUSD) | pkg/types/types.go:94-100 | IMPLEMENTED | — | All 5 fields present with correct json tags |
| spec-common:70 | ProjectState (includes importRef field) | pkg/types/types.go:106-118 | IMPLEMENTED | — | `ImportRef string json:"importRef,omitempty"` present |
| spec-common:70 | HostState struct | pkg/types/types.go:124-130 | IMPLEMENTED | — | Slug, Type, Name, Online, LastSeenAt fields |
| spec-common:70 | QuotaStatus struct | pkg/types/types.go:136-143 | IMPLEMENTED | — | HasData, U5, R5, U7, R7, TS fields |
| spec-common:74 | ImportRequest: slug, sizeBytes | pkg/types/types.go:152-157 | IMPLEMENTED | — | Both fields with correct json tags |
| spec-common:74 | ImportMetadata: cwd, gitRemote, gitBranch, importedAt, sessionIds, claudeCodeVersion | pkg/types/types.go:161-174 | IMPLEMENTED | — | All 6 fields present |
| spec-common:74 | AttachmentRef: id, filename, mimeType, sizeBytes (no S3 key) | pkg/types/types.go:179-189 | IMPLEMENTED | — | 4 fields, no S3 key as specified |
| spec-common:74 | AttachmentMeta: id, s3Key, filename, mimeType, sizeBytes, userSlug, hostSlug, projectSlug, createdAt | pkg/types/types.go:194-212 | IMPLEMENTED | — | All 9 fields present |
| spec-common:78 | Lifecycle event constants: 12 listed | pkg/types/types.go:26-42 | IMPLEMENTED | — | All 12 constants match string values |
| spec-common:80 | LifecycleEvent struct with common fields (Type, SessionID, TS) and optional per-event fields | pkg/types/types.go:50-58 | IMPLEMENTED | — | All common + optional fields present |
| spec-common:84 | Dependencies: golang.org/x/text for Unicode normalization | pkg/slug/slug.go:11 | IMPLEMENTED | — | `golang.org/x/text/unicode/norm` imported |
| spec-common:84 | Dependencies: github.com/nats-io/jwt/v2 for NATS JWT claims | pkg/nats/operator_keys.go:5 | IMPLEMENTED | — | `natsjwt "github.com/nats-io/jwt/v2"` |
| spec-common:84 | Dependencies: github.com/nats-io/nkeys for NKey pair generation | pkg/nats/operator_keys.go:6 | IMPLEMENTED | — | `"github.com/nats-io/nkeys"` |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| pkg/slug/slug.go:1-7 | INFRA | Package declaration and imports |
| pkg/slug/slug.go:9-12 | INFRA | Imports for standard library + golang.org/x/text |
| pkg/slug/slug.go:108-112 | INFRA | Charset and MaxLen documentation constants (referenced by spec) |
| pkg/slug/slug.go:233-237 | INFRA | base32NoPad encoding variable (implementation detail for ValidateOrFallback) |
| pkg/slug/slug.go:267-269 | INFRA | fallbackSlug helper (internal implementation for ValidateOrFallback) |
| pkg/subj/subj.go:1-11 | INFRA | Package declaration and imports |
| pkg/subj/subj.go:157-173 | UNSPEC'd | `UserHostProjectAPITerminal(u, h, p, suffix)` — terminal subject helper not mentioned in spec-common.md. Used by session-agent and SPA for terminal I/O. Described in spec-state-schema.md under "Terminal subjects" but absent from the component spec. |
| pkg/nats/creds.go:1-4 | INFRA | Package declaration |
| pkg/nats/operator_keys.go:1-8 | INFRA | Package declaration and imports |
| pkg/nats/operator_keys.go:25-35 | INFRA | GenerateOperatorAccount doc comment |
| pkg/types/types.go:1-13 | INFRA | Package declaration, imports, doc comment |
| pkg/types/types.go:15-24 | INFRA | LifecycleEventType type declaration |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-common:9 | Typed slug wrappers (UserSlug, etc.) | slug_test.go:TestMustParseUserSlugValid | — | UNIT_ONLY | No integration test but this is a pure data type — unit tests are sufficient |
| spec-common:18 | Slugify algorithm (6 steps) | slug_test.go:TestSlugify (16 cases) | — | UNIT_ONLY | Pure function, unit tests cover all steps |
| spec-common:24 | Validate function | slug_test.go:TestValidate (19+ cases) | — | UNIT_ONLY | Pure function, comprehensive test cases including all 15 reserved words |
| spec-common:28 | ValidateOrFallback | slug_test.go:TestValidateOrFallback (7 cases) | — | UNIT_ONLY | Covers valid candidate, empty, reserved, emoji, determinism, different seeds, host kind |
| spec-common:30 | DeriveUserSlug | slug_test.go:TestDeriveUserSlug (6 cases) | — | UNIT_ONLY | Covers standard, multi-part, no display name, domain segments |
| spec-common:32 | MustParse helpers | slug_test.go:TestMustParseUserSlugPanics, TestMustParseUserSlugValid | — | UNIT_ONLY | Only UserSlug tested; other MustParse helpers (Project, Session, Host, Cluster) lack explicit tests |
| spec-common:36 | FilterMclaudeSessions constant | subj_test.go:TestFilterConstants | — | UNIT_ONLY | Verifies exact string value |
| spec-common:42-54 | Subject helpers (all) | subj_test.go:TestAllSpecSubjects (19 cases) + individual tests | — | UNIT_ONLY | All subject patterns tested against expected outputs |
| spec-common:58 | KV key helpers (all) | subj_test.go:TestAllSpecKVKeys (3 cases) | — | UNIT_ONLY | SessionsKVKey, ProjectsKVKey, HostsKVKey all tested |
| spec-common:62 | FormatNATSCredentials | — | — | UNTESTED | No tests found for FormatNATSCredentials |
| spec-common:64 | GenerateOperatorAccount | — | — | UNTESTED | No tests found for GenerateOperatorAccount or OperatorAccount struct |
| spec-common:70 | SessionState round-trip | types_test.go:TestSessionStateRoundTrip | — | UNIT_ONLY | JSON marshal/unmarshal verified |
| spec-common:70 | ProjectState round-trip | types_test.go:TestProjectStateRoundTrip, TestProjectStateImportRefOmitsWhenEmpty | — | UNIT_ONLY | Round-trip + omitempty behavior verified |
| spec-common:70 | HostState round-trip | types_test.go:TestHostStateRoundTrip, TestHostStateHasNoRoleField | — | UNIT_ONLY | Round-trip + structural assertion (no role field) |
| spec-common:70 | QuotaStatus round-trip | types_test.go:TestQuotaStatusRoundTrip | — | UNIT_ONLY | Round-trip verified |
| spec-common:78 | Lifecycle event constants (12) | types_test.go:TestLifecycleEventTypeConstants | — | UNIT_ONLY | All 12 string values verified |
| spec-common:80 | LifecycleEvent round-trip | types_test.go:TestLifecycleEventRoundTrip (12 subtests) | — | UNIT_ONLY | All event types tested |
| spec-common:74 | ImportRequest round-trip | types_test.go:TestImportRequestRoundTrip | — | UNIT_ONLY | JSON round-trip verified |
| spec-common:74 | ImportMetadata round-trip | types_test.go:TestImportMetadataRoundTrip | — | UNIT_ONLY | JSON round-trip verified |
| spec-common:74 | AttachmentRef round-trip | types_test.go:TestAttachmentRefRoundTrip | — | UNIT_ONLY | JSON round-trip verified |
| spec-common:74 | AttachmentMeta round-trip | types_test.go:TestAttachmentMetaRoundTrip | — | UNIT_ONLY | JSON round-trip verified |

### Phase 4 — Bug Triage

No bugs in `.agent/bugs/` reference the `mclaude-common` component. All existing bugs target other components (spa, session-agent).

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No mclaude-common bugs filed |

### Summary

- Implemented: 56
- Gap: 1
- Partial: 0
- Infra: 12
- Unspec'd: 1
- Dead: 0
- Tested: 0
- Unit only: 21
- E2E only: 0
- Untested: 2
- Bugs fixed: 0
- Bugs open: 0

#### Non-clean findings

**GAP [SPEC→FIX]:** spec-common.md states "ClustersKVKey exists in code as dead code" but ClustersKVKey has been removed from pkg/subj/subj.go. The spec note is stale and should be updated to reflect that this dead code was cleaned up.

**UNSPEC'd:** pkg/subj/subj.go:157-173 — `UserHostProjectAPITerminal(u UserSlug, h HostSlug, p ProjectSlug, suffix string)` helper for terminal subjects (`mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal.{suffix}`). This function is documented in spec-state-schema.md under "Terminal subjects" and actively used by session-agent and SPA, but is absent from spec-common.md's package `subj` interface listing. Spec should add it.

**UNTESTED:** `FormatNATSCredentials` (pkg/nats/creds.go) — no unit or integration tests.

**UNTESTED:** `GenerateOperatorAccount` (pkg/nats/operator_keys.go) — no unit or integration tests. This is a critical function that generates the NATS trust chain. Testing would verify correct JWT structure, signing relationships, and JetStream limits.
