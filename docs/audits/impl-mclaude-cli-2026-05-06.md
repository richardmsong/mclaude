## Run: 2026-05-06T00:00:00Z

Component: mclaude-cli
Scope: ADR-0092 — `mclaude auth anthropic --revoke` extended to publish token-revoke to all accepted-grant hosts
Primary spec: docs/mclaude-cli/spec-cli.md §mclaude auth anthropic
ADR: docs/adr-0092-anthropic-token-ux.md §mclaude-cli (line 302)
Cross-cutting: docs/spec-nats-data-taxonomy.md

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-cli.md:155 | "`--revoke` deletes the KV key and publishes `token-revoke` to all accepted-grant hosts (ADR-0092)" | anthropic.go:758-759 (runRevoke dispatch) | IMPLEMENTED | — | `flags.Revoke` branch calls `runRevoke` which does KV delete then iterates accepted hosts |
| spec-cli.md:155 | "Behavior: delete token from KV first (always succeeds)" | anthropic.go:874-877 | IMPLEMENTED | — | `kvDeleteToken` called first; error returns `ExitAuthKVFailed` |
| spec-cli.md:155 | "then iterate accepted-grant hosts and send `token-revoke` request/reply to each (10s timeout per host)" | anthropic.go:880-895 (fetchAcceptedGrants + loop); anthropic.go:835-841 (SendTokenRevoke 10s timeout) | IMPLEMENTED | — | `fetchAcceptedGrants` fetches accepted hosts; loop calls `SendTokenRevoke`; timeout is 10*time.Second |
| spec-cli.md:155 | "Continue on per-host failure — print `'Warning: could not revoke on {hslug}: {error}'` to stderr" | anthropic.go:893-894 | IMPLEMENTED | — | `fmt.Fprintf(errOut, "Warning: could not revoke on %s: %v\n", hslug, err)` |
| spec-cli.md:155 | "Exit code: `ExitAuthSuccess` (0) if KV delete succeeded (even if some hosts failed)" | anthropic.go:911 | IMPLEMENTED | — | `return &AuthAnthropicResult{ExitCode: ExitAuthSuccess}, nil` always returned when KV delete succeeded |
| spec-cli.md:155 | "Summary messages: all hosts succeeded → `'Token removed. Revoked on N/N hosts.'`" | anthropic.go:903-904 | IMPLEMENTED | — | `fmt.Fprintf(out, "Token removed. Revoked on %d/%d hosts.\n", revokedCount, total)` when revokedCount==total |
| spec-cli.md:155 | "Partial failure → `'Token removed. Revoked on N/M hosts.'`" | anthropic.go:908-909 | IMPLEMENTED | — | else branch: same format `"Token removed. Revoked on %d/%d hosts.\n"` |
| spec-cli.md:155 | "All hosts failed → `'Token removed from account. Warning: N hosts could not be reached — token may still be cached on those hosts.'`" | anthropic.go:905-907 | IMPLEMENTED | — | `fmt.Fprintf(out, "Token removed from account. Warning: %d hosts could not be reached…\n", total)` when revokedCount==0 |
| adr-0092:302 | "NATS subject `mclaude.users.{uslug}.hosts.{hslug}.agent.token-revoke`" | anthropic.go:841 | IMPLEMENTED | — | `subject := fmt.Sprintf("mclaude.users.%s.hosts.%s.agent.token-revoke", uslug, hslug)` — exact match |
| adr-0092:302 | "Request payload `{ 'provider': 'anthropic' }`" | anthropic.go:829-831 (tokenRevokePayload); anthropic.go:836-838 | IMPLEMENTED | — | `tokenRevokePayload{Provider: "anthropic"}` marshalled and sent |
| adr-0092:302 | "User-agent replies `{ 'ok': true }` on success or `{ 'ok': false, 'error': '...' }` on failure" | anthropic.go:847-854 | IMPLEMENTED | — | Response parsed: checks `ok` bool and `error` string |
| adr-0092:302 | "SPA uses `nc.Request()` with 10s timeout" (CLI spec mirrors same timeout) | anthropic.go:843 | IMPLEMENTED | — | `nc.Request(subject, data, 10*time.Second)` |
| spec-nats-data-taxonomy.md:67 | "`token-revoke` listed under `agent.>` hierarchy" | anthropic.go:841 | IMPLEMENTED | — | Subject uses `agent.token-revoke` suffix, consistent with taxonomy |


### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| anthropic.go:1-18 | INFRA | Package header + file-level doc comment. Note: line 11 comment says `--revoke — delete tokens.anthropic from KV (client-side only)` — stale since ADR-0092 extended revoke to broadcast to hosts. The function implementation is correct; the package-level comment is a stale description. Not a behavioral gap, just a stale comment. |
| anthropic.go:21-42 | INFRA | Import block — all imports required for OAuth, NATS, X25519, HTTP, JSON, CLI context |
| anthropic.go:48-73 | INFRA | `AnthropicOAuthConfig` struct + `DefaultAnthropicOAuthConfig()` — PKCE OAuth config, spec'd by ADR-0091 |
| anthropic.go:80-132 | INFRA | `AuthAnthropicFlags`, `AuthAnthropicResult`, `HostDeliveryResult` structs + exit code constants — test/injection infrastructure for the spec'd command |
| anthropic.go:139-215 | INFRA | `KVEntry`, `KVBucket`, `KVAccessor`, `jsKVAccessor`, `realJSKVAccessor`, `realKVBucket` interfaces + implementations — NATS KV adapters required for KV operations spec'd by ADR-0091 |
| anthropic.go:216-276 | INFRA | `connectNATSForAnthropic`, `kvPutToken`, `kvGetToken`, `kvDeleteToken` — NATS/KV plumbing for spec'd operations |
| anthropic.go:282-359 | INFRA | `grantsResponse`, `grantEntry`, `fetchAcceptedGrants`, `pubkeyResponse`, `fetchUserAgentPubkey` — HTTP helpers for CP grant/pubkey endpoints, required by spec'd delivery + revoke flows |
| anthropic.go:364-435 | INFRA | `SealToken`, `tokenDeliveryPayload`, `DeliverTokenToHost` — X25519 sealed-box and token delivery, spec'd by ADR-0091 |
| anthropic.go:440-683 | INFRA | `PKCEParams`, `GeneratePKCE`, `ComputeS256Challenge`, `StartLoopbackListener`, `openBrowser`, `BuildAuthURL`, `LoopbackCallbackResult`, `WaitForCallback`, `tokenExchangeResponse`, `ExchangeCodeForToken`, `runPKCEOAuth` — PKCE OAuth flow, spec'd by ADR-0091 |
| anthropic.go:689-824 | INFRA | `RunAuthAnthropic` main entry point + `deliverToAcceptedHosts` — orchestrates all sub-commands, spec'd by ADR-0091 and ADR-0092 |
| anthropic.go:826-856 | INFRA | `tokenRevokePayload`, `SendTokenRevoke` — payload type and low-level NATS call for token-revoke, directly spec'd by ADR-0092 |
| anthropic.go:858-912 | INFRA | `runRevoke` — implements the ADR-0092 revoke flow (already fully covered in Phase 1) |
| anthropic.go:914-960 | INFRA | `runStatus` — implements `--status` subcommand, spec'd by ADR-0091 spec-cli.md |
| anthropic.go:962-1007 | INFRA | `deliverToAcceptedHosts` — token delivery loop, spec'd by ADR-0091 |
| anthropic.go:1010-1042 | INFRA | `injectedNATSConnWrapper`, `AnthropicTestNATSConn` — test injection helpers; necessary to make the spec'd interfaces testable without real NATS |


### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-cli.md:155 | `--revoke` deletes KV key first | `TestRunAuthAnthropic_Revoke_NoHosts` (anthropic_test.go:961); `TestRunAuthAnthropic_Revoke_AllHostsSucceed` (anthropic_test.go:1019) | None | UNIT_ONLY | No integration test against a real NATS server verifies KV deletion + host broadcast together. ADR-0092 integration test cases list "Disconnect revokes all hosts" but that is a web/SPA test, not CLI. CLI revoke has no integration test case specified in either ADR, and none implemented. |
| spec-cli.md:155 | iterate accepted-grant hosts, send token-revoke (10s timeout) | `TestRunAuthAnthropic_Revoke_AllHostsSucceed` (anthropic_test.go:1019); `TestSendTokenRevoke_SendsToCorrectSubject` (anthropic_test.go:1296) | None | UNIT_ONLY | Subject pattern verified via mock. No real NATS request/reply against an actual cluster. |
| spec-cli.md:155 | per-host failure: warning to stderr, continue | `TestRunAuthAnthropic_Revoke_PartialFailure` (anthropic_test.go:1105); `TestRunAuthAnthropic_Revoke_AllHostsFail` (anthropic_test.go:1172) | None | UNIT_ONLY | — |
| spec-cli.md:155 | exit code ExitAuthSuccess even on host failures | `TestRunAuthAnthropic_Revoke_PartialFailure` (anthropic_test.go:1150); `TestRunAuthAnthropic_Revoke_AllHostsFail` (anthropic_test.go:1214) | None | UNIT_ONLY | — |
| spec-cli.md:155 | summary: "Token removed. Revoked on N/N hosts." | `TestRunAuthAnthropic_Revoke_AllHostsSucceed` (anthropic_test.go:1092-1098) | None | UNIT_ONLY | — |
| spec-cli.md:155 | summary: partial "Token removed. Revoked on N/M hosts." | `TestRunAuthAnthropic_Revoke_PartialFailure` (anthropic_test.go:1165-1170) | None | UNIT_ONLY | — |
| spec-cli.md:155 | summary: all-fail "Token removed from account. Warning: N hosts could not be reached…" | `TestRunAuthAnthropic_Revoke_AllHostsFail` (anthropic_test.go:1232-1238) | None | UNIT_ONLY | — |
| adr-0092:302 | NATS subject `…agent.token-revoke`, payload `{provider: "anthropic"}` | `TestSendTokenRevoke_SendsToCorrectSubject` (anthropic_test.go:1296); `TestRunAuthAnthropic_Revoke_AllHostsSucceed` (anthropic_test.go:1073-1090) | None | UNIT_ONLY | Integration test required per the critical-paths bar (NATS pub/sub with real subject permissions). |
| adr-0092:302 | response `{ok: false, error: "..."}` counted as failure | `TestSendTokenRevoke_AgentError` (anthropic_test.go:1321); `TestRunAuthAnthropic_Revoke_AgentErrorResponse` (anthropic_test.go:1240) | None | UNIT_ONLY | — |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | No open bugs with Component: mclaude-cli found in `.agent/bugs/` | — | Checked all `.agent/bugs/*.md` — none matched mclaude-cli |

### Summary

- Implemented: 13
- Gap: 0
- Partial: 0
- Infra: 15
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 8
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0

