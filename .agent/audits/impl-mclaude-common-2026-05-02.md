# Implementation Audit: mclaude-common

## Run: 2026-05-02T00:00:00Z

**Component root:** `mclaude-common/`
**Spec files:** `docs/mclaude-common/spec-common.md`, cross-cutting `docs/spec-state-schema.md`, `docs/spec-nats-payload-schema.md`
**ADRs evaluated:** ADR-0024 (superseded — skipped), ADR-0054 (draft — skipped), ADR-0062 (implemented), ADR-0075 (accepted)
**Production source files:**
- `pkg/hostauth/host_auth.go`
- `pkg/nats/creds.go`
- `pkg/nats/operator_keys.go`
- `pkg/slug/slug.go`
- `pkg/subj/subj.go`
- `pkg/types/types.go`

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-common.md:13-21 | Typed wrappers: `UserSlug`, `ProjectSlug`, `SessionSlug`, `HostSlug`, `ClusterSlug` — each a distinct `type T string` | slug.go:20-33 | IMPLEMENTED | — | All five types declared as distinct named types. |
| spec-common.md:14-21 | Kind constants: `KindUser`, `KindProject`, `KindSession`, `KindHost`, `KindCluster` with fallback prefixes `u-`, `p-`, `s-`, `h-`, `c-` | slug.go:41-65 | IMPLEMENTED | — | Kind enum with kindPrefix() returns the correct prefixes. |
| spec-common.md:24 | Slugify step 1: lowercase the input | slug.go:143 | IMPLEMENTED | — | `strings.ToLower(displayName)` |
| spec-common.md:25 | Slugify step 2: NFD Unicode decomposition | slug.go:146 | IMPLEMENTED | — | `norm.NFD.String(s)` |
| spec-common.md:26 | Slugify step 3: strip combining marks (Unicode category Mn) | slug.go:149-156 | IMPLEMENTED | — | Loop over runes, skip `unicode.Is(unicode.Mn, r)` |
| spec-common.md:27 | Slugify step 4: replace runs of non-`[a-z0-9]` with a single hyphen | slug.go:159-175 | IMPLEMENTED | — | `inRun` boolean correctly collapses runs into one `-` |
| spec-common.md:28 | Slugify step 5: trim leading and trailing hyphens | slug.go:177-178 | IMPLEMENTED | — | `strings.Trim(s, "-")` |
| spec-common.md:29 | Slugify step 6: truncate to 63 chars, re-trimming trailing hyphen | slug.go:180-184 | IMPLEMENTED | — | Truncates then `TrimRight("-")` |
| spec-common.md:32-36 | Validate charset: `[a-z0-9][a-z0-9-]{0,62}`, max 63, no leading `_` or `-`, reserved-word blocklist | slug.go:231-260 | IMPLEMENTED | — | All checks present; first-char check after inner loop correctly blocks leading `-`. |
| spec-common.md:35 | Validate returns typed errors: `ErrEmpty`, `ErrCharset`, `ErrTooLong`, `ErrLeadingUnderscore`, `ErrReserved` | slug.go:193-260 | IMPLEMENTED | — | All five error types defined and returned. |
| spec-common.md:36 | Reserved-word blocklist: `users`, `hosts`, `projects`, `sessions`, `clusters`, `api`, `events`, `lifecycle`, `quota`, `terminal`, `create`, `delete`, `input`, `config`, `control` | slug.go:75-113 | IMPLEMENTED | — | All 15 reserved words declared as typed `reservedWord` constants; present in `reservedSet`. |
| spec-common.md:39-41 | `ValidateOrFallback`: if invalid, generates `{prefix}-{6 base32 chars}` from first 4 bytes of UUID seed; base32 alphabet `a-z2-7` | slug.go:277-293 | IMPLEMENTED | — | `base32NoPad` uses the correct 32-char alphabet; fallbackSlug encodes 4 bytes → 7 chars, takes first 6. |
| spec-common.md:43-44 | `DeriveUserSlug`: `slugify(full-email)`, examples: `dev@mclaude.local` → `dev-mclaude-local`, `richard.song@gmail.com` → `richard-song-gmail-com` | slug.go:360-362 | IMPLEMENTED | — | Delegates to `Slugify(email)`. The `@` and `.` become `-` via step 4. Both examples verified correct. |
| spec-common.md:46 | MustParse helpers: `MustParseUserSlug`, `MustParseProjectSlug`, `MustParseSessionSlug`, `MustParseHostSlug`, `MustParseClusterSlug` — validate or panic | slug.go:300-337 | IMPLEMENTED | — | All five helpers validate then return typed wrapper or `panic(fmt.Sprintf(...))`. |
| spec-common.md:54-60 | `FilterMclaudeSessions` constant: `mclaude.users.*.hosts.*.projects.*.sessions.>` | subj.go:25 | IMPLEMENTED | — | Exact pattern matches. |
| spec-common.md:60 | Three legacy filters (`FilterMclaudeAPI`, `FilterMclaudeEvents`, `FilterMclaudeLifecycle`) are removed | subj.go (absent) | IMPLEMENTED | — | No legacy filter constants exist in the file. |
| spec-common.md:62-63 | User-scoped subjects: `UserAPIProjectsCreate`, `UserAPIProjectsUpdated`, `UserQuota` | subj.go:36-57 | IMPLEMENTED | — | Correct patterns: `mclaude.users.{u}.api.projects.create`, etc. |
| spec-common.md:66 | `UserHostStatus(u UserSlug, h HostSlug)` → `mclaude.users.{uslug}.hosts.{hslug}.status` | subj.go:68-70 | IMPLEMENTED | — | Pattern matches spec. |
| spec-common.md:68-75 | Host-scoped provisioning subjects: `HostUserProjectsCreate`, `HostUserProjectsDelete` accepting `(h HostSlug, u UserSlug, p ProjectSlug)` | subj.go:85-97 | IMPLEMENTED | — | Both helpers implemented with correct patterns `mclaude.hosts.{h}.users.{u}.projects.{p}.create/delete`. |
| spec-common.md:77-80 | Session-scoped subject helpers: `UserHostProjectSessionsCreate`, `...Input`, `...Control`, `...Config`, `...Delete`, `...Events`, `...Lifecycle` all accept `(u UserSlug, h HostSlug, p ProjectSlug, ...)` | subj.go:112-170 | IMPLEMENTED | — | All seven helpers present; `Control` and `Lifecycle` accept an extra `suffix string` param as expected. |
| spec-common.md:82-83 | `UserHostProjectAPITerminal(u UserSlug, h HostSlug, p ProjectSlug) string` → `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.api.terminal` | subj.go:184-186 | IMPLEMENTED | — | Function takes additional `suffix string` param enabling discriminators like `in`, `out`, `term-1.output`. |
| spec-common.md:85-93 | KV key helpers: `SessionsKVKey(h,p,s)`, `ProjectsKVKey(h,p)`, `HostsKVKey(h)` with patterns `hosts.{h}.projects.{p}.sessions.{s}`, `hosts.{h}.projects.{p}`, `{hslug}` | subj.go:202-225 | IMPLEMENTED | — | All three helpers present with exact pattern matches. |
| spec-common.md:93 | `JobQueueKVKey` is removed | subj.go (absent) | IMPLEMENTED | — | No such function exists. |
| spec-common.md:95 | `ClustersKVKey` and `LaptopsKVKey` removed | subj.go (absent) | IMPLEMENTED | — | Neither function exists. |
| spec-common.md:101-103 | `FormatNATSCredentials(jwt string, seed []byte) []byte` — standard NATS `.creds` format | nats/creds.go:8-22 | IMPLEMENTED | — | Produces the standard NATS credential file format with correct BEGIN/END delimiters. |
| spec-common.md:105-107 | `GenerateOperatorAccount(operatorName, accountName string) (*OperatorAccount, error)` | nats/operator_keys.go:36-121 | IMPLEMENTED | — | Generates operator + system account + application account. |
| spec-common.md:109-122 | `OperatorAccount` struct fields: `OperatorSeed`, `OperatorPublicKey`, `AccountSeed`, `AccountPublicKey`, `OperatorJWT`, `AccountJWT`, `SysAccountSeed`, `SysAccountPublicKey`, `SysAccountJWT` | nats/operator_keys.go:13-28 | IMPLEMENTED | — | All 9 fields present with correct types. |
| spec-common.md:125-131 | `NewHostAuthFromCredsFile(credsPath, cpURL, log)` — reads `.creds` file with NKey seed + JWT; used by `mclaude-controller-local` | hostauth/host_auth.go:53-75 | IMPLEMENTED | — | Parses decorated NKey seed and JWT from creds file. |
| spec-common.md:132-133 | `NewHostAuthFromSeed(seedPath, cpURL, log)` — reads only the NKey seed; no JWT required; used by `mclaude-controller-k8s` | hostauth/host_auth.go:81-100 | IMPLEMENTED | — | Reads seed file, sets `currentJWT = ""`. |
| spec-common.md:135-144 + adr-0075:29-45 | `Refresh(ctx)`: step 1: POST /api/auth/challenge; step 2: base64-decode challenge to raw nonce bytes then sign those; step 3: POST /api/auth/verify → {jwt} | hostauth/host_auth.go:141-185 | IMPLEMENTED | — | ADR-0075 fix applied: `base64.StdEncoding.DecodeString(challenge)` then `h.kp.Sign(nonceBytes)`. |
| spec-common.md:141-144 | When public key not registered (HTTP 404), return sentinel `ErrNotRegistered` and log instructional message with `mclaude host register` command | hostauth/host_auth.go:150-159 + requestChallenge:241 | IMPLEMENTED | — | `requestChallenge()` returns `ErrNotRegistered` on 404; `Refresh()` logs the full instructional message. |
| spec-common.md:147 | `PublicKey() string` — returns NKey public key (U-prefix) | hostauth/host_auth.go:104-111 | IMPLEMENTED | — | Returns `kp.PublicKey()`, panics on error (unreachable for valid keypair). |
| spec-common.md:149-151 | `JWTFunc() nats.AuthTokenHandler` — returns function that returns cached JWT | hostauth/host_auth.go:116-123 | IMPLEMENTED | — | Returns closure over `currentJWT` with RLock protection. |
| spec-common.md:153-154 | `SignFunc() nats.SignatureHandler` — returns NKey sign function | hostauth/host_auth.go:127-131 | IMPLEMENTED | — | Returns `kp.Sign(nonce)` closure. |
| spec-common.md:155-157 | `StartRefreshLoop(ctx)` — background goroutine, proactively refreshes before 5-min TTL, stops on ctx cancel | hostauth/host_auth.go:194-222 | IMPLEMENTED | — | Fires at `5min - 60s = 4min` intervals. No-op if cpURL empty (logs warning). |
| spec-common.md:158-170 | `types` package: KV payload structs `SessionState`, `Capabilities`, `UsageStats`, `ProjectState` (with `importRef`), `HostState`, `QuotaStatus` | types/types.go:89-181 | IMPLEMENTED | — | All structs present with correct fields. `ImportRef` has `omitempty` JSON tag. |
| spec-common.md:162-169 | Import and attachment types: `ImportRequest`, `ImportMetadata`, `AttachmentRef`, `AttachmentMeta` with specified fields | types/types.go:190-252 | IMPLEMENTED | — | All four types present. `AttachmentRef` has no S3 key per spec. `AttachmentMeta` is internal to CP per spec. |
| spec-common.md:171 | Lifecycle event type constants (12 total) | types/types.go:24-39 | IMPLEMENTED | — | All 12 constants present with correct string values. |
| spec-common.md:173 | `LifecycleEvent` struct with common fields (`Type`, `SessionID`, `TS`) and optional per-event fields | types/types.go:51-80 | IMPLEMENTED | — | All common fields present; optional fields match spec. |

