# Implementation Audit: mclaude-cli — ADR-0069 (Round 2)

**Component**: mclaude-cli
**Component root**: mclaude-cli/
**Spec files**: docs/adr-0069-cli-integration-nats-url-override.md, docs/mclaude-cli/spec-cli.md
**Date**: 2026-05-03
**Focus**: Re-check all 13 spec requirements after fix pass; primary focus is `cmd/integration_login_test.go`

## Run: 2026-05-03T00:00:00Z

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0069:33 | `CLIDeviceCodePollResponse.NATSUrl string \`json:"natsUrl,omitempty"\`` | login.go:71–81 (`deviceCodePollResponse.NATSUrl`) | IMPLEMENTED | — | Field present with correct json tag |
| adr-0069:36 | "In `handleCLIDeviceCodePoll`, when status is `"authorized"`, populate `NATSUrl: s.natsWsURL`" | mclaude-control-plane scope — out of this component | IMPLEMENTED | — | CLI side: login.go:159 captures `pollResp.NATSUrl` on authorized |
| adr-0069:42 | "`deviceCodePollResponse` gains `NATSUrl string \`json:"natsUrl,omitempty"\``" | login.go:78 | IMPLEMENTED | — | Exact field + tag match |
| adr-0069:44 | "when `pollResp.Status == "authorized"`, capture `pollResp.NATSUrl` and include it when building `AuthCredentials`" | login.go:156–178 | IMPLEMENTED | — | `natsURL = pollResp.NATSUrl` at line 159; passed to `AuthCredentials.NATSUrl` at line 177 |
| adr-0069:47 | "`AuthCredentials` gains `NATSUrl string \`json:"natsUrl,omitempty"\``" | auth.go:26–27 | IMPLEMENTED | — | Exact field + omitempty tag |
| adr-0069:52–56 | `RunImport` uses `creds.NATSUrl` if non-empty, else `DeriveNATSURL(serverURL)` | import.go:548–551 | IMPLEMENTED | — | `natsURL := creds.NATSUrl; if natsURL == "" { natsURL = clicontext.DeriveNATSURL(serverURL) }` |
| adr-0069:59 | `integration_import_test.go` line 159: replace `DeriveNATSURL(serverURL)` with `creds.NATSUrl` (loaded from test `auth.json` written by `TestMain`/`RunLogin`). If empty, fall back to `DeriveNATSURL`. | integration_main_test.go:261–264 (`intNATSURL = creds.NATSUrl; if intNATSURL == "" { ... DeriveNATSURL ... }`) | IMPLEMENTED | — | TestMain reads `creds.NATSUrl` from `auth.json` and uses it for `intNATSURL`; import test uses `intNATSURL` |
| spec-cli:48 | Poll response body: `{ "status": "pending" \| "authorized", "jwt": "...", "userSlug": "...", "natsUrl": "..." }` | login.go:71–81 | IMPLEMENTED | — | Struct decodes all four fields |
| spec-cli:50 | "CLI writes credentials to `~/.mclaude/auth.json` (mode `0600`) … `{ "jwt": ..., "nkeySeed": ..., "userSlug": ..., "natsUrl": ... }`. The `natsUrl` field is omitted when the poll response returned an empty value." | auth.go:22–27, auth.go:74 (0600 mode), login.go:173–178 | IMPLEMENTED | — | `omitempty` ensures field absent when empty; SaveAuth uses 0600 |
| spec-cli:205 | "The NATS server WebSocket URL is read from `natsUrl` in `~/.mclaude/auth.json` when non-empty; otherwise derived from the control-plane server URL" | import.go:548–551 | IMPLEMENTED | — | |
| spec-cli:227 | "The CP returns `natsUrl` in its device-code poll response. The CLI stores it in `auth.json` and uses it for NATS connections — no separate NATS URL env var is needed." | login.go:159,177; import.go:548–551 | IMPLEMENTED | — | |
| spec-cli:240–243 | TestMain: `RunLogin`'s poll detects authorized and writes `{jwt, nkeySeed, userSlug, natsUrl}` to `cli-integration/.test-creds.json` — `natsUrl` is whatever the CP returned | integration_main_test.go:254–264 | IMPLEMENTED | — | `testCreds` struct includes `NATSUrl`; globals populated; fallback to DeriveNATSURL when empty |
| spec-cli:252 | `TestIntegration_Login_DeviceCode`: mock poll returns `{"status":"authorized","jwt":"...","userSlug":"...","natsUrl":"wss://mock-nats.example.com"}` on second call; `auth.json` written with valid `{jwt, nkeySeed, userSlug, natsUrl}`; NKey seed is a valid U-key | integration_login_test.go:81–86 (mock returns natsUrl), 132–134 (assert `creds.NATSUrl == mockNATSUrl`) | IMPLEMENTED | — | **This is the previously-reported gap — now fixed.** Mock returns `natsUrl`; test asserts `creds.NATSUrl` at lines 132–134 |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| login.go:1–12 (package comment, import block) | INFRA | Standard file header and imports for HTTP, JSON, NKey, context |
| login.go:27–45 (LoginFlags, LoginResult structs) | INFRA | Necessary flag/result types for the login command; implicitly covered by spec §mclaude login |
| login.go:88–197 (RunLogin) | INFRA | Full implementation of device-code flow described in spec; all branches covered in Phase 1 |
| login.go:199–242 (postDeviceCode, pollDeviceCode helpers) | INFRA | HTTP helper functions for the spec-defined device-code flow |
| auth.go:1–78 (full file) | INFRA | AuthCredentials struct, LoadAuth, SaveAuth — all described by spec §auth.json and ADR-0069 |
| import.go:1–39 (package comment, imports) | INFRA | Standard file header |
| import.go:41–136 (ImportFlags, NATSConn, ImportResult, ImportMetadata, request/response types) | INFRA | Types described by spec §mclaude import |
| import.go:138–193 (EncodeCWD, DiscoverSessions, listEncodedDirs) | INFRA | CWD encoding described spec §CWD encoding algorithm |
| import.go:195–315 (BuildArchive and helper archive functions) | INFRA | Archive building described spec §Flow step 7 |
| import.go:317–336 (gitInfo, claudeCodeVersion) | INFRA | Metadata helpers described spec §ImportMetadata |
| import.go:338–357 (UploadToS3) | INFRA | S3 upload described spec §Flow step 9 |
| import.go:359–382 (connectNATS) | INFRA | NATS connection described spec §NATS connection |
| import.go:384–461 (checkSlugAvailability, requestImportURL, confirmImport, generateID) | INFRA | NATS request/reply operations described spec §Flow steps 5,8,10 |
| import.go:463–706 (RunImport, slugifyProjectName, promptProjectName) | INFRA | Full import flow described spec §mclaude import |
| integration_login_test.go:1–159 | INFRA | Integration test described by spec §Test cases table (TestIntegration_Login_DeviceCode) |
| integration_import_test.go:1–327 | INFRA | Integration tests described by spec §Test cases table |
| integration_main_test.go:1–427 | INFRA | TestMain setup described by spec §Test user setup |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0069:42 / spec-cli:48 | `deviceCodePollResponse.NATSUrl` field | login_test.go:314–359 (`TestRunLogin_StoresNatsUrl`) | integration_login_test.go:29–159 (`TestIntegration_Login_DeviceCode`) | TESTED |
| adr-0069:47 / spec-cli:50 | `AuthCredentials.NATSUrl` stored in auth.json with omitempty | login_test.go:363–415 (`TestRunLogin_NatsUrlOmittedWhenEmpty`) | integration_login_test.go:132–134 | TESTED |
| adr-0069:52–56 / spec-cli:205 | RunImport uses creds.NATSUrl with DeriveNATSURL fallback | import_test.go (unit; noted as `TestRunImport_UsesStoredNatsUrl` per ADR-0069 §Integration Test Cases) | integration_import_test.go (HappyPath uses intNATSURL from auth.json) | TESTED |
| spec-cli:252 | `TestIntegration_Login_DeviceCode` mock poll returns natsUrl; auth.json asserted for natsUrl | login_test.go:314–359 (unit analog) | integration_login_test.go:29–159 | TESTED |

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` with Component matching `mclaude-cli`.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | No open CLI bugs found | — | |

### Summary

- Implemented: 13
- Gap: 0
- Partial: 0
- Infra: 16
- Unspec'd: 0
- Dead: 0
- Tested: 4
- Unit only: 0
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0
