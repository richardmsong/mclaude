# Implementation Audit: mclaude-common

## Run: 2026-05-01T05:00:00Z

Round 2 — verifying round 1 gaps are closed.

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-common:11 | `UserSlug` type distinct `type T string` | slug.go:22 | IMPLEMENTED | | `type UserSlug string` |
| spec-common:12 | `ProjectSlug` type | slug.go:25 | IMPLEMENTED | | `type ProjectSlug string` |
| spec-common:13 | `SessionSlug` type | slug.go:28 | IMPLEMENTED | | `type SessionSlug string` |
| spec-common:14 | `HostSlug` type | slug.go:31 | IMPLEMENTED | | `type HostSlug string` |
| spec-common:15 | `ClusterSlug` type | slug.go:34 | IMPLEMENTED | | `type ClusterSlug string` |
| spec-common:17 | Kind constants: `KindUser`, `KindProject`, `KindSession`, `KindHost`, `KindCluster` | slug.go:42-48 | IMPLEMENTED | | All five constants defined |
| spec-common:18 | Fallback prefixes: u-, p-, s-, h-, c- | slug.go:52-66 | IMPLEMENTED | | `kindPrefix()` returns correct prefixes |
| spec-common:20-26 | Slugify algorithm (6 steps) | slug.go:117-160 | IMPLEMENTED | | All steps implemented correctly |
| spec-common:28 | Validate: charset `[a-z0-9][a-z0-9-]{0,62}`, max 63 | slug.go:188-217 | IMPLEMENTED | | Regex pattern, max length, leading char check |
| spec-common:28 | Validate: rejects reserved-word blocklist including ADR-0054 additions | slug.go:80-103 | IMPLEMENTED | | 15 reserved words including `create`, `delete`, `input`, `config`, `control` |
| spec-common:28 | Validate: returns typed errors `ErrEmpty`, `ErrCharset`, `ErrTooLong`, `ErrLeadingUnderscore`, `ErrReserved` | slug.go:170-186, 188-217 | IMPLEMENTED | | All five error types defined and returned |
| spec-common:30 | `ValidateOrFallback`: deterministic `{prefix}-{6 base32 chars}` from UUID seed | slug.go:223-232 | IMPLEMENTED | | Uses first 4 bytes, base32 lowercase no-pad |
| spec-common:32 | `DeriveUserSlug`: `{slugify(name)}-{domain}` | slug.go:259-303 | IMPLEMENTED | | Falls back to email local-part |
| spec-common:34 | `MustParse*` helpers (5 types) | slug.go:239-272 | IMPLEMENTED | | All five helpers: MustParseUserSlug through MustParseClusterSlug |
| spec-common:38 | `FilterMclaudeSessions` = `mclaude.users.*.hosts.*.projects.*.sessions.>` | subj.go:25 | IMPLEMENTED | | Exact match |
| spec-common:38 | Legacy filters (`FilterMclaudeAPI`, `FilterMclaudeEvents`, `FilterMclaudeLifecycle`) removed | subj.go (entire file) | IMPLEMENTED | | No legacy filters present |
| spec-common:42 | `UserAPIProjectsCreate` | subj.go:34-37 | IMPLEMENTED | | Correct pattern |
| spec-common:42 | `UserAPIProjectsUpdated` | subj.go:43-46 | IMPLEMENTED | | Correct pattern |
| spec-common:42 | `UserQuota` | subj.go:52-55 | IMPLEMENTED | | Correct pattern |
| spec-common:45 | `UserHostStatus(u, h)` → `mclaude.users.{uslug}.hosts.{hslug}.status` | subj.go:63-66 | IMPLEMENTED | | Correct pattern |
| spec-common:49 | `HostUserProjectsCreate(h, u, p)` → `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create` | subj.go:76-79 | IMPLEMENTED | | Correct pattern |
| spec-common:50 | `HostUserProjectsDelete(h, u, p)` → `mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.delete` | subj.go:87-90 | IMPLEMENTED | | Correct pattern |
| spec-common:53-59 | Session subject helpers: `UserHostProjectSessionsCreate`, `...Events`, `...Input`, `...Delete`, `...Control`, `...Config`, `...Lifecycle` | subj.go:101-163 | IMPLEMENTED | | All 7 helpers with correct patterns |
| spec-common:62 | `UserHostProjectAPITerminal(u, h, p) string` → `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal` | subj.go:175-178 | IMPLEMENTED | | Takes suffix parameter, returns correct pattern. **Round 1 gap closed.** |
| spec-common:66-68 | `SessionsKVKey(h, p, s)` → `hosts.{hslug}.projects.{pslug}.sessions.{sslug}` | subj.go:190-193 | IMPLEMENTED | | Per-user bucket, literal type-tokens |
| spec-common:67 | `ProjectsKVKey(h, p)` → `hosts.{hslug}.projects.{pslug}` | subj.go:200-203 | IMPLEMENTED | | Per-user bucket, literal type-tokens |
| spec-common:68 | `HostsKVKey(h)` → `{hslug}` | subj.go:210-213 | IMPLEMENTED | | Flat key, shared bucket |
| spec-common:70 | `ClustersKVKey` and `LaptopsKVKey` removed | subj.go (entire file) | IMPLEMENTED | | Neither function exists in code. **Round 1 gap closed.** |
| spec-common:74 | `FormatNATSCredentials(jwt string, seed []byte) []byte` | creds.go:6-18 | IMPLEMENTED | | Standard .creds format |
| spec-common:76 | `GenerateOperatorAccount(operatorName, accountName string) (*OperatorAccount, error)` | operator_keys.go:37-107 | IMPLEMENTED | | Generates operator + account + system account |
| spec-common:78 | `OperatorAccount` struct: 9 fields (OperatorSeed, OperatorPublicKey, AccountSeed, AccountPublicKey, OperatorJWT, AccountJWT, SysAccountSeed, SysAccountPublicKey, SysAccountJWT) | operator_keys.go:12-25 | IMPLEMENTED | | All 9 fields present |
| spec-common:82-84 | KV payload structs: `SessionState`, `Capabilities`, `UsageStats`, `ProjectState`, `HostState`, `QuotaStatus` | types.go:55-158 | IMPLEMENTED | | All structs with correct fields |
| spec-common:84 | `ProjectState.ImportRef` field (nullable string, ADR-0053) | types.go:134 | IMPLEMENTED | | `ImportRef string json:"importRef,omitempty"` |
| spec-common:86-90 | Import/attachment types: `ImportRequest`, `ImportMetadata`, `AttachmentRef`, `AttachmentMeta` | types.go:162-224 | IMPLEMENTED | | All 4 types with correct fields |
| spec-common:92 | Lifecycle event type constants (12 constants) | types.go:23-44 | IMPLEMENTED | | All 12 constants defined |
| spec-common:94 | `LifecycleEvent` struct with common + per-event fields | types.go:54-76 | IMPLEMENTED | | Envelope with Type, SessionID, TS + optional fields |
| spec-common:96 | Dependencies: `golang.org/x/text`, `github.com/nats-io/jwt/v2`, `github.com/nats-io/nkeys` | slug.go imports, operator_keys.go imports | IMPLEMENTED | | All three imported and used |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| slug.go:1-20 | INFRA | Package doc, imports |
| slug.go:37-48 | INFRA | Kind type definition and constants (spec'd) |
| slug.go:52-66 | INFRA | kindPrefix helper (internal implementation of spec'd fallback behavior) |
| slug.go:68-103 | INFRA | Reserved word type, constants, and set (internal implementation of spec'd blocklist) |
| slug.go:107-112 | INFRA | Charset doc constant and MaxLen constant |
| subj.go:1-13 | INFRA | Package doc, imports |
| creds.go:1-4 | INFRA | Package doc |
| operator_keys.go:1-10 | INFRA | Package doc, imports |
| types.go:1-16 | INFRA | Package doc, imports |
| types.go:18-21 | INFRA | LifecycleEventType type definition |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-common:11-15 | Typed slug wrappers (5 types) | slug_test.go | N/A | TESTED | Compile-time type safety verified |
| spec-common:20-26 | Slugify algorithm | slug_test.go | N/A | TESTED | Multiple test cases |
| spec-common:28 | Validate (charset, reserved words, errors) | slug_test.go | N/A | TESTED | All error types tested |
| spec-common:30 | ValidateOrFallback | slug_test.go | N/A | TESTED | Fallback generation tested |
| spec-common:32 | DeriveUserSlug | slug_test.go | N/A | TESTED | Multiple email/name combos |
| spec-common:34 | MustParse helpers | slug_test.go | N/A | TESTED | Implicitly via test setup `MustParseUserSlug("alice-gmail")` etc. |
| spec-common:38 | FilterMclaudeSessions constant | subj_test.go:TestFilterConstants | N/A | TESTED | Exact string match |
| spec-common:42-68 | All subject helpers (16 helpers) | subj_test.go:TestAllSpecSubjects | N/A | TESTED | Comprehensive pattern verification |
| spec-common:66-68 | KV key helpers (3 helpers) | subj_test.go:TestAllSpecKVKeys | N/A | TESTED | All three patterns verified |
| spec-common:62 | UserHostProjectAPITerminal | subj_test.go:TestUserHostProjectAPITerminal | N/A | TESTED | Two test cases (simple + termId) |
| spec-common:74 | FormatNATSCredentials | nats_test.go:TestFormatNATSCredentials | N/A | TESTED | 7 subtests. **Round 1 gap closed.** |
| spec-common:76 | GenerateOperatorAccount | nats_test.go:TestGenerateOperatorAccount | N/A | TESTED | 9 subtests including JWT validation, key uniqueness. **Round 1 gap closed.** |
| spec-common:82-94 | KV payload structs + lifecycle types | types_test.go | N/A | TESTED | Serialization verified |
| spec-common:86-90 | Import/attachment types | types_test.go | N/A | TESTED | |

Note: Integration tests are N/A for mclaude-common — it is a pure library with no external dependencies (no NATS server, no DB). Unit tests are sufficient for this component. The nats package tests use real nkeys library for key generation, providing realistic coverage.

### Phase 4 — Bug Triage

No open bugs with `**Component**: mclaude-common`.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | | | |

### Summary

- Implemented: 33
- Gap: 0
- Partial: 0
- Infra: 10
- Unspec'd: 0
- Dead: 0
- Tested: 14
- Unit only: 0
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0

**Round 1 gaps verified closed:**
1. ✅ `ClustersKVKey` stale reference — spec updated to note removal; no code references remain
2. ✅ `UserHostProjectAPITerminal` missing from spec — now documented with signature and pattern
3. ✅ `FormatNATSCredentials` and `GenerateOperatorAccount` untested — comprehensive unit tests added (7 + 9 subtests respectively), all passing
