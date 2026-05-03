# Spec: CLI

## Role

mclaude-cli is a terminal client for the mclaude platform. It attaches to running session agents over unix sockets, provides an interactive REPL for sending messages and approving tool-use permission requests, lists sessions for a given user/project, and imports existing Claude Code session data into mclaude. It authenticates via a device-code flow and connects to NATS using JWT + NKey credentials for import operations. It reads default slug values from a local context file (`~/.mclaude/context.json`) so users do not need to pass identity flags on every invocation.

## Deployment

Installed as a standalone Go binary (`mclaude-cli`). No container, no daemon -- invoked directly from the shell. Requires a running session agent exposing a unix socket for the attach command. The import command communicates with the control-plane via NATS using JWT + NKey credentials.

## Interfaces

### Commands

#### `mclaude-cli attach <session-id>`

Connects to a session agent's unix socket and starts an interactive REPL. Events from the agent (streaming text, tool use, permission requests, progress, results, state changes, compaction boundaries) are rendered as human-readable terminal output. User input is sent as messages; when a permission prompt is pending, `y`/`n` input is sent as an allow/deny control response instead.

Flags:

| Flag | Description | Default |
|------|-------------|---------|
| `--socket <path>` | Override unix socket path | `/tmp/mclaude-session-{id}.sock` |
| `--log-machine` | Emit structured JSON logs to stderr | off (pretty console logs) |
| `--log-level <level>` | Log verbosity: `debug`, `info`, `warn`, `error` | `info` |

#### `mclaude-cli session list`

Resolves user and project slugs (from flags or context file), validates them, and prints the NATS KV key prefix that would be used to query sessions. Does not make network calls -- outputs the resolved parameters only.

Flags:

| Flag | Description | Default |
|------|-------------|---------|
| `-u <uslug>` | User slug | Value from `~/.mclaude/context.json` |
| `-p <pslug>` | Project slug (accepts `@pslug` short form) | Value from `~/.mclaude/context.json` |

#### `mclaude login`

Authenticates the user against the control-plane using a device-code flow and persists NATS credentials for subsequent CLI operations (import, admin, cluster commands).

**Device-code flow:**

1. CLI generates an NKey pair locally — the private seed never leaves the machine.
2. CLI sends `POST /api/auth/device-code` to the control-plane with `{ publicKey }` (the NKey public key).
3. Control-plane returns `{ deviceCode, userCode, verificationUrl, expiresIn, interval }`.
4. CLI displays the verification URL and user code, instructing the user to open the URL in a browser and enter the code.
5. CLI polls `POST /api/auth/device-code/poll` with `{ deviceCode }` at the specified interval until the user completes authentication or the code expires (15-minute TTL). The control-plane always responds HTTP 200. The response body is `{ "status": "pending" | "authorized", "jwt": "...", "userSlug": "...", "natsUrl": "..." }` — `jwt`, `userSlug`, and `natsUrl` are omitted when status is `"pending"`.
6. On `status == "authorized"`, the poll response contains `{ status, jwt, userSlug, natsUrl }` — a signed NATS JWT, the user's slug, and the NATS WebSocket URL (empty when the server expects the client to derive it from the control-plane URL). No seed or NKey material comes from the server.
7. CLI writes credentials to `~/.mclaude/auth.json` (mode `0600`) in the format: `{ "jwt": "<nats-jwt>", "nkeySeed": "<local-nkey-seed>", "userSlug": "<uslug>", "natsUrl": "<wss-url>" }`. The `natsUrl` field is omitted when the poll response returned an empty value.

The credentials are user-scoped (not host-scoped): a single login covers operations across all of the user's hosts. Re-running `mclaude login` regenerates the NKey pair and overwrites the file.

**JWT refresh:** Deferred — JWT refresh before import operations will be implemented when NATS import operations are fully wired. For now, the user must re-run `mclaude login` if the JWT expires.

Flags:

| Flag | Description | Default |
|------|-------------|---------|
| `--server <url>` | Control-plane base URL | Read from `~/.mclaude/context.json`'s `server` key, otherwise `https://api.mclaude.internal` |

#### `mclaude import [--host <hslug>]`

