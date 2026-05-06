# Implementation Audit: mclaude-cli

## Run: 2026-05-06T00:00:00Z

Component root: `mclaude-cli/`
Specs evaluated: `docs/mclaude-cli/spec-cli.md`, `docs/adr-0097-unify-host-model.md` (accepted), `docs/spec-state-schema.md` (cross-cutting)

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-cli.md:15-17 | `mclaude-cli attach <session-id>` — connects to session agent unix socket, REPL | main.go:61-63, repl/repl.go:48-217 | IMPLEMENTED | — | Full REPL implemented |
| spec-cli.md:20 | `--socket <path>` flag, default `/tmp/mclaude-session-{id}.sock` | main.go:45, 121-135 | IMPLEMENTED | — | Default matches `socketFmt` constant |
| spec-cli.md:21 | `--log-machine` emits structured JSON logs to stderr | main.go:148-160 | IMPLEMENTED | — | zerolog JSON mode |
| spec-cli.md:22 | `--log-level <level>` debug/info/warn/error | main.go:135-146 | IMPLEMENTED | — | zerolog.ParseLevel |
| spec-cli.md:27-30 | `mclaude-cli session list` resolves slugs, prints NATS KV key prefix, no network calls | cmd/session.go:49-137 | IMPLEMENTED | — | Returns KVKeyPrefix and EventsSubject without network calls |
| spec-cli.md:33-35 | `-u <uslug>` user slug flag, default from context.json | cmd/session.go:71-84, main.go:317-325 | IMPLEMENTED | — | |
| spec-cli.md:34-35 | `-p <pslug>` project slug flag, accepts `@pslug` short form, default from context | cmd/session.go:85-97, context/context.go:159-167 | IMPLEMENTED | — | |
| spec-cli.md:39-50 | `mclaude login` device-code flow: generates NKey pair locally, POSTs /api/auth/device-code with {publicKey} | cmd/login.go:101-119 | IMPLEMENTED | — | |
| spec-cli.md:47 | CP always responds HTTP 200; response body `{ "status": "pending" | "authorized", ... }` | cmd/login.go:229-235, 66-81 | IMPLEMENTED | — | |
| spec-cli.md:48 | Poll response: `jwt`, `userSlug`, `natsUrl` omitted when pending | cmd/login.go:72-81 | IMPLEMENTED | — | `omitempty` on JWT, UserSlug, NATSUrl |
| spec-cli.md:50 | CLI writes `~/.mclaude/auth.json` (mode 0600) with `{ jwt, nkeySeed, userSlug, natsUrl }` | cmd/auth.go:62-78 | IMPLEMENTED | — | mode 0600 at line 74 |
| spec-cli.md:50 | `natsUrl` field omitted when poll response returned empty value | cmd/auth.go:26 `json:"natsUrl,omitempty"` | IMPLEMENTED | — | omitempty |
| spec-cli.md:54 | JWT refresh deferred — user must re-run `mclaude login` | No refresh code exists | IMPLEMENTED | — | Spec explicitly marks this deferred |
| spec-cli.md:58-60 | `--server <url>` flag, defaults to context.json server or `https://api.mclaude.internal` | context/context.go:58-70, main.go:199-206 | IMPLEMENTED | — | |
| spec-cli.md:63-86 | `mclaude import` full 11-step flow | cmd/import.go:471-668 | IMPLEMENTED | — | All 11 steps present |
| spec-cli.md:68 | CWD encoding: replace `/` with `-`, leading `-` preserved | cmd/import.go:146-149 | PARTIAL | SPEC→FIX | Spec says "The leading `-` is preserved" and gives example `-Users-rsong-work-mclaude`, but code strips the leading `-` via `strings.TrimPrefix(encoded, "-")`. The code is correct for actual Claude Code directory naming (no leading dash). Spec is self-contradictory: it says leading `-` is preserved but also "This matches the directory names under `~/.claude/projects/`" — those dirs have no leading dash. Spec must be corrected to say the leading `-` is stripped. |
| spec-cli.md:73-86 | Import steps 1-11 (all) | cmd/import.go:483-668 | IMPLEMENTED | — | (See individual step rows above) |
| spec-cli.md:95-107 | `mclaude host register --name --nkey-public [--default-platform]` — registers via NATS, validates NKey format | cmd/host.go:127-252 | IMPLEMENTED | — | |
| spec-cli.md:97 | `--nkey-public` required, validates 56-char base32 U-prefix, N-prefix rejected | cmd/host.go:75-80, 138-144 | IMPLEMENTED | — | Uses `nkeys.IsValidPublicUserKey` which enforces U-prefix |
| spec-cli.md:97 | Error `"--nkey-public is required"` if omitted | cmd/host.go:139-141 | IMPLEMENTED | — | |
| spec-cli.md:97 | Error `"invalid NKey public key format"` if key fails format | cmd/host.go:143-144 | IMPLEMENTED | — | |
| spec-cli.md:99 | On success, writes `~/.mclaude/hosts/{hslug}/config.json` with `hubUrl` (from `auth.json.natsUrl`) | cmd/host.go:232-246 | IMPLEMENTED | — | `cfg.HubURL = creds.NATSUrl` |
| spec-cli.md:99 | No `nkey.seed`, no `nats.creds` written | cmd/host.go:232-246 | IMPLEMENTED | — | Only config.json written |
| spec-cli.md:99 | No `active-host` symlink written on register | cmd/host.go:127-252 | IMPLEMENTED | — | No symlink creation in RunHostRegister |
| spec-cli.md:109-111 | `mclaude host list` — calls `GET /api/users/{uslug}/hosts`, prints slug/name/online status table | cmd/host.go:277-318 | PARTIAL | SPEC→FIX | Code reads local `~/.mclaude/hosts/` directory instead of calling the HTTP API (TODO(stage7) comment). Output shows only slug and active marker — no name or online status. Spec says "prints a table of slug, name, and online status". The HTTP call is intentionally deferred; spec should note this is not yet wired. |
| spec-cli.md:113-115 | `mclaude host use <hslug>` — symlinks `~/.mclaude/active-host → ~/.mclaude/hosts/{hslug}/` | cmd/host.go:326-360 | IMPLEMENTED | — | |
| spec-cli.md:117-120 | `mclaude host rm <hslug>` — calls `DELETE /api/users/{uslug}/hosts/{hslug}`, removes local dir, removes active-host symlink if active | cmd/host.go:378-418 | PARTIAL | SPEC→FIX | HTTP `DELETE /api/users/{uslug}/hosts/{hslug}` is stubbed (TODO(stage7), `_ = uslug`). Local dir removal and symlink cleanup are fully implemented. Spec should note HTTP deletion is not yet wired. |
| spec-cli.md:121-143 | `mclaude auth anthropic` — PKCE OAuth, KV write, per-host delivery, --status, --revoke | cmd/anthropic.go:687-814 | IMPLEMENTED | — | All four sub-commands (default/--token/--status/--revoke) implemented |
| spec-cli.md:122 | `--token <v>` skips OAuth | cmd/anthropic.go:759-762 | IMPLEMENTED | — | |
| spec-cli.md:122 | `--status` prints connected/not-set + per-host trust status | cmd/anthropic.go:829-874 | IMPLEMENTED | — | |
| spec-cli.md:122 | `--revoke` deletes tokens.anthropic from KV | cmd/anthropic.go:816-826 | IMPLEMENTED | — | |
| spec-cli.md:133-135 | Step 1: PKCE OAuth opens browser, waits on loopback, exchanges code | cmd/anthropic.go:631-679 | IMPLEMENTED | — | |
| spec-cli.md:136 | Step 3: write token to `mclaude-user-info-{uslug}` at key `tokens.anthropic` via NATS KV PUT | cmd/anthropic.go:231-241 | IMPLEMENTED | — | bucket `mclaude-user-info-{uslug}`, key `tokens.anthropic` |
| spec-cli.md:137 | Step 4: for each accepted host, read X25519 pubkey, seal, publish to `mclaude.users.{uslug}.hosts.{hslug}.agent.token-delivery` | cmd/anthropic.go:399-431, 876-921 | IMPLEMENTED | — | |
| spec-cli.md:139 | `--revoke` only deletes KV key; host-side cleanup deferred | cmd/anthropic.go:816-826 | IMPLEMENTED | — | |
| spec-cli.md:141 | Exit codes: 0 success, 1 partial delivery, 2 no token, 3 KV failed | cmd/anthropic.go:119-128 | IMPLEMENTED | — | |
| spec-cli.md:145-147 | Context file `~/.mclaude/context.json`: stores `userSlug`, `projectSlug`, `hostSlug`, `server` | context/context.go:38-55 | IMPLEMENTED | — | |
| spec-cli.md:147 | Default server URL `https://api.mclaude.internal` | context/context.go:59 | IMPLEMENTED | — | |
| spec-cli.md:147 | Overridable via `MCLAUDE_CONTEXT_FILE` environment variable | context/context.go:25-28 | IMPLEMENTED | — | |
| spec-cli.md:147 | If file does not exist, all fields default to empty | context/context.go:100-103 | IMPLEMENTED | — | Returns empty Context (not error) on ENOENT |
| spec-cli.md:150-151 | Wire protocol: JSONL on unix socket, native stream-json format | repl/repl.go, events/parse.go | IMPLEMENTED | — | |
| spec-cli.md:185-186 | Outbound: `message`, `permission_response` typed commands | repl/repl.go:161-216 | IMPLEMENTED | — | `message` (sendUserMessage) and `control_response` (sendControlResponse) both present |
| spec-cli.md:185 | `skill_invoke` outbound command | repl/repl.go | GAP | SPEC→FIX | Spec-cli.md:185 lists `skill_invoke` as an outbound `SessionInput` type the CLI must support. No CLI code emits a skill_invoke. However, the spec says the accumulator "handles the native stream-json types directly" — this is an inbound description. The outbound skill_invoke is mentioned in the wire protocol spec but no CLI user-facing command or REPL trigger maps to it. Spec should clarify if skill_invoke emission is in v1 scope or deferred. |
| spec-cli.md:188-195 | Accumulator: text_delta, message, tool_call/tool_result, permission rendered; state_change/turn_complete/init tracked; thinking_delta/backend_specific discarded | events/accumulator.go | IMPLEMENTED | — | All specified event handling present |
| spec-cli.md:195 | On `clear` event: resets all turns, no divider | events/accumulator.go:127-130 | IMPLEMENTED | — | |
| spec-cli.md:196 | On `compact_boundary`: resets turns, renders `--- context compacted ---` divider | events/accumulator.go:122-124, renderer/renderer.go:64-65 | IMPLEMENTED | — | |
| spec-cli.md:199 | No reconnection; CLI exits if socket drops | repl/repl.go:86-92 | IMPLEMENTED | — | Scanner exits on error; no retry loop |
| spec-cli.md:201-203 | NATS connection uses JWT + NKey seed from auth.json; NATS URL from natsUrl when non-empty, else derived from server URL | cmd/import.go:547-562 | IMPLEMENTED | — | |
| spec-cli.md:252 | `TestIntegration_Login_DeviceCode` — httptest mock, auth.json written with valid U-key seed | cmd/integration_login_test.go:29-158 | IMPLEMENTED | — | Test fully implements the spec-cited test case |
| adr-0097:98-101 | Remove `cluster register`, `cluster grant` subcommands; remove `daemon` subcommand | main.go:60-78 | IMPLEMENTED | — | No `cluster`, `daemon` case in the switch |
| adr-0097:101-108 | `host register`: `--nkey-public` required, `--default-platform` optional, `--name` required, publishes to `mclaude.users.{uslug}.hosts._.register` | cmd/host.go:87-252 | IMPLEMENTED | — | |
| adr-0097:104 | CLI validates NKey format (56-char base32 U-prefix; N-prefix rejected) | cmd/host.go:75-80 | IMPLEMENTED | — | `nkeys.IsValidPublicUserKey` |
| adr-0097:108 | On success, writes `~/.mclaude/hosts/{hslug}/config.json` with `hubUrl` from `auth.json.natsUrl`. No `nkey.seed`, no `nats.creds`, no `active-host` symlink | cmd/host.go:232-246 | IMPLEMENTED | — | |
| adr-0097:135 | Error `"invalid NKey public key format"` | cmd/host.go:77 | IMPLEMENTED | — | |
| adr-0097:136 | Error `"--nkey-public is required"` | cmd/host.go:140 | IMPLEMENTED | — | |
| spec-state-schema.md:829-835 | Host config dir `~/.mclaude/hosts/{hslug}/`, file `config.json` with `{slug, hubUrl}` | cmd/host.go:56-60 | IMPLEMENTED | — | `HostConfig` struct has `Slug` and `HubURL` (json:"hubUrl") |
| spec-state-schema.md:839-843 | `~/.mclaude/auth.json` (mode 0600), contents: `{ jwt, nkeySeed, userSlug, natsUrl }` | cmd/auth.go:22-27 | IMPLEMENTED | — | All four fields present |

