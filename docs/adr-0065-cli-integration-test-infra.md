# ADR: CLI Integration Test Infrastructure

**Status**: implemented
**Status history**:
- 2026-05-02: draft
- 2026-05-02: accepted — paired with docs/mclaude-cli/spec-cli.md
- 2026-05-02: implemented — all scope CLEAN

## Overview

Smoke tests for the `mclaude` CLI that run against any real deployed stack (k3d preview or production). Analogous to the web Playwright suite: they prove user behaviors work end-to-end after a deployment and catch regressions. Tests exercise the full `mclaude import` and `mclaude login` flows against a live control-plane, real NATS, and real S3/MinIO. No per-component Docker Compose — the tests consume shared infrastructure, same as the web e2e suite.

## Motivation

The existing CLI test suite is unit-only. There is no smoke test that can be run against a fresh deployment to verify that `mclaude login` and `mclaude import` work. Without a deployment smoke test, a regression in NATS permissions, S3 pre-signed URL signing, or CP import handlers can go undetected until a user files a bug. The web Playwright suite already fills this role for browser-based user behavior; this ADR extends the same discipline to the CLI — every deployment should be verifiable by running the CLI smoke tests against it.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Infrastructure | Real deployed stack (k3d / CI cluster) | Catches actual protocol errors, subject mismatches, and CP behavior — not mock divergence. Same model as web e2e (ADR-0064). |
| No per-CLI Docker Compose | CLI testutil has no docker-compose.yml | S3, NATS, CP are platform infrastructure. CLI tests consume them via env vars, same as Playwright. |
| Build tag | `//go:build integration` | `go test -tags integration ./...` matches CP convention. |
| Test user creation | Admin API (`ADMIN_URL`) — same env var as web e2e | CLI TestMain calls `POST /admin/users` to create an ephemeral test user; teardown deletes it. Skips if `ADMIN_URL` unset. `POST /admin/users` returns `{id, email, name, slug}`. TestMain supplies its own generated password in the request body and retains it locally — the password is not returned in the response. |
| NATS credentials | TestMain concurrently runs `RunLogin` + completes device-code via form POST | TestMain goroutine 1: `RunLogin(flags)` — POSTs `/api/auth/device-code` with `{publicKey}` (CLI-generated NKey public key), gets `{deviceCode, userCode}`, polls. TestMain goroutine 2: submits form `POST /api/auth/device-code/verify` with fields `user_code=<userCode>`, `email=<testEmail>`, `password=<testToken>` — authenticates inline from form fields, no session cookie. `RunLogin`'s poll detects `{"status":"authorized"}` and writes `{jwt, nkeySeed, userSlug}` to `cli-integration/.test-creds.json`. The admin API does not issue NATS credentials; `RunLogin` is the only credential-acquisition path. |
| Test host | Fixed host slug via `MCLAUDE_TEST_HOST_SLUG` env var (default: `dev-local`) | The host must be pre-registered in the k3d cluster. Tests run full end-to-end: wait for the session-agent to unpack the archive and sessions to appear. No throwaway host registration. |
| Full e2e scope | Test polls `GET /api/users/{uslug}/projects/{pslug}` until `importRef` is null | After `RunImport` returns, test polls the project HTTP endpoint (`Authorization: Bearer <jwt>` from `cli-integration/.test-creds.json`) every 2s until `importRef` is null — the session-agent clears this field after successful unpack. Then asserts sessions visible: connect to NATS with test-user JWT+seed, call `kv.Watch("hosts.{hslug}.projects.{pslug}.sessions.>")` on `mclaude-sessions-{uslug}` (this subject matches the allowed `$KV.mclaude-sessions-{uslug}.hosts.>` in the user JWT), drain `watcher.Updates()` until it sends nil (initial values exhausted), assert at least 1 entry received, call `watcher.Stop()`. Timeout: 60s total. |
| S3 cleanup | Project deletion via admin API deletes S3 prefix | CP deletes `{uslug}/{hslug}/{pslug}/` on project delete. TestMain teardown deletes the project. |
| Login poll protocol | CP returns `{"status":"pending"\|"authorized", jwt, userSlug}` (200); CLI must parse `status` field | The CP always returns HTTP 200. CLI's current `pending bool` field parsing is wrong — it must check `status == "authorized"` to detect completion. **This is a production bug (ADR-0065 backpressure):** `mclaude login` will not complete against a real CP until this is fixed. Fix tracked separately via `/feature-change`. |
| Credential propagation | `ADMIN_URL`, `ADMIN_TOKEN`, `MCLAUDE_TEST_HOST_SLUG` env vars | Consistent invocation pattern with web e2e (ADR-0064). |