Imports existing Claude Code session data from the local machine into mclaude. Creates a new project containing all historical sessions for the current working directory.

**Prerequisites:** User must be logged in (`mclaude login`). An active host must be registered and selected (`mclaude host register` + `mclaude host use`).

**CWD encoding algorithm:** The CLI derives an encoded CWD from the current directory using Claude Code's path encoding: take the absolute path (e.g. `/Users/rsong/work/mclaude`), replace every `/` with `-`, strip the leading `-` (e.g. `Users-rsong-work-mclaude`). This matches the directory names under `~/.claude/projects/`. The CLI verifies at runtime that the derived path exists under `~/.claude/projects/`; if it doesn't, the CLI lists available encoded directories and errors with a hint.

**Flow:**

1. Loads auth credentials from `~/.mclaude/auth.json` (errors if not logged in).
2. Reads the active host from context (`~/.mclaude/context.json`); `--host <hslug>` overrides.
3. Derives encoded CWD and discovers session data at `~/.claude/projects/{encoded-cwd}/`:
   - JSONL transcripts: `{sessionId}.jsonl`
   - Subagent data: `{sessionId}/subagents/`
   - Memories: `memory/` directory
   - Project CLAUDE.md (from CWD `.claude/CLAUDE.md` or `CLAUDE.md`)
4. Connects to NATS using stored JWT + NKey seed from `~/.mclaude/auth.json`.
5. Derives project name from the last path component of CWD. Checks slug availability via NATS request/reply to the control-plane (`mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug`).
6. If slug taken: prompts user for a new name, re-checks. Loops until available.
7. Packages data into `import-{slug}.tar.gz` with `metadata.json` containing `{ cwd, gitRemote, gitBranch, importedAt, sessionIds, claudeCodeVersion }`.
8. Requests pre-signed upload URL from CP via NATS request/reply (`mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.request`) with `{ slug, sizeBytes }`.
9. Uploads archive directly to S3 using the signed URL.
10. Confirms upload via NATS request/reply (`mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.confirm`) with `{ importId }`.
11. Waits for provisioning acknowledgement, prints success message.

Flags:

| Flag | Description | Default |
|------|-------------|---------|
| `--host <hslug>` | Host slug | Read from `~/.mclaude/active-host` symlink |
| `--server <url>` | Control-plane base URL (for NATS bootstrap) | Read from `~/.mclaude/context.json`'s `server` key, otherwise `https://api.mclaude.internal` |

#### `mclaude host register [--name <name>]`

Device-code registration flow for BYOH machines. Prompts for a hostname (default = `hostname` output, slugified). Generates an NKey pair locally — the private seed never leaves the machine, written to `~/.mclaude/hosts/{hslug}/nkey.seed` (mode 0600). Calls `POST /api/users/{uslug}/hosts/code` with `{publicKey}` to get a 6-character device code, then prints instructions for the user to open the dashboard and enter the code. Polls `GET /api/users/{uslug}/hosts/code/{code}` until the status changes from `pending` to `completed`. On completion, writes `~/.mclaude/hosts/{hslug}/{nats.creds, config.json}` from the returned JWT + the locally-stored seed, and symlinks `~/.mclaude/active-host → {hslug}`.

Flags:

| Flag | Description | Default |
|------|-------------|---------|
| `--name <name>` | Display name for the host | `hostname` output, slugified |

#### `mclaude host list`

Lists all hosts the authenticated user owns or has been granted access to. Calls `GET /api/users/{uslug}/hosts` and prints a table of slug, name, type, role, and online status.

#### `mclaude host use <hslug>`

Sets the active host by symlinking `~/.mclaude/active-host → ~/.mclaude/hosts/{hslug}/`. Subsequent commands that require a host slug (e.g. `mclaude daemon`) read from this symlink when `--host` is not provided.

#### `mclaude host rm <hslug>`

Removes a host registration. Calls `DELETE /api/users/{uslug}/hosts/{hslug}` and removes the local `~/.mclaude/hosts/{hslug}/` directory. If the removed host is the active host, the `active-host` symlink is also removed.

#### `mclaude cluster register`

Admin-only. Registers a new K8s worker cluster. Calls `POST /admin/clusters`.

Flags:

| Flag | Description | Default |
|------|-------------|---------|
| `--slug <cslug>` | Cluster slug (required; becomes the `hosts.slug` for all granted users) | (none) |
| `--name <display>` | Display name | Defaults to slug |
| `--jetstream-domain <jsd>` | JetStream domain for the worker NATS | (required) |
| `--leaf-url <url>` | Worker NATS leaf-node URL (e.g. `nats-leaf://hub:7422`) | (required) |
| `--direct-nats-url <wss>` | Externally-reachable WebSocket URL for SPA direct-to-worker | (optional) |

Returns `{slug, leafJwt, leafSeed, accountJwt, operatorJwt, jsDomain, directNatsUrl}` for the admin to drop into the worker cluster's NATS Secret + `mclaude-worker` Helm values.

#### `mclaude cluster grant <cluster-slug> <uslug>`

Admin-only. Grants a user access to a cluster. Calls `POST /admin/clusters/{cluster-slug}/grants` with `{userSlug}`. Control-plane creates a new `hosts` row for that user with the cluster-shared fields copied from the existing cluster host row, and mints a per-user JWT.

#### `mclaude daemon --host <hslug>`

Starts the BYOH local controller daemon. Reads `--host` from the flag or from `~/.mclaude/active-host` symlink if unset. Connects to hub NATS using the host's credentials from `~/.mclaude/hosts/{hslug}/nats.creds`, subscribes to `mclaude.users.{uslug}.hosts.{hslug}.api.projects.>`, and starts session-agent subprocesses for each provisioned project. Intended to run as a launchd / systemd service.

Flags:

| Flag | Description | Default |
|------|-------------|---------|
| `--host <hslug>` | Host slug | Read from `~/.mclaude/active-host` symlink |

### Context file

`~/.mclaude/context.json` stores `userSlug`, `projectSlug`, `hostSlug`, and `server` defaults. The `server` key holds the control-plane base URL (e.g. `https://api.mclaude.internal`); `--server <url>` flags on individual commands override this value. Default when absent: `https://api.mclaude.internal`. The path is overridable via the `MCLAUDE_CONTEXT_FILE` environment variable. If the file does not exist, all fields default to empty.

### Wire protocol

The attach REPL communicates over a newline-delimited JSON (JSONL) protocol on the unix socket. The unix socket currently carries Claude Code's native stream-json format (the session agent forwards raw backend output). The canonical `SessionEvent` types (defined in ADR-0005) are the NATS-layer schema; the CLI's accumulator handles the native stream-json types directly. The mapping between native and canonical types is shown below for reference.

**Canonical SessionEvent types** (per ADR-0005):

| Event type | Description |
|------------|-------------|
| `init` | Backend, model, tools, skills, agents, capabilities |
| `state_change` | Driver state: `idle`, `running`, `requires_action` |
| `text_delta` | Streaming text content (with `messageId`, `blockIndex`, `text`) |
| `thinking_delta` | Streaming thinking content |
| `message` | Complete assistant/user message with content blocks |
| `tool_call` | Tool invocation started (`toolUseId`, `toolName`, `input`) |
| `tool_progress` | Tool execution progress |
| `tool_result` | Tool execution result (`content`, `isError`) |
| `permission` | Permission request or resolution (`requestId`, `resolved`, `allowed`) |
| `turn_complete` | Turn finished with token/cost metrics |
| `error` | Error with `message`, `code`, `retryable` |
| `backend_specific` | Opaque backend-unique events (e.g. Droid missions) |

**Claude Code stream-json mapping:** When the backend is `claude_code`, the session agent's ClaudeCodeDriver translates Claude Code's native stream-json types to canonical events as follows:

| Claude Code type | Canonical event |
|-----------------|-----------------|
| `system.init` | `init` |
| `system.session_state_changed` | `state_change` |
| `stream` (content_block_delta, text) | `text_delta` |
| `stream` (content_block_delta, thinking) | `thinking_delta` |
| `assistant` (complete message) | `message` + extracted `tool_call` per tool_use block |
| `tool_progress` | `tool_progress` |
| user message with `tool_result` blocks | `tool_result` per block |
| `sdk_control_request.permission` | `permission` (resolved=false) |
| `control_response` to permission | `permission` (resolved=true) |
| `result` | `turn_complete` |