---

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| cmd/import.go:41 | INFRA | `natsRequestTimeout` constant — necessary timeout for NATS request/reply |
| cmd/import.go:80-90 | INFRA | `ImportMetadata` struct — required by ADR-0053 archive format spec, used in step 7 |
| cmd/import.go:463-468 | INFRA | `generateID()` — generates message IDs for NATS envelopes (needed by payload schema) |
| cmd/import.go:671-692 | INFRA | `slugifyProjectName` — converts directory names to valid slugs; necessary for step 5 |
| cmd/import.go:694-706 | INFRA | `promptProjectName` — reads user input for slug collision resolution in step 6 |
| cmd/host.go:22-39 | INFRA | `mclaudeDir()`, `hostDir()`, `activeHostPath()` — filesystem path helpers for host management |
| cmd/host.go:87-101 | INFRA | `HostRegisterFlags`, `HostRegisterResult` — flag struct required for RunHostRegister |
| cmd/host.go:109-125 | INFRA | `hostRegisterRequest`, `hostRegisterResponse` — NATS payload structs for host registration |
| cmd/anthropic.go:47-72 | INFRA | `AnthropicOAuthConfig` — injectable OAuth config for PKCE flow |
| cmd/anthropic.go:131-157 | INFRA | `KVEntry`, `KVBucket`, `KVAccessor` — narrow KV interfaces abstracting NATS JetStream KV for testability |
| cmd/anthropic.go:159-228 | INFRA | `realJSKVAccessor`, `realKVBucket`, `natsConnWrapper`, `connectNATSForAnthropic` — production NATS KV wiring |
| cmd/anthropic.go:279-355 | INFRA | `grantsResponse`, `fetchAcceptedGrants`, `pubkeyResponse`, `fetchUserAgentPubkey` — HTTP helpers for token delivery step 4 |
| cmd/anthropic.go:363-430 | INFRA | `SealToken`, `DeliverTokenToHost` — X25519 sealing for token delivery |
| cmd/anthropic.go:437-574 | INFRA | PKCE flow helpers (`PKCEParams`, `GeneratePKCE`, etc.) — implement spec-cli.md step 1 PKCE OAuth |
| cmd/anthropic.go:927-955 | INFRA | `injectedNATSConnWrapper`, `AnthropicTestNATSConn` — test injection interface |
| context/context.go:130-202 | INFRA | Slug validation and parse helpers (`ValidateUserSlug` etc.) — called by `session list` slug resolution |
| events/parse.go:43-58 | INFRA | `ToolResultBlocks()` — used by repl.go to detect inbound tool result events |
| repl/repl.go:24-27 | INFRA | `isConnClosedErr` — guard for expected socket-close sentinel during REPL teardown |
| testutil/server.go | INFRA | `MockServer` for CLI unit tests — test support code, not production |
| main.go:241-258 | INFRA | `runAuth` / `runAuthAnthropic` dispatch — CLI routing |
| main.go:295-341 | INFRA | `runSession` / `runSessionList` dispatch — CLI routing |
| main.go:347-438 | INFRA | `runHost` / `runHostRegister` / `runHostList` / `runHostUse` / `runHostRm` dispatch — CLI routing |
| cmd/import.go:146-149 | UNSPEC'd | `EncodeCWD` strips leading `-` (correct behavior) but spec-cli.md:68 says it is preserved. Code is right; already flagged as PARTIAL spec-cli.md:68. |
| cmd/integration_main_test.go:396-414 | UNSPEC'd | `grantClusterAccess` calls `POST /admin/clusters/{hslug}/grants` — this endpoint is removed by ADR-0097. The integration test setup still uses the deleted admin cluster grants endpoint to bootstrap test user access. Post-ADR-0097 this call will return 404 and integration tests will fail. Needs to be rewritten to use whatever host-access grant mechanism survives the removal (e.g. admin API for host grants, or NATS `manage.grant`). |