## Component Changes

### mclaude-control-plane

Two pre-existing implementation gaps must be closed before the CLI smoke tests can pass:

- `project_http.go:ProjectResponse` — add `ImportRef *string \`json:"importRef,omitempty"\`` field; update the SQL query in `handleGetProjectHTTP` to include `import_ref` in the SELECT. This field is cleared by the session-agent after successful unpack; the test polls for `importRef == null`.
- `device_auth.go:CLIDeviceCodeRequest` — add `PublicKey string \`json:"publicKey"\`` field and decode it in `handleCLIDeviceCodeCreate`; store the public key in `cliDeviceCodeEntry.PublicKey` (add `PublicKey string` to the struct — no DB migration, device codes are in-memory). In `handleCLIDeviceCodeVerify`, if the entry has a non-empty `PublicKey`, issue the JWT using that key (`IssueUserJWT(entry.PublicKey, ...)`). If the entry has an empty `PublicKey` (user visited the verify URL manually via browser), fall through to `user.NKeyPublic`; if that is also empty, return HTTP 400. Without this fix, the JWT's subject NKey will not match the CLI's locally-generated seed, causing NATS authentication failure.
- `admin.go:AdminUserResponse` — add `Slug string \`json:"slug"\`` field. TestMain needs the slug to construct `GET /api/users/{uslug}/projects/{pslug}` API paths and the device-code verify form POST. The password does not need to be returned (TestMain already has the password it generated before calling `POST /admin/users`).

### mclaude-cli

- New `cmd/integration_main_test.go` (`//go:build integration`): `TestMain` — creates test user via admin API (`POST /admin/users`), then runs `RunLogin` device-code flow to acquire NATS JWT+seed; stores in `cli-integration/.test-creds.json`; defers teardown (project + user deletion via admin API)
- New `cmd/integration_import_test.go` (`//go:build integration`): `TestIntegration_Import_HappyPath`, `TestIntegration_Import_SlugCollision`
- New `cmd/integration_login_test.go` (`//go:build integration`): `TestIntegration_Login_DeviceCode`
- `cmd/import.go`: add `Input io.Reader` to `ImportFlags` (falls back to `os.Stdin`) — needed for slug-collision prompt injection in tests

## Test Cases

### Import happy path

1. TestMain has created a test user with NATS JWT + NKey seed and knows a valid `hslug`
2. Seed a temp `~/.claude/projects/{encoded-cwd}/` with 2 JSONL session files + memory dir
3. Call `cmd.RunImport(flags)` pointing at the real NATS URL + real CP
4. Assert: `RunImport` returns nil
5. Poll `GET /api/users/{uslug}/projects/{pslug}` with `Authorization: Bearer <jwt>` every 2s until `importRef` is null or 60s elapses; fail if timeout
6. Connect to NATS with the test user's JWT + NKey seed; call `js.KeyValue("mclaude-sessions-"+uslug)` to open the bucket; call `kv.Watch("hosts."+hslug+".projects."+pslug+".sessions.>")` (this filtered subject is within the user JWT's allowed `$KV.mclaude-sessions-{uslug}.hosts.>` sub permissions); drain watcher entries until `watcher.Updates()` sends nil (initial values exhausted); assert at least 1 entry received
7. Teardown: delete project via admin API (which also cleans S3 prefix)

### Slug collision → prompt rename