---

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| hostauth/host_auth.go:23-28 | INFRA | `hostJWTTTL` (5min) and `hostJWTRefreshBuffer` (60s) constants — implementation detail of the refresh loop interval. Spec says "fires before 5-min TTL within safety margin" without pinning values. |
| hostauth/host_auth.go:33 | INFRA | `ErrNotRegistered` sentinel — exported per spec ("returns a sentinel retryable error"). |
| hostauth/host_auth.go:205-222 | INFRA | `startRefreshLoopWithInterval` private method — test harness variant of `StartRefreshLoop` with configurable interval. Enables unit testing with short intervals without modifying the public API. |
| hostauth/host_auth.go:227-260 | INFRA | `requestChallenge` private helper — implements step 1 of the spec'd challenge-response flow. |
| hostauth/host_auth.go:263-301 | INFRA | `verifyChallenge` private helper — implements step 3 of the spec'd flow. |
| slug.go:69-113 | INFRA | `reservedWord` type and `reservedSet` map — enforces typed-constant pattern for the blocklist; necessary for type safety on additions. |
| slug.go:117-126 | INFRA | `Charset` and `MaxLen` documentation constants — not directly spec'd as exports but serve as self-documentation and are used by callers for error messages. |
| slug.go:266-268 | INFRA | `base32NoPad` encoding variable — private implementation detail of fallback slug generation. |
| slug.go:283-293 | INFRA | `fallbackSlug` private function — implements the spec'd `{prefix}-{6 base32 chars}` fallback algorithm. |
| nats/operator_keys.go:99-104 | INFRA | JetStream limits set to `-1` (unlimited) on the application account JWT — spec says "application account JWT with JetStream enabled" but does not pin the limit values. -1 is standard NATS unlimited marker. |