---

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| spec-cli.md:97 / adr-0097:104 | NKey format validation (U-prefix, N-prefix rejected, 56-char) | cmd/host_test.go:31-88 (TestValidateNKeyPublic_*) | None | UNIT_ONLY | Not a critical-path auth flow; pure format check |
| spec-cli.md:97 / adr-0097:136 | `"--nkey-public is required"` error | cmd/host_test.go:92-103 | None | UNIT_ONLY | Pure flag validation |
| spec-cli.md:97 / adr-0097:135 | `"invalid NKey public key format"` error | cmd/host_test.go:106-118 | None | UNIT_ONLY | Pure flag validation |
| spec-cli.md:99 / adr-0097:108 | config.json written with hubUrl from auth.json.natsUrl | cmd/host_test.go:253-305 (TestRunHostRegister_WritesConfigJSON) | None | UNIT_ONLY | |
| spec-cli.md:95-107 / adr-0097:101-108 | host register NATS publish to correct subject with correct payload | cmd/host_test.go:161-215 (TestRunHostRegister_PublishesToNATS — mock NATS) | None | UNIT_ONLY | ADR-0097 §Integration Test Cases row "Register host via NATS with nkey_public" has no corresponding integration test. CODE→FIX |
| adr-0097:178 | Register host with default-platform (admin): is_default_platform=true in DB | cmd/host_test.go:217-251 (mock NATS only) | None | UNIT_ONLY | ADR-0097 integration test case missing. CODE→FIX |
| adr-0097:179 | Register host with default-platform (non-admin): returns admin_required | cmd/host_test.go:307-335 (mock response) | None | UNIT_ONLY | ADR-0097 integration test case missing. CODE→FIX |
| spec-cli.md:39-50 | mclaude login device-code flow | cmd/login_test.go:21-137 (unit) | cmd/integration_login_test.go:29-158 (httptest mock) | TESTED | Integration test uses httptest.Server (HTTP-only per spec); matches spec-cli.md:252 |
| spec-cli.md:50 | auth.json written with mode 0600 | cmd/login_test.go:195-236 | None | UNIT_ONLY | Filesystem mode — not infrastructure-dependent |
| spec-cli.md:50 | natsUrl stored in auth.json | cmd/login_test.go:313-359 | integration_main_test.go (reads back auth.json with natsUrl) | TESTED | |
| spec-cli.md:63-86 | mclaude import full flow | cmd/import_test.go (unit, mock NATS) | cmd/integration_import_test.go:97-215 (real stack) | TESTED | |
| spec-cli.md:81 | Slug collision prompt + loop | cmd/import_test.go (unit) | integration_import_test.go:220-277 (real stack) | TESTED | |
| spec-cli.md:109-111 | host list (local listing stub) | None found | None | UNTESTED | `RunHostList` has no dedicated unit test |
| spec-cli.md:113-115 | host use symlink | None found | None | UNTESTED | `RunHostUse` has no dedicated unit test |
| spec-cli.md:117-120 | host rm | None found | None | UNTESTED | `RunHostRm` has no dedicated unit test |
| spec-cli.md:121-143 | mclaude auth anthropic (PKCE, KV write, per-host delivery, --status, --revoke) | cmd/anthropic_test.go | None against real stack | UNIT_ONLY | KV write + sealed-box token delivery is a critical data-flow path. Integration test against real NATS KV + real user-agent pubkey endpoint is missing. CODE→FIX |
| spec-cli.md:188-195 | Accumulator event handling | events/accumulator_test.go | None (pure in-memory) | UNIT_ONLY | Acceptable — accumulator is pure logic with no external deps |
| spec-cli.md:195-196 | clear and compact_boundary | events/accumulator_test.go | None | UNIT_ONLY | Acceptable |

---

### Phase 4 — Bug Triage

No bugs in `.agent/bugs/` have `**Component**: mclaude-cli`. All open bugs reference other components (SPA/session-agent). No triage actions required.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | — | — | No mclaude-cli bugs open |

---

### Summary

- Implemented: 47
- Gap: 1
- Partial: 4
- Infra: 21
- Unspec'd: 2
- Dead: 0
- Tested: 6
- Unit only: 12
- E2E only: 0
- Untested: 3
- Bugs fixed: 0
- Bugs open: 0
