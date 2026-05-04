## Run: 2026-05-03T00:00:00Z

### ADR: docs/adr-0069-cli-integration-nats-url-override.md
### Specs checked: docs/mclaude-cli/spec-cli.md, docs/mclaude-control-plane/spec-control-plane.md

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 21 | `CLIDeviceCodePollResponse.NATSUrl` — set from `s.natsWsURL` when authorized | spec-control-plane.md:57 — `POST /api/auth/device-code/poll` returns `{status: "authorized", jwt, userSlug, natsUrl}` where `natsUrl` is from `s.natsWsURL`; omitted when empty | REFLECTED | — | CP spec fully captures the field, source, and omit-when-empty semantics |
| 22 | `AuthCredentials.NATSUrl string json:"natsUrl,omitempty"` in `auth.json`; omitted when empty | spec-cli.md:50 — `auth.json` format `{ "jwt": "...", "nkeySeed": "...", "userSlug": "...", "natsUrl": "..." }`; "The `natsUrl` field is omitted when the poll response returned an empty value." | REFLECTED | — | Spec captures field name, json key, omitempty semantics |
| 23 | `RunImport` uses `creds.NATSUrl` if non-empty, else `DeriveNATSURL(serverURL)` | spec-cli.md:205 — "The NATS server WebSocket URL is read from `natsUrl` in `~/.mclaude/auth.json` when non-empty; otherwise derived from the control-plane server URL by replacing the scheme (`https` → `wss`) and appending `/nats`." | REFLECTED | — | Spec captures the fallback logic. Note: ADR says fallback is `DeriveNATSURL(serverURL)` which appends `/nats`; spec describes the same derivation differently (scheme swap + append `/nats`) — consistent |
| 24 | No `MCLAUDE_TEST_NATS_URL` env var; integration tests work via `RunLogin` → `auth.json` → `RunImport` | spec-cli.md:227 — "The CP returns `natsUrl` in its device-code poll response (`s.natsWsURL`). The CLI stores it in `auth.json` and uses it for NATS connections — no separate NATS URL env var is needed." | REFLECTED | — | Spec explicitly states no separate env var needed |
| 25 | `integration_import_test.go` reads `natsUrl` from `auth.json` (via `creds.NATSUrl`) for direct NATS connections | spec-cli.md:250 — `TestIntegration_Import_HappyPath`: "connect to NATS using `natsUrl` from `.test-creds.json` (populated by `RunLogin` from the poll response)" | REFLECTED | — | Spec describes test using stored natsUrl from .test-creds.json |
| 31-35 | `device_auth.go` — `CLIDeviceCodePollResponse` gains `NATSUrl string json:"natsUrl,omitempty"`; populated from `s.natsWsURL` when status is `"authorized"` | spec-control-plane.md:57 — poll endpoint description includes `natsUrl` in authorized response | REFLECTED | — | |
| 39-43 | `cmd/login.go` — `deviceCodePollResponse` gains `NATSUrl`; captured on `"authorized"` and included in `AuthCredentials` | spec-cli.md:48-50 — step 5 describes poll response as `{ "status": "pending" \| "authorized", "jwt": "...", "userSlug": "...", "natsUrl": "..." }`; step 7 describes writing it to auth.json | REFLECTED | — | |
| 44-48 | `cmd/auth.go` (or wherever `AuthCredentials` is defined) — `NATSUrl string json:"natsUrl,omitempty"` | spec-cli.md:50 — auth.json format includes `natsUrl` field | REFLECTED | — | |
| 62-71 | `~/.mclaude/auth.json` gains optional `natsUrl` field; omitted when CP returns empty `natsWsUrl` | spec-cli.md:50 — full auth.json format with `natsUrl`; "omitted when the poll response returned an empty value" | REFLECTED | — | |
| 75 | If stored `natsUrl` is stale, `RunImport` returns a NATS connection error; user can re-run `mclaude login` to refresh | spec-cli.md does not document stale natsUrl behavior | GAP | SPEC→FIX | ADR documents a user-facing recovery path (re-run `mclaude login`); spec is silent on what happens if natsUrl becomes stale. Not mentioned in error cases table or import flow |
| 240 | Test step 3 — `.test-creds.json` written with `{jwt, nkeySeed, userSlug, natsUrl}` | spec-cli.md:240 — "writes NATS credentials to `cli-integration/.test-creds.json` (gitignored): `{jwt, nkeySeed, userSlug, natsUrl}` — `natsUrl` is whatever the CP returned in the poll response" | REFLECTED | — | |
| 252 | `TestIntegration_Login_DeviceCode` — mock returns `{"status":"authorized","jwt":"...","userSlug":"...","natsUrl":"wss://mock-nats.example.com"}`; `auth.json` written with `{jwt, nkeySeed, userSlug, natsUrl}` | spec-cli.md:252 — test case described verbatim including mock natsUrl value `wss://mock-nats.example.com` and expected auth.json fields | REFLECTED | — | |

### Summary

- Reflected: 11
- Gap: 1
- Partial: 0
