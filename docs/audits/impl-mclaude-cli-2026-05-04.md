# Implementation Audit — mclaude-cli (auth anthropic)

**Component**: mclaude-cli
**Component root**: mclaude-cli/
**Spec files**: docs/mclaude-cli/spec-cli.md (§mclaude auth anthropic), docs/adr-0091-per-user-anthropic-token.md
**ADR-0092 status**: draft — excluded (not accepted/implemented)
**Auditor**: impl-evaluator
**Date**: 2026-05-04

Focus: `mclaude auth anthropic` subcommand only.

---

## Run: 2026-05-04T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-cli.md:137-145 | `mclaude auth anthropic` command exists; connects token to `mclaude-user-info-{uslug}` KV and delivers to trusted hosts | main.go:76, main.go:259-308, cmd/anthropic.go:687-814 | IMPLEMENTED | — | `runAuth` dispatches to `runAuthAnthropic`; full flow in `RunAuthAnthropic` |
| spec-cli.md:141 | `mclaude auth anthropic` (no flags) — OAuth PKCE loopback flow in default browser | cmd/anthropic.go:757-778, cmd/anthropic.go:631-679 | IMPLEMENTED | — | `runPKCEOAuth` handles PKCE with loopback listener and browser open |
| spec-cli.md:142 | `mclaude auth anthropic --token <v>` — manual paste; skips OAuth, same downstream path | cmd/anthropic.go:759-761, main.go:282-287 | IMPLEMENTED | — | `flags.Token != ""` branch skips OAuth and goes directly to KV write + delivery |
| spec-cli.md:143 | `mclaude auth anthropic --status` — print connected/not-set + per-host trust status | cmd/anthropic.go:752-755, cmd/anthropic.go:829-874 | IMPLEMENTED | — | `runStatus` reads KV token + per-host pubkey |
| spec-cli.md:144 | `mclaude auth anthropic --revoke` — delete tokens.anthropic from KV (client-side only) | cmd/anthropic.go:748-750, cmd/anthropic.go:817-826 | IMPLEMENTED | — | `runRevoke` calls `kvDeleteToken`; output notes deferred host-side cleanup |
| spec-cli.md:149 | Step 1: Obtain token — PKCE OAuth opens system browser to claude.ai; waits on loopback HTTP listener; exchanges code for access_token | cmd/anthropic.go:490-505 (openBrowser), cmd/anthropic.go:507-518 (BuildAuthURL), cmd/anthropic.go:532-575 (WaitForCallback), cmd/anthropic.go:588-629 (ExchangeCodeForToken), cmd/anthropic.go:631-679 (runPKCEOAuth) | IMPLEMENTED | — | Full PKCE loopback flow |
| spec-cli.md:149 | `--token <v>` skips OAuth and uses supplied value directly | cmd/anthropic.go:759-761 | IMPLEMENTED | — | `if flags.Token != "" { token = flags.Token }` |
| spec-cli.md:150 | Step 2: Authenticate to mclaude (uses existing user JWT from `~/.mclaude/auth.json`) | cmd/anthropic.go:698-715 | IMPLEMENTED | — | `LoadAuth` reads JWT + NKeySeed from auth.json |
| spec-cli.md:151 | Step 3: Write token to `mclaude-user-info-{uslug}` at key `tokens.anthropic` via NATS KV PUT | cmd/anthropic.go:231-241, cmd/anthropic.go:781-786 | IMPLEMENTED | — | `kvPutToken` uses bucket name `mclaude-user-info-{uslug}`, key `tokens.anthropic` |
| spec-cli.md:152-153 | Step 4: For each host in `GET /api/users/{uslug}/grants` with `status='accepted'`: read pubkey → `crypto_box_seal(token, x25519_pub)` → publish via NATS request/reply to `mclaude.users.{uslug}.hosts.{hslug}.agent.token-delivery` with `{provider: "anthropic", token_wrapped: "<base64>"}` | cmd/anthropic.go:290-319 (fetchAcceptedGrants), cmd/anthropic.go:327-355 (fetchUserAgentPubkey), cmd/anthropic.go:402-431 (DeliverTokenToHost), cmd/anthropic.go:877-921 (deliverToAcceptedHosts) | IMPLEMENTED | — | Filters on `status == "accepted"`; seals with `box.SealAnonymous`; delivers via NATS |
| spec-cli.md:152 | Read pubkey via `GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey` | cmd/anthropic.go:327-355 | IMPLEMENTED | — | `fetchUserAgentPubkey` calls correct endpoint |
| spec-cli.md:153 | Reports per-host result (delivered / agent-offline / error) | cmd/anthropic.go:895-919 | IMPLEMENTED | — | Per-host stdout reporting; `HostDeliveryResult` slice returned |
| spec-cli.md:154 | `--revoke` only deletes KV key; host-side cleanup is deferred | cmd/anthropic.go:817-826 | IMPLEMENTED | — | Output explicitly notes "host-side cleanup (Secret deletion, JWT revocation) is deferred to a future release" |
| spec-cli.md:156-157 | `--status` prints `Connected` or `Not set` based on KV read; for each accepted host, prints whether user-agent has registered X25519 pubkey | cmd/anthropic.go:829-874 | IMPLEMENTED | — | Checks KV for token, then per-host pubkey registration |
| spec-cli.md:159 | Exit code 0 — success on all hosts | cmd/anthropic.go:119-128, cmd/anthropic.go:796-808 | IMPLEMENTED | — | `ExitAuthSuccess = 0` |
| spec-cli.md:159 | Exit code 1 — token saved but ≥1 host could not receive it (transient) | cmd/anthropic.go:119-128, cmd/anthropic.go:796-802 | IMPLEMENTED | — | `ExitAuthPartialDelivery = 1` |
| spec-cli.md:159 | Exit code 2 — no token configured / OAuth aborted | cmd/anthropic.go:119-128, cmd/anthropic.go:770-777 | IMPLEMENTED | — | `ExitAuthNoToken = 2`; returned on OAuth abort and --status not-set |
| spec-cli.md:159 | Exit code 3 — KV write failed | cmd/anthropic.go:119-128, cmd/anthropic.go:782-785 | IMPLEMENTED | — | `ExitAuthKVFailed = 3` |
| adr-0091:179 | OAuth path (CLI): `mclaude auth anthropic` opens browser; handles PKCE loopback → writes token to `mclaude-user-info-{uslug} / tokens.anthropic` | cmd/anthropic.go:631-679, cmd/anthropic.go:231-241 | IMPLEMENTED | — | Steps 1–3 of token setup flow |
| adr-0091:185 | Manual paste path (CLI): `mclaude auth anthropic --token <value>` → runs steps 1–3 directly | cmd/anthropic.go:759-786 | IMPLEMENTED | — | Steps 1–3 without OAuth |
| adr-0091:316 | New command `mclaude auth anthropic`: OAuth or token paste → write token to `mclaude-user-info-{uslug}` KV → deliver token_wrapped to accessible hosts via NATS request/reply | cmd/anthropic.go:687-814 | IMPLEMENTED | — | Full flow present |
| adr-0091:317 | New command `mclaude auth anthropic --status` — shows connected/not set | cmd/anthropic.go:829-874 | IMPLEMENTED | — | Shows per-host pubkey status as well |
| adr-0091:318 | New command `mclaude auth anthropic --revoke` — delete token from `mclaude-user-info-{uslug}` KV; client-side only; host-side cleanup deferred | cmd/anthropic.go:817-826 | IMPLEMENTED | — | Matches spec exactly including deferred notice |
| adr-0091:116 | token_wrapped payload: `{ "provider": "anthropic", "token_wrapped": "<base64>" }` via NATS request/reply to `mclaude.users.{uslug}.hosts.{hslug}.agent.token-delivery` | cmd/anthropic.go:393-397, cmd/anthropic.go:417 | IMPLEMENTED | — | JSON fields `provider` and `token_wrapped`; subject format matches |
| adr-0091:193 | `token_wrapped = crypto_box_seal(token, ua_x25519_pub)` (NaCl sealed box) | cmd/anthropic.go:366-386 (SealToken uses `box.SealAnonymous`) | IMPLEMENTED | — | Uses `golang.org/x/crypto/nacl/box.SealAnonymous` which is NaCl anonymous sealed box |
| adr-0091:192 | Read user-agent X25519 pubkey from CP: `GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey` | cmd/anthropic.go:327-355 | IMPLEMENTED | — | Correct endpoint path |
| adr-0091:614 (mclaude-cli est.) | ~100 lines; `auth anthropic` command, X25519 wrap, NATS request/reply | cmd/anthropic.go (956 lines total) | IMPLEMENTED | — | Estimate was low; implementation includes full PKCE OAuth infrastructure; scope increase is justified |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| cmd/anthropic.go:1-19 (package + imports) | INFRA | Package declaration and imports; necessary plumbing for the command |
| cmd/anthropic.go:49-62 (AnthropicOAuthConfig struct) | INFRA | Exported struct with OAuth endpoints; necessary for PKCE flow and test injection per spec |
| cmd/anthropic.go:65-72 (DefaultAnthropicOAuthConfig) | INFRA | Returns production OAuth config; required entry point for the PKCE flow |
| cmd/anthropic.go:79-98 (AuthAnthropicFlags) | INFRA | Flags struct for the command; test injection fields (ContextPath, AuthPath, OAuthConfig, NATSConn, HTTPClient, ServerURL) are plumbing for testability |
| cmd/anthropic.go:101-113 (AuthAnthropicResult, HostDeliveryResult) | INFRA | Return types for RunAuthAnthropic; carry exit code and per-host results per spec |
| cmd/anthropic.go:119-128 (ExitAuth* constants) | INFRA | Exit code constants directly from spec exit codes table |
| cmd/anthropic.go:135-157 (KVEntry, KVBucket, KVAccessor interfaces, jsKVAccessor alias) | INFRA | Narrow interfaces over nats.KeyValue; allow test mocking of NATS KV. The alias `jsKVAccessor = KVAccessor` is internal renaming; no dead code |
| cmd/anthropic.go:160-186 (realJSKVAccessor, realKVBucket) | INFRA | Production adapters that bridge real nats.JetStreamContext/nats.KeyValue to the narrow interfaces; required for production path |
| cmd/anthropic.go:194-228 (anthropicNATSConn interface, natsConnWrapper, connectNATSForAnthropic) | INFRA | Wraps *nats.Conn with JetStream KV accessor; production NATS connection used in RunAuthAnthropic |
| cmd/anthropic.go:231-272 (kvPutToken, kvGetToken, kvDeleteToken) | INFRA | Implement the three KV operations needed for token write, status read, and revoke per spec |
| cmd/anthropic.go:279-319 (grantsResponse, grantEntry, fetchAcceptedGrants) | INFRA | HTTP helper for GET /api/users/{uslug}/grants; required for step 4 delivery loop |
| cmd/anthropic.go:322-355 (pubkeyResponse, fetchUserAgentPubkey) | INFRA | HTTP helper for GET /api/users/{uslug}/hosts/{hslug}/user-agent/pubkey; required for delivery |
| cmd/anthropic.go:366-386 (SealToken) | INFRA | Implements crypto_box_seal(token, x25519_pub) as specified in ADR-0091 §Phase 3 |
| cmd/anthropic.go:393-431 (tokenDeliveryPayload, DeliverTokenToHost) | INFRA | Implements NATS request/reply delivery with correct subject and payload shape per ADR-0091 |
| cmd/anthropic.go:439-466 (PKCEParams, GeneratePKCE) | INFRA | PKCE code_verifier + challenge generation; required for OAuth path |
| cmd/anthropic.go:469-473 (ComputeS256Challenge) | INFRA | SHA256 S256 challenge computation; extracted for testability; used by GeneratePKCE |
| cmd/anthropic.go:476-488 (StartLoopbackListener) | INFRA | Opens TCP listener for OAuth redirect; spec says "loopback HTTP listener" |
| cmd/anthropic.go:491-505 (openBrowser) | INFRA | OS-specific browser opener; spec says "opens the system browser" |
| cmd/anthropic.go:507-518 (BuildAuthURL) | INFRA | Constructs OAuth authorization URL with PKCE params; required for PKCE flow |
| cmd/anthropic.go:522-526 (LoopbackCallbackResult) | INFRA | Return type for WaitForCallback; carries code/state/error from redirect |
| cmd/anthropic.go:532-575 (WaitForCallback) | INFRA | HTTP server that waits for the OAuth redirect; spec: "waits on a loopback HTTP listener" |
| cmd/anthropic.go:578-584 (tokenExchangeResponse) | INFRA | Response type for token exchange endpoint |
| cmd/anthropic.go:588-629 (ExchangeCodeForToken) | INFRA | Exchanges OAuth code for access_token; spec: "exchanges code for access_token" |
| cmd/anthropic.go:632-679 (runPKCEOAuth) | INFRA | Orchestrates the full PKCE OAuth flow (PKCE → listener → browser → callback → exchange) |
| cmd/anthropic.go:687-814 (RunAuthAnthropic) | INFRA | Main entry point; implements the full command flow per spec |
| cmd/anthropic.go:817-826 (runRevoke) | INFRA | Implements --revoke sub-behavior |
| cmd/anthropic.go:829-874 (runStatus) | INFRA | Implements --status sub-behavior |
| cmd/anthropic.go:877-921 (deliverToAcceptedHosts) | INFRA | Delivery loop across accepted hosts; core of step 4 |
| cmd/anthropic.go:924-955 (injectedNATSConnWrapper, AnthropicTestNATSConn) | INFRA | Test injection plumbing; `injectedNATSConnWrapper` wraps injected NATSConn for tests; `AnthropicTestNATSConn` is the exported interface tests implement. These live in production code to be accessible to the external `cmd_test` package. Standard Go pattern; not dead code. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| spec-cli.md:141 | OAuth PKCE loopback flow | TestWaitForCallback_ReceivesCode, TestExchangeCodeForToken_Success, TestBuildAuthURL_ContainsRequiredParams, TestGeneratePKCE_* | None | UNIT_ONLY |
| spec-cli.md:142 | --token manual paste; skips OAuth | TestRunAuthAnthropic_ManualToken_NoHosts, TestRunAuthAnthropic_ManualToken_DeliversToHost | None | UNIT_ONLY |
| spec-cli.md:143 | --status: Connected/Not set + per-host pubkey | TestRunAuthAnthropic_Status_NotSet, TestRunAuthAnthropic_Status_Connected, TestRunAuthAnthropic_Status_HostNoPubkey | None | UNIT_ONLY |
| spec-cli.md:144 | --revoke: delete tokens.anthropic from KV | TestRunAuthAnthropic_Revoke, TestRunAuthAnthropic_Revoke_MentionsClientSideOnly | None | UNIT_ONLY |
| spec-cli.md:149 | PKCE: code_verifier + code_challenge generation (S256) | TestGeneratePKCE_VerifierLength, TestGeneratePKCE_ChallengeIsS256, TestGeneratePKCE_UniqueEachCall | None | UNIT_ONLY |
| spec-cli.md:149 | PKCE: loopback listener receives OAuth callback | TestStartLoopbackListener_BindsPort, TestWaitForCallback_ReceivesCode, TestWaitForCallback_ErrorResponse | None | UNIT_ONLY |
| spec-cli.md:149 | Token exchange (code → access_token) | TestExchangeCodeForToken_Success, TestExchangeCodeForToken_ErrorResponse, TestExchangeCodeForToken_EmptyAccessToken | None | UNIT_ONLY |
| spec-cli.md:151 | Write token to KV at tokens.anthropic | TestRunAuthAnthropic_ManualToken_NoHosts (verifies KV write) | None | UNIT_ONLY |
| spec-cli.md:152-153 | Seal token (crypto_box_seal), NATS delivery, per-host result | TestSealToken_ProducesBase64Ciphertext, TestSealToken_UniqueEachCall, TestDeliverTokenToHost_SendsToCorrectSubject, TestDeliverTokenToHost_TokenWrappedDecryptable, TestDeliverTokenToHost_AgentError, TestRunAuthAnthropic_ManualToken_DeliversToHost | None | UNIT_ONLY |
| spec-cli.md:159 | Exit codes 0/1/2/3 | TestRunAuthAnthropic_NotLoggedIn (exit 2), TestRunAuthAnthropic_ManualToken_NoHosts (exit 0), TestRunAuthAnthropic_ManualToken_PartialDelivery (exit 1) | None | UNIT_ONLY |
| spec-cli.md:154 | --revoke: client-side only note | TestRunAuthAnthropic_Revoke_MentionsClientSideOnly | None | UNIT_ONLY |

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` have `Component: mclaude-cli` matching this component. The two open bugs (`002-droid-shield-false-positives.md`, `003-initial-message-injection-race.md`, `002-ptt-race-condition.md`, `005-getting-started-canned-text.md`) are UI/SPA/session-agent bugs unrelated to `mclaude-cli`.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No open bugs for mclaude-cli |

### Summary

- Implemented: 24
- Gap: 0
- Partial: 0
- Infra: 29
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 11
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0
