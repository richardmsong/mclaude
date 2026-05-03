# ADR: CLI propagates natsWsUrl from device-code poll response

**Status**: implemented
**Status history**:
- 2026-05-03: draft
- 2026-05-03: accepted — paired with docs/mclaude-cli/spec-cli.md, docs/mclaude-control-plane/spec-control-plane.md
- 2026-05-03: implemented — all scope CLEAN

## Overview

The control-plane's web `LoginResponse` already returns `natsUrl` — the configured NATS WebSocket URL (e.g. `wss://dev-nats.mclaude.richardmcsong.com`) — with a comment that "empty string means the client should derive it from its own origin." The CLI device-code poll response (`CLIDeviceCodePollResponse`) does not include this field. As a result, the CLI always falls back to `DeriveNATSURL(serverURL)`, which appends `/nats` to the main domain — a URL that has no matching Ingress route in k3d (where NATS WebSocket is at a separate `dev-nats.*` subdomain). This ADR adds `natsUrl` to the device-code poll response and threads it through to `auth.json` and `RunImport`, making the CLI self-configuring on the same basis as the SPA.

## Motivation

Running `mclaude import` against the k3d preview cluster fails because NATS WebSocket is exposed at `wss://dev-nats.mclaude.richardmcsong.com` (a separate Ingress with `path: /`) while `DeriveNATSURL("https://dev.mclaude.richardmcsong.com")` produces `wss://dev.mclaude.richardmcsong.com/nats`. The Helm chart comment ("NATS WebSocket only accepts connections at / and has no configurable handshake path — a dedicated host avoids path-rewriting middleware") confirms the separate subdomain is by design. The CLI integration tests also fail for the same reason.

The CP already carries `natsWsURL` (configured via `NATS_WS_URL` env var) and returns it in the web login response. The CLI just needs to receive and store it.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Propagation path | `CLIDeviceCodePollResponse.NATSUrl` (set from `s.natsWsURL` when authorized) | Mirror the web `LoginResponse.NATSUrl` field; same server-side value, same semantic |
| Storage | `AuthCredentials.NATSUrl string \`json:"natsUrl,omitempty"\`` in `auth.json` | Persists across invocations without requiring a re-login; omitted when empty to avoid breaking existing auth.json files |
| Import fallback | `RunImport` uses `creds.NATSUrl` if non-empty, else `DeriveNATSURL(serverURL)` | Backward compatible with deployments where CP returns empty `natsUrl` (empty = "derive from origin") |
| No test-only env var | Remove `MCLAUDE_TEST_NATS_URL` from scope | Integration tests work via `RunLogin` → `auth.json` → `RunImport`, same as real users |
| Direct test connection | `integration_import_test.go` reads `natsUrl` from `auth.json` (via `creds.NATSUrl`) | Consistent with whatever URL `RunImport` uses |

## Component Changes

### mclaude-control-plane

**`device_auth.go`** — `CLIDeviceCodePollResponse` gains:
```go
NATSUrl string `json:"natsUrl,omitempty"`
```
In `handleCLIDeviceCodePoll`, when status is `"authorized"`, populate `NATSUrl: s.natsWsURL`.

### mclaude-cli

**`cmd/login.go`** — `deviceCodePollResponse` gains:
```go
NATSUrl string `json:"natsUrl,omitempty"`
```
In the poll loop, when `pollResp.Status == "authorized"`, capture `pollResp.NATSUrl` and include it when building `AuthCredentials`.

**`cmd/auth.go`** (or wherever `AuthCredentials` is defined) — gains:
```go
NATSUrl string `json:"natsUrl,omitempty"`
```

**`cmd/import.go`** — in `RunImport`, after loading `creds`:
```go
natsURL := creds.NATSUrl
if natsURL == "" {
    natsURL = clicontext.DeriveNATSURL(serverURL)
}
```

**`cmd/integration_import_test.go`** — line 159: replace `clicontext.DeriveNATSURL(serverURL)` with `creds.NATSUrl` (loaded from the test `auth.json` written by `TestMain`/`RunLogin`). If empty, fall back to `DeriveNATSURL`.