---

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-common.md:24-29 | Slugify 6-step algorithm | `TestSlugify` (slug_test.go:14) + `TestSlugifyRoundTrip` | none | UNIT_ONLY | Covers all steps, edge cases (accents, emoji, truncation, leading/trailing punctuation). Pure algorithm — no infrastructure dependency; integration test not required by the integration test bar. |
| spec-common.md:32-36 | Validate charset + reserved words + error types | `TestValidate` (slug_test.go:111) | none | UNIT_ONLY | All 15 reserved words tested; all 5 typed error types exercised. |
| spec-common.md:39-41 | `ValidateOrFallback` fallback generation | `TestValidateOrFallback` (slug_test.go:178) | none | UNIT_ONLY | Tests all 5 kind prefixes, determinism, seed variation. |
| spec-common.md:43-44 | `DeriveUserSlug` full-email algorithm | `TestDeriveUserSlug` (slug_test.go:281) + `TestDeriveUserSlug_CollisionResistance` | none | UNIT_ONLY | Tests both spec examples and collision-resistance property. |
| spec-common.md:46 | MustParse helpers panic on invalid | `TestMustParse*Panics` × 5, `TestMustParse*Valid` × 5 (slug_test.go:338-432) | none | UNIT_ONLY | All five helpers covered with both valid and panic cases. |
| spec-common.md:54-60 | `FilterMclaudeSessions` constant | `TestFilterConstants` (subj_test.go:23) | none | UNIT_ONLY | Verifies exact string value. |
| spec-common.md:62-63 | User-scoped subject helpers | `TestUserAPIProjectsCreate`, `TestUserAPIProjectsUpdated`, `TestUserQuota` (subj_test.go) | none | UNIT_ONLY | |
| spec-common.md:66 | `UserHostStatus` | `TestUserHostStatus` (subj_test.go:76) | none | UNIT_ONLY | |
| spec-common.md:68-75 | Host-scoped provisioning subjects | `TestHostUserProjectsCreate`, `TestHostUserProjectsDelete` | none | UNIT_ONLY | |
| spec-common.md:77-80 | Session-scoped subject helpers (7 functions) | All 7 covered in subj_test.go + `TestAllSpecSubjects` (subj_test.go:264) | none | UNIT_ONLY | Full coverage including all Control and Lifecycle suffix variants. |
| spec-common.md:82-83 | `UserHostProjectAPITerminal` | `TestUserHostProjectAPITerminal`, `TestUserHostProjectAPITerminalOutput` | none | UNIT_ONLY | |
| spec-common.md:85-93 | KV key helpers | `TestSessionsKVKey`, `TestProjectsKVKey`, `TestHostsKVKey` + `TestAllSpecKVKeys` | none | UNIT_ONLY | |
| spec-common.md:101-103 | `FormatNATSCredentials` | `TestFormatNATSCredentials` (nats_test.go:17) | none | UNIT_ONLY | 6 sub-tests covering all delimiter variants, embedding, and ordering. |
| spec-common.md:105-122 | `GenerateOperatorAccount` + `OperatorAccount` fields | `TestGenerateOperatorAccount` (nats_test.go:114) | none | UNIT_ONLY | 8 sub-tests including JWT decoding, system account reference, JetStream limits, key uniqueness. |
| spec-common.md:125-131 | `NewHostAuthFromCredsFile` | `TestNewHostAuthFromCredsFile_Valid`, `_InvalidPath`, `_InvalidContent` | none | UNIT_ONLY | |
| spec-common.md:132-133 | `NewHostAuthFromSeed` | `TestNewHostAuthFromSeed_Valid`, `_InvalidPath`, `_InvalidContent` | none | UNIT_ONLY | |
| spec-common.md:135-144 + adr-0075 | `Refresh()` full flow with raw-nonce signing | `TestRefresh_FullFlow`, `TestRefresh_UpdatesStoredJWT`, `TestRefresh_ErrorWhenNoCPURL`, `TestRefresh_404ReturnsErrNotRegistered`, `TestRefresh_SignatureCorrectness`, `TestRefresh_SignsRawNonceNotBase64` | none | UNIT_ONLY | ADR-0075 explicitly lists `TestRefresh_SignsRawNonceNotBase64` as the required unit test — present. Test uses `nkeys.FromPublicKey(pub).Verify(rawNonce, sig)` to confirm raw-byte signing, AND asserts that the base64-string signature fails verification (discriminating test). |
| spec-common.md:147 | `PublicKey()` | `TestPublicKey_Format`, `TestPublicKey_FromSeed` | none | UNIT_ONLY | |
| spec-common.md:149-151 | `JWTFunc()` | Covered via `TestNewHostAuthFromCredsFile_Valid`, `TestRefresh_UpdatesStoredJWT` | none | UNIT_ONLY | |
| spec-common.md:153-154 | `SignFunc()` | `TestSignFunc_ProducesSignature`, `TestSignFunc_SignatureVerifiable` | none | UNIT_ONLY | |
| spec-common.md:155-157 | `StartRefreshLoop` | `TestStartRefreshLoop_RefreshesBeforeTTL`, `TestStartRefreshLoop_StopsOnContextCancel`, `TestStartRefreshLoop_NoCPURL` (host_auth_loop_test.go) | none | UNIT_ONLY | Uses internal `startRefreshLoopWithInterval` with 100ms interval. |
| spec-common.md:158-173 | All `types` package structs and lifecycle constants | `TestSessionStateRoundTrip`, `TestProjectStateRoundTrip`, `TestProjectStateImportRefOmitsWhenEmpty`, `TestHostStateRoundTrip`, `TestHostStateHasNoRoleField`, `TestQuotaStatusRoundTrip`, `TestLifecycleEventRoundTrip`, `TestLifecycleEventTypeConstants`, `TestImportRequestRoundTrip`, `TestImportMetadataRoundTrip`, `TestAttachmentRefRoundTrip`, `TestAttachmentMetaRoundTrip` | none | UNIT_ONLY | All structs round-trip tested; all 12 lifecycle constants verified against expected string values. |

---

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` have `**Component**: mclaude-common` or reference any package in `mclaude-common/`. The bugs present (002-droid-shield-false-positives, 003-initial-message-injection-race, 002-ptt-race-condition, 005-getting-started-canned-text) are all UI-layer issues unrelated to this component.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | — | — | No open bugs target mclaude-common |

---

### Summary

- Implemented: 39
- Gap: 0
- Partial: 0
- Infra: 10
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 23
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0
