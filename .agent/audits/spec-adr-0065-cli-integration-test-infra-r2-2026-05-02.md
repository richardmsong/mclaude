## Run: 2026-05-02T00:00:00Z (Round 2)

ADR: docs/adr-0065-cli-integration-test-infra.md
Spec: docs/mclaude-cli/spec-cli.md

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| 19–20 | Real deployed stack; no per-CLI docker-compose.yml | spec-cli.md:214 "CLI smoke tests prove user behaviors work end-to-end against a real deployed stack" | REFLECTED | — | |
| 21 | `//go:build integration`; `go test -tags integration ./...` | spec-cli.md:224 (code block shows `go test -tags integration ./cmd/...`) + line 252 | REFLECTED | — | |
| 22 | `POST /admin/users` returns `{id, email, name, slug}`; password not returned; TestMain supplies generated password | spec-cli.md:234 "The response returns `{id, email, name, slug}`. TestMain supplies its own generated password in the request body; the password is not returned in the response." | REFLECTED | — | Round 1 fix applied correctly |
| 22 | Skips if `ADMIN_URL` unset | spec-cli.md:229 "If unset, `TestMain` writes `{skipped:true}` to `cli-integration/.test-creds.json` and exits without error — tests are skipped, not failed." | REFLECTED | — | Spec adds correct detail about skipped:true sentinel |
| 23 | Goroutine 1: `RunLogin(flags)` POSTs `/api/auth/device-code` with `{publicKey}`, gets `{deviceCode, userCode}`, polls | spec-cli.md:236 "Start `RunLogin(flags)` in a goroutine — POSTs `/api/auth/device-code` with `{publicKey}`…" | REFLECTED | — | |
| 23 | Goroutine 2: form `POST /api/auth/device-code/verify` with `user_code`, `email`, `password`; authenticates inline; no session cookie | spec-cli.md:237 "form `POST /api/auth/device-code/verify` with fields `user_code=<userCode>`, `email=<testEmail>`, `password=<testToken>` — the endpoint authenticates inline from form fields; no separate login or session cookie needed" | REFLECTED | — | |
| 23 | `RunLogin` writes `{jwt, nkeySeed, userSlug}` to `cli-integration/.test-creds.json` | spec-cli.md:238 "`RunLogin`'s poll detects `{"status":"authorized"}` and writes NATS credentials to `cli-integration/.test-creds.json` (gitignored): `{jwt, nkeySeed, userSlug}`" | REFLECTED | — | |
| 23 | Admin API does not issue NATS credentials; `RunLogin` is the only credential-acquisition path | spec-cli.md:234–238 — spec describes credentials obtained only via device-code flow (RunLogin); admin API path not described as issuing creds | REFLECTED | — | Implied clearly enough; developer reading spec would not attempt admin API for NATS creds |
| 24 | Fixed host slug via `MCLAUDE_TEST_HOST_SLUG`; default `dev-local`; no throwaway host registration | spec-cli.md:230–231 "`MCLAUDE_TEST_HOST_SLUG` must refer to a host that is already registered… `TestMain` does **not** register a throwaway host… Default: `dev-local`." | REFLECTED | — | |
| 25 | Poll `GET /api/users/{uslug}/projects/{pslug}` every 2s until `importRef` is null; `Authorization: Bearer <jwt>` from `.test-creds.json`; timeout 60s; then `kv.Watch`, drain until nil, assert ≥1 entry, `watcher.Stop()` | spec-cli.md:248 (TestIntegration_Import_HappyPath row) | REFLECTED | — | Round 1 fix applied; watcher.Stop() present |
| 26 | CP deletes `{uslug}/{hslug}/{pslug}/` on project delete; TestMain teardown deletes project | spec-cli.md:242 "delete test project via admin API (CP deletes S3 prefix `{uslug}/{hslug}/{pslug}/`), then delete the test user." | REFLECTED | — | Round 1 fix applied |
| 27 | CP returns HTTP 200 always; response body `{"status":"pending"\|"authorized", jwt, userSlug}`; `jwt`/`userSlug` omitted when pending; CLI checks `status == "authorized"` | spec-cli.md:48 "The control-plane always responds HTTP 200. The response body is `{ "status": "pending" \| "authorized", "jwt": "...", "userSlug": "..." }` — `jwt` and `userSlug` are omitted when status is `"pending"`." | REFLECTED | — | |
| 27 | Production bug: CLI's `pending bool` parsing is wrong (backpressure, fix tracked separately) | spec-cli.md:48 describes correct behavior (`status == "authorized"`); bug is a deferred fix note, not a spec decision | REFLECTED | — | Spec describes correct protocol; bug note is not a spec item |
| 28 | Env vars: `ADMIN_URL`, `ADMIN_TOKEN`, `MCLAUDE_TEST_HOST_SLUG` | spec-cli.md:220–224 (code block shows all three); line 229 describes ADMIN_URL behavior | REFLECTED | — | |
| 100 | NATS URL derived from `MCLAUDE_TEST_SERVER_URL` via `DeriveNATSURL`; no separate NATS URL env var | spec-cli.md:222–224 (code block shows `MCLAUDE_TEST_SERVER_URL`); line 205 "NATS server URL is derived from the control-plane server URL" | REFLECTED | — | Code block + prose together cover this |
| 42–44 | Three test files: `integration_main_test.go`, `integration_import_test.go`, `integration_login_test.go`; all `//go:build integration` | spec-cli.md:252 "Test files (all with `//go:build integration`): `cmd/integration_main_test.go` (TestMain), `cmd/integration_import_test.go` (import tests), `cmd/integration_login_test.go` (login test)." | REFLECTED | — | Round 1 fix applied |
| 45 / 63 | `ImportFlags.Input io.Reader` (ADR line 45 has internal typo "Stdin"; line 63 and spec use "Input") | spec-cli.md:254 "`ImportFlags.Input io.Reader` allows tests to inject simulated user input for slug collision prompts (falls back to `os.Stdin`)." | REFLECTED | — | Round 1 fix applied; ADR line 45 has internal typo but spec correctly uses `Input` per line 63 |
| 63–64 | Slug collision: assert `result.ProjectSlug == "my-project-2"`; confirm `GET /api/users/{uslug}/projects/my-project-2` returns HTTP 200 | spec-cli.md:249 "Assert `result.ProjectSlug == "my-project-2"`. Confirm by calling `GET /api/users/{uslug}/projects/my-project-2` and asserting HTTP 200." | REFLECTED | — | Round 1 fix applied |
| 68–71 | Login test: `httptest.Server` mock; background goroutine delivers creds after 200ms; `auth.json` contains `{jwt, nkeySeed, userSlug}` with valid U-key seed | spec-cli.md:250 "`mclaude login` against an `httptest.Server` mock (login uses HTTP only — no NATS or S3). `auth.json` written with valid `{jwt, nkeySeed, userSlug}`; NKey seed is a valid U-key." | REFLECTED | — | |
| 75–80 | Error cases: NATS refused, S3 upload fail, import.confirm timeout, device-code poll timeout | spec-cli.md:258–263 Error cases table | REFLECTED | — | Round 1 fix applied |
| 25 | JWT used as `Authorization: Bearer <jwt>` for all authenticated CP HTTP calls | spec-cli.md:239 "Tests use this JWT as `Authorization: Bearer <jwt>` for all authenticated CP HTTP calls." | REFLECTED | — | |
| 25 | NATS KV bucket `mclaude-sessions-{uslug}`; watch subject `hosts.{hslug}.projects.{pslug}.sessions.>`; within allowed JWT `$KV.mclaude-sessions-{uslug}.hosts.>` | spec-cli.md:248 — bucket name and subject both present | REFLECTED | — | |

### Summary

- Reflected: 22
- Gap: 0
- Partial: 0

**CLEAN — 22 ADR decisions reflected in spec, 0 gaps**