## Data Model

`~/.mclaude/auth.json` gains an optional field:
```json
{
  "jwt": "...",
  "nkeySeed": "...",
  "userSlug": "alice-gmail",
  "natsUrl": "wss://dev-nats.mclaude.richardmcsong.com"
}
```
Field is omitted when the CP returns an empty `natsWsUrl` (e.g. production deployments that serve NATS at `<origin>/nats`).

## Error Handling

If the stored `natsUrl` is reachable but stale (e.g. cluster was reconfigured), `RunImport` returns a NATS connection error. The user can re-run `mclaude login` to refresh `auth.json`. No special error handling beyond the existing NATS connection error message.

## Security

`natsUrl` is a WebSocket URL, not a credential. Storing it in `auth.json` alongside `jwt` and `nkeySeed` is appropriate — same sensitivity level. No new attack surface.

## Impact

Specs updated in this commit:
- `docs/mclaude-cli/spec-cli.md` — Auth/credentials section: document `natsUrl` in `auth.json`; Smoke Tests section: update developer invocation (no `MCLAUDE_TEST_NATS_URL` needed); update `integration_import_test.go` NATS connection note.
- `docs/mclaude-control-plane/spec-*.md` — `CLIDeviceCodePollResponse` gains `natsUrl`.

Components implementing the change:
- `mclaude-control-plane` (device_auth.go)
- `mclaude-cli` (login.go, auth.go/auth-related, import.go, integration_import_test.go)

## Scope

**v1:**
- `CLIDeviceCodePollResponse.NATSUrl` populated from `s.natsWsURL`
- `AuthCredentials.NATSUrl` stored/loaded from `auth.json`
- `RunImport` uses `creds.NATSUrl` with `DeriveNATSURL` fallback
- `integration_import_test.go` uses `creds.NATSUrl` for direct NATS connection

**Deferred:**
- `MCLAUDE_TEST_NATS_URL` escape-hatch env var (not needed with this fix)
- Surfacing NATS connection errors with "try re-running mclaude login" hint

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| `TestIntegration_Import_HappyPath` (existing) | After `RunLogin` writes `auth.json` with `natsUrl`, `RunImport` connects to NATS at the stored URL and upload completes; sessions visible in KV | TestMain creates user + runs RunLogin; teardown deletes project + user | CP device-code poll (natsUrl), CLI login, CLI import, NATS |
| CP unit: `TestCLIDeviceCodePoll_AuthorizedIncludesNatsUrl` | `CLIDeviceCodePollResponse` includes `natsUrl` when server `natsWsURL` is set | Unit test — no live stack | CP device_auth.go |
| CLI unit: `TestRunLogin_StoresNatsUrl` | `auth.json` includes `natsUrl` from poll response | Unit test with httptest mock | CLI login.go |
| CLI unit: `TestRunImport_UsesStoredNatsUrl` | `RunImport` uses `creds.NATSUrl` when non-empty, falls back to DeriveNATSURL when empty | Unit test with mock NATSConn | CLI import.go |

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| `mclaude-control-plane/device_auth.go` | ~5 | Add `NATSUrl` to `CLIDeviceCodePollResponse`; populate in handler |
| `mclaude-cli/cmd/login.go` | ~5 | Add `NATSUrl` to `deviceCodePollResponse`; pass through to `AuthCredentials` |
| `mclaude-cli/cmd/auth.go` (or equivalent) | ~3 | Add `NATSUrl` to `AuthCredentials` struct |
| `mclaude-cli/cmd/import.go` | ~5 | Use `creds.NATSUrl` with fallback |
| `mclaude-cli/cmd/integration_import_test.go` | ~5 | Use `creds.NATSUrl` instead of `DeriveNATSURL` |
| `docs/mclaude-cli/spec-cli.md` | ~15 | Document `natsUrl` in auth.json; update smoke test NATS note |
| `docs/mclaude-control-plane/spec-*.md` | ~5 | Add `natsUrl` to CLIDeviceCodePollResponse |

**Total estimated tokens:** ~60k