1. `RunImport` once to create a project with slug `my-project`
2. `RunImport` again against the same CWD — check-slug returns `available: false`
3. Inject new name `"my-project-2\n"` via `ImportFlags.Input`
4. Assert: `result.ProjectSlug == "my-project-2"` (RunImport returns the slug it used); confirm by calling `GET /api/users/{uslug}/projects/my-project-2` and verifying HTTP 200

### Login device-code

1. Start an `httptest.Server` mocking `/api/auth/device-code` and `/api/auth/device-code/poll` (login uses HTTP, not NATS — httptest mock is still appropriate here)
2. Background goroutine delivers credentials after 200ms
3. Call `cmd.RunLogin(flags)` with temp auth.json
4. Assert: auth.json contains `{jwt, nkeySeed, userSlug}` with a valid U-key seed

## Error Handling

| Error | Test behavior |
|-------|--------------|
| NATS connection refused | `RunImport` returns error; test asserts error message |
| S3 upload fails (CP returns bad pre-signed URL) | `RunImport` returns upload error |
| `import.confirm` NATS timeout | `RunImport` returns error after timeout |
| Device-code poll timeout | `RunLogin` returns error; no auth.json written |

## Security

Tests use ephemeral credentials created per-run. `cli-integration/.test-creds.json` is gitignored. All test projects deleted in teardown.

## Developer invocation

```bash
# Forward admin port first (separate terminal):
kubectl port-forward -n mclaude-system svc/mclaude-cp-control-plane 9091:9091 &

# Run CLI integration tests:
ADMIN_URL=http://localhost:9091 \
ADMIN_TOKEN=dev-admin-token \
MCLAUDE_TEST_HOST_SLUG=dev-local \
MCLAUDE_TEST_SERVER_URL=https://api.mclaude.internal \
go test -tags integration ./cmd/...
```

The NATS URL is derived from `MCLAUDE_TEST_SERVER_URL` via `DeriveNATSURL` — no separate NATS URL env var needed.

## Impact

Specs updated:
- `docs/mclaude-cli/spec-cli.md` — Integration Tests section (update to real-infra approach)

## Scope

**v1:**
- `TestIntegration_Import_HappyPath`
- `TestIntegration_Import_SlugCollision`
- `TestIntegration_Login_DeviceCode`
- `TestMain` with admin-API user creation + teardown
- `ImportFlags.Input io.Reader`

**Deferred:**
- `TestIntegration_Import_S3Upload_Failure` (requires CP cooperation to return a bad URL — awkward with real CP)
- Integration tests for `host register` / `host list` / `cluster register`
- CI job wiring (separate ADR)
- Attachment upload integration tests

## Open questions

(none remaining)

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| mclaude-control-plane/project_http.go | ~10 | Add `importRef` field to `ProjectResponse`; add `import_ref` to `handleGetProjectHTTP` SQL query |
| mclaude-control-plane/device_auth.go | ~20 | Add `PublicKey` to `CLIDeviceCodeRequest` and `cliDeviceCodeEntry`; use in verify JWT issuance with fallback to `user.NKeyPublic`; no DB migration (in-memory store) |
| mclaude-control-plane/admin.go | ~5 | Add `Slug` field to `AdminUserResponse`; populate from the created user's slug |
| cmd/integration_main_test.go | ~120 | TestMain: admin user + concurrent RunLogin + HTTP device-code completion + teardown (delete project + user) |
| cmd/integration_import_test.go | ~180 | 2 test cases: happy path (poll until sessions visible, 60s timeout) + slug collision |
| cmd/integration_login_test.go | ~70 | 1 test: RunLogin with HTTP mock (login uses no NATS; httptest still appropriate) |
| cmd/import.go | ~5 | Add `Stdin io.Reader` to `ImportFlags` |
| docs/mclaude-cli/spec-cli.md | ~40 | Replace Docker Compose section with real-infra smoke test description |

**Total estimated tokens:** ~120k (CP pre-requisite fixes + CLI test infra)