Outbound messages from the CLI are `SessionInput` typed commands (per ADR-0005): `message` (chat message with optional `attachments`), `skill_invoke`, or `permission_response` (with `requestId`, `allowed`, `behavior`).

### Accumulator behavior

The CLI accumulates inbound canonical `SessionEvent` events into a conversation model for rendering. Events rendered: `text_delta` (streaming text), `message` (complete messages with content blocks), `tool_call` / `tool_result` (tool invocations and results), `permission` (permission requests/resolutions). Events used for state tracking: `state_change`, `turn_complete`, `init`. Events silently discarded: `thinking_delta`, `backend_specific`.

When a `message` event carries a non-null `parentToolUseId`, the resulting turn is nested under the parent tool_call's agent turn (subagent nesting).

On `clear` event: the accumulator resets all turns to empty (no divider rendered, just a blank conversation state).

On `compact_boundary` system event: the accumulator resets all turns and renders a `--- context compacted ---` divider.

### Reconnection

No reconnection. If the unix socket drops, the CLI exits immediately. The user re-runs `mclaude-cli attach <session-id>` to reconnect.

### NATS connection

The CLI connects to NATS for import operations and slug availability checks. Connection uses the JWT + NKey seed stored in `~/.mclaude/auth.json` (written by `mclaude login`). The NATS server WebSocket URL is read from `natsUrl` in `~/.mclaude/auth.json` when non-empty; otherwise derived from the control-plane server URL by replacing the scheme (`https` → `wss`) and appending `/nats`.

The CLI uses NATS request/reply for:
- Slug availability checks (`mclaude.users.{uslug}.hosts.{hslug}.projects.check-slug`)
- Import upload URL requests (`…projects.{pslug}.import.request`)
- Import confirmation (`…projects.{pslug}.import.confirm`)

## Smoke Tests (CLI e2e)

CLI smoke tests prove user behaviors work end-to-end against a real deployed stack. They are the CLI subset of the full deployment certification suite (web Playwright + CLI smoke tests together certify any deployment). Run with `//go:build integration`:

```bash
# Forward admin port first (admin port is 9091 externally, pod listens on 9090):
kubectl port-forward -n mclaude-system svc/mclaude-cp-control-plane 9091:9090 &

ADMIN_URL=http://localhost:9091 \
ADMIN_TOKEN=dev-admin-token \
MCLAUDE_TEST_HOST_SLUG=dev-local \
MCLAUDE_TEST_SERVER_URL=https://<control-plane-domain> \
go test -tags integration ./cmd/...
```

The CP returns `natsUrl` in its device-code poll response (`s.natsWsURL`). The CLI stores it in `auth.json` and uses it for NATS connections — no separate NATS URL env var is needed.

### Prerequisites

- `ADMIN_URL` must be set (e.g. `http://localhost:9091` via `kubectl port-forward`). If unset, `TestMain` writes `{skipped:true}` to `cli-integration/.test-creds.json` and exits without error — tests are skipped, not failed. This matches the Playwright global-setup skip behavior (ADR-0064).
- `MCLAUDE_TEST_HOST_SLUG` must refer to a host that is already registered in the cluster and has a running session-agent. `TestMain` does **not** register a throwaway host — the host is pre-existing infrastructure. Default: `dev-local`.

### Test user setup

`TestMain` creates an ephemeral test user via `POST /admin/users` (same admin API as Playwright). The response returns `{id, email, name, slug}`. TestMain supplies its own generated password in the request body; the password is not returned in the response. NATS credentials are obtained by automating the device-code flow (no browser needed):

1. Start `RunLogin(flags)` in a goroutine — POSTs `/api/auth/device-code` with `{publicKey}` (CLI-generated NKey public key), gets `{deviceCode, userCode}`, begins polling
2. Complete the code: form `POST /api/auth/device-code/verify` with fields `user_code=<userCode>`, `email=<testEmail>`, `password=<testToken>` — the endpoint authenticates inline from form fields; no separate login or session cookie needed
3. `RunLogin`'s poll detects `{"status":"authorized"}` and writes NATS credentials to `cli-integration/.test-creds.json` (gitignored): `{jwt, nkeySeed, userSlug, natsUrl}` — `natsUrl` is whatever the CP returned in the poll response (the correct NATS WebSocket URL for this deployment)

