# Implementation Audit: mclaude-common / pkg/hostauth

**Component**: mclaude-common (specifically `pkg/hostauth`)
**Component root**: `mclaude-common/`
**Spec files**: `docs/mclaude-common/spec-common.md`, `docs/adr-0063-k8s-architecture-spec.md`
**Date**: 2026-05-01

## Run: 2026-05-01T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-common.md:124-125 | "Package `hostauth` — Shared NKey challenge-response authentication logic for host controllers. Both `mclaude-controller-k8s` and `mclaude-controller-local` import this package as `mclaude.io/common/pkg/hostauth`." | `host_auth.go:1-4` (package declaration, module path in go.mod) | IMPLEMENTED | — | Package exists at the correct path; module is `mclaude.io/common`. controller-local already imports it (main.go:16). controller-k8s has not yet been updated but that is a controller-k8s gap, not a common gap. |
| spec-common.md:129 | "`NewHostAuthFromCredsFile(credsPath string, cpURL string, log zerolog.Logger) (*HostAuth, error)` — reads a `.creds`-formatted file (NKey seed + pre-existing JWT). Used by `mclaude-controller-local` where a JWT is already present." | `host_auth.go:52-74` | IMPLEMENTED | — | Reads credsData via os.ReadFile; parses NKey via ParseDecoratedUserNKey; parses JWT via ParseDecoratedJWT; stores both; matches spec signature exactly. |
| spec-common.md:131-132 | "`NewHostAuthFromSeed(seedPath string, cpURL string, log zerolog.Logger) (*HostAuth, error)` — reads only the NKey seed from `seedPath`. No pre-existing JWT required. Used by `mclaude-controller-k8s`." | `host_auth.go:80-99` | IMPLEMENTED | — | Reads seed via os.ReadFile; parses NKey via ParseDecoratedUserNKey; sets `currentJWT: ""`; matches spec signature exactly. |
| spec-common.md:133-139 | "`Refresh(ctx context.Context) (jwt string, err error)` — HTTP challenge-response: (1) POST /api/auth/challenge {nkey_public} → {challenge}, (2) signs challenge with NKey, (3) POST /api/auth/verify {nkey_public, challenge, signature} → {jwt}, (4) returns new JWT." | `host_auth.go:140-178` (Refresh), `host_auth.go:214-246` (requestChallenge), `host_auth.go:250-288` (verifyChallenge) | IMPLEMENTED | — | All three steps implemented. POST body uses `nkey_public` snake_case (line 215, 251). Signature computed at line 163. JWT stored in currentJWT under mutex at lines 173-175. |
| spec-common.md:140-145 | "When constructed via `NewHostAuthFromSeed` and the public key is not yet registered (CP returns HTTP 404), `Refresh()` returns a sentinel retryable error and logs: `NKey <pubkey> not registered with control-plane. To complete registration run: mclaude host register --type cluster --name <display-name> --nkey-public <pubkey>`" | `host_auth.go:228-229` (ErrNotRegistered on 404), `host_auth.go:151-157` (log + return ErrNotRegistered) | IMPLEMENTED | — | HTTP 404 → `ErrNotRegistered` in requestChallenge (line 228-229). Refresh logs the exact message format (lines 153-156) and returns `ErrNotRegistered`. |
| spec-common.md:146 | "Callers must implement the retry loop. The recommended pattern for K8s: retry every 5s with exponential backoff capped at 60s." | `host_auth.go:30-32` (ErrNotRegistered comment), `host_auth.go:137` (Refresh comment) | IMPLEMENTED | — | Spec says callers implement the loop. Package documents this with "recommended: 5s initial, doubling, capped at 60s" in comments on ErrNotRegistered and Refresh. Package itself does not implement the boot retry loop, consistent with the spec placing this in callers. |
| spec-common.md:148 | "`PublicKey() string` — returns the NKey public key (U-prefix). Used to extract the key for logging or NOTES.txt display before JWT acquisition." | `host_auth.go:103-110` | IMPLEMENTED | — | Returns kp.PublicKey(); panics on error (documented as impossible for valid keypair). Starts with "U" for user-type NKeys. |
| adr-0063:150-154 | "Move `mclaude-controller-local/host_auth.go` to `mclaude-common/pkg/hostauth/`, importable by both controllers as `mclaude.io/common/pkg/hostauth`. `NewHostAuthFromCredsFile` constructor is kept unchanged." | `host_auth.go:52-74` | IMPLEMENTED | — | Package exists at the correct path. controller-local already imports from `mclaude.io/common/pkg/hostauth` (confirmed in main.go:16). |
| adr-0063:150-154 (NewHostAuthFromSeed) | "For K8s, add a new constructor: `NewHostAuthFromSeed(seedPath string, cpURL string, log zerolog.Logger) (*HostAuth, error)` — reads NKey seed from seedPath, derives key pair, stores cpURL, sets jwt=\"\"." | `host_auth.go:80-99` | IMPLEMENTED | — | Exact match on signature and behavior. |
| adr-0063:156-161 (404 behavior) | "On `Refresh()` when jwt==\"\" (initial boot), controller calls challenge+verify. If CP returns HTTP 404, method logs the registration instruction and returns a retryable sentinel error." | `host_auth.go:140-178`, `host_auth.go:151-157` | IMPLEMENTED | — | The guard is actually on cpURL empty (line 141-143) rather than jwt=="" but the 404 log+sentinel path fires regardless of which constructor was used. The JWT-empty check is in the caller's responsibility (boot before NATS connect). |
| adr-0063:161 (boot loop location) | "The K8s controller's main boot loop retries `Refresh()` on a 5-second interval, doubling each attempt up to a 60-second cap." | Not in mclaude-common | IMPLEMENTED | — | ADR-0063 body places this loop in the K8s controller (not the package). spec-common.md line 146 confirms "Callers must implement the retry loop." Package is not required to contain the boot loop — this is correct. |
| adr-0063:262 + 301 | Implementation Plan: "add `NewHostAuthFromSeed` with 5s/60s-capped retry boot loop" (listed under mclaude-common changes) | `host_auth.go:80-99` only; no retry loop in package | PARTIAL | SPEC→FIX | The Implementation Plan table at line 262 and 301 says "5s/60s-capped retry boot loop" belongs in mclaude-common, but the ADR body text (line 161) says "K8s controller's main boot loop" and spec-common.md line 146 says "Callers must implement the retry loop." The package does NOT implement the boot retry loop. The Implementation Plan is inconsistent with the ADR body and spec-common.md. The code follows the body + spec-common (caller-responsibility), which is the correct design. The Implementation Plan table description should be updated to remove "5s/60s-capped retry boot loop" from the mclaude-common row. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|----------------|-------------|
| `host_auth.go:22-27` (constants `hostJWTTTL`, `hostJWTRefreshBuffer`) | INFRA | Constants used by StartRefreshLoop; necessary support infrastructure for the JWT refresh loop described by ADR-0054 (5-min TTL, proactive refresh). |
| `host_auth.go:29-32` (`ErrNotRegistered` sentinel) | INFRA | Exported sentinel error required by spec-common.md:140 ("returns a sentinel retryable error"). Name matches what the spec implies. |
| `host_auth.go:37-44` (`HostAuth` struct) | INFRA | Internal state struct for the package. Necessary plumbing; fields (kp, cpURL, log, mu, currentJWT) are all required by the spec'd constructors and methods. |
| `host_auth.go:115-122` (`JWTFunc()`) | UNSPEC'd | Returns a closure over currentJWT for use with nats.UserJWT(). Not described by spec-common.md or ADR-0063. However, it is clearly necessary infrastructure: NATS client requires a function callback, not a raw string. This is a practical interface bridging the spec'd HostAuth to the NATS client. Likely omitted from spec because it is a NATS-specific integration detail. Flag for spec documentation. |
| `host_auth.go:126-129` (`SignFunc()`) | UNSPEC'd | Returns a closure over kp.Sign for use with nats.UserJWT(). Same situation as JWTFunc() — not described by spec but required for NATS NKey authentication. Should be added to spec-common.md for completeness. |
| `host_auth.go:187-210` (`StartRefreshLoop(ctx)`) | UNSPEC'd | Starts a background goroutine to proactively refresh JWT before TTL expiry. ADR-0054 specifies "JWT lifetime: held in memory only, refreshed via challenge-response code path before each 5-min TTL expiry" (referenced in component changes). Not described in spec-common.md pkg/hostauth section. Clearly necessary behavior derived from ADR-0054's JWT-TTL requirement; should be added to spec-common.md. |
| `host_auth.go:214-246` (`requestChallenge` private) | INFRA | Private helper extracted from Refresh(). Implements step 1 of the challenge-response flow described in spec-common.md:134-135. Necessary plumbing. |
| `host_auth.go:250-288` (`verifyChallenge` private) | INFRA | Private helper extracted from Refresh(). Implements steps 3-4 of the challenge-response flow described in spec-common.md:136-138. Necessary plumbing. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-common.md:129 | `NewHostAuthFromCredsFile` | `TestNewHostAuthFromCredsFile_Valid`, `TestNewHostAuthFromCredsFile_InvalidPath`, `TestNewHostAuthFromCredsFile_InvalidContent` | None | UNIT_ONLY | Critical auth flow. Tests use httptest (not a real CP). No integration test against a real CP server exists. FLAG: auth flow must have integration test. |
| spec-common.md:131-132 | `NewHostAuthFromSeed` | `TestNewHostAuthFromSeed_Valid`, `TestNewHostAuthFromSeed_InvalidPath`, `TestNewHostAuthFromSeed_InvalidContent` | None | UNIT_ONLY | Same as above — critical auth bootstrap. |
| spec-common.md:133-139 | `Refresh(ctx)` full flow | `TestRefresh_FullFlow`, `TestRefresh_UpdatesStoredJWT`, `TestRefresh_ErrorWhenNoCPURL`, `TestRefresh_SignatureCorrectness` | None | UNIT_ONLY | Auth flow critical path (challenge + verify + JWT update). Uses httptest mock, not real CP. No integration test. |
| spec-common.md:140-145 | `Refresh()` HTTP 404 → `ErrNotRegistered` + log | `TestRefresh_404ReturnsErrNotRegistered` | None | UNIT_ONLY | Critical error path. No integration test against real CP. |
| spec-common.md:148 | `PublicKey()` returns U-prefix key | `TestPublicKey_Format`, `TestPublicKey_FromSeed` | None | UNIT_ONLY | Not a critical auth flow per se (read-only key extraction); unit test is sufficient. |

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` reference mclaude-common or pkg/hostauth.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No bugs filed against this component. |

### Summary

- Implemented: 10
- Gap: 0
- Partial: 1 (SPEC→FIX: ADR-0063 Implementation Plan table says "5s/60s-capped retry boot loop" in mclaude-common, but ADR body + spec-common both correctly place the boot retry loop in callers)
- Infra: 5
- Unspec'd: 3 (`JWTFunc`, `SignFunc`, `StartRefreshLoop` — all clearly necessary but not described in spec-common.md)
- Dead: 0
- Tested (unit): 5 spec lines covered by unit tests
- Unit only: 5 (all critical auth paths have unit tests but no integration tests)
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0