The JWT is bound to the CLI's `publicKey` (sent in step 1). The CLI signs NATS connections with the matching `nkeySeed`. Tests use this JWT as `Authorization: Bearer <jwt>` for all authenticated CP HTTP calls.

`TestMain` teardown: delete test project via admin API (CP deletes S3 prefix `{uslug}/{hslug}/{pslug}/`), then delete the test user.

### Test cases

| Test | User behavior verified |
|------|-----------------------|
| `TestIntegration_Import_HappyPath` | `mclaude import` against the pre-registered host: archive uploaded to S3, project created in CP, session-agent unpacks. Completion detected by polling `GET /api/users/{uslug}/projects/{pslug}` (`Authorization: Bearer <jwt>` from `.test-creds.json`) every 2s until `importRef` is null (session-agent cleared it after successful unpack), timeout 60s. Sessions asserted visible: connect to NATS using `natsUrl` from `.test-creds.json` (populated by `RunLogin` from the poll response) with test-user JWT + NKey seed, call `kv.Watch("hosts.{hslug}.projects.{pslug}.sessions.>")` on `mclaude-sessions-{uslug}` bucket, drain `watcher.Updates()` until nil (initial values exhausted), assert at least 1 entry received, call `watcher.Stop()`. |
| `TestIntegration_Import_SlugCollision` | Import once (project created), re-import same CWD: CP returns slug unavailable, CLI prompts for new name (injected via `ImportFlags.Input`), second import succeeds. Assert `result.ProjectSlug == "my-project-2"`. Confirm by calling `GET /api/users/{uslug}/projects/my-project-2` and asserting HTTP 200. |
| `TestIntegration_Login_DeviceCode` | `mclaude login` against an `httptest.Server` mock (login uses HTTP only — no NATS or S3). The mock `/api/auth/device-code/poll` endpoint returns `{"status":"pending"}` on the first call and `{"status":"authorized","jwt":"...","userSlug":"...","natsUrl":"wss://mock-nats.example.com"}` on the second. `auth.json` written with valid `{jwt, nkeySeed, userSlug, natsUrl}`; NKey seed is a valid U-key. |

Test files (all with `//go:build integration`): `cmd/integration_main_test.go` (TestMain), `cmd/integration_import_test.go` (import tests), `cmd/integration_login_test.go` (login test).

`ImportFlags.Input io.Reader` allows tests to inject simulated user input for slug collision prompts (falls back to `os.Stdin`).

### Error cases

These error paths are verified by unit tests in `cmd/import_test.go` and `cmd/login_test.go`, not integration tests.

| Error | Assertion |
|-------|-----------|
| NATS connection refused or stale `natsUrl` in `auth.json` (e.g. cluster reconfigured) | `RunImport` returns a NATS connection error; user re-runs `mclaude login` to refresh `auth.json` with the current URL |
| S3 upload fails (CP returns bad pre-signed URL) | `RunImport` returns upload error |
| `import.confirm` NATS timeout | `RunImport` returns error after timeout |
| Device-code poll timeout | `RunLogin` returns error; `auth.json` not written |

## Dependencies

- **Session agent unix socket** -- the attach command connects to a session agent's socket at `/tmp/mclaude-session-{id}.sock` (or a custom path).
- **NATS** -- the import command connects to NATS using JWT + NKey credentials for request/reply operations with the control-plane.
- **`~/.mclaude/context.json`** -- optional; provides default user/project/host slugs and control-plane server URL.
- **`~/.mclaude/auth.json`** -- NATS credentials (`{ jwt, nkeySeed, userSlug, natsUrl }`) written by `mclaude login`. `natsUrl` is the NATS WebSocket URL from the CP poll response; omitted when empty (CLI falls back to deriving from server URL). Required for import and admin operations.
- **mclaude-common (`mclaude.io/common`)** -- shared slug validation (`pkg/slug`) and NATS subject construction (`pkg/subj`).
- **zerolog** -- structured logging.
