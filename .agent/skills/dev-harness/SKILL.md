---
name: dev-harness
description: Iteratively build and verify test + monitoring infrastructure for any mclaude component. Audits gaps, implements the next missing category, runs tests, and commits. Run repeatedly until the branch converges to fully-tested.
---

# Dev Harness

Iteratively build and verify the test + monitoring infrastructure for any mclaude component. Run this skill repeatedly — each session audits what exists, implements the next missing category, runs the tests, and commits. The branch converges to a fully-tested, fully-instrumented component.

## Usage

```
/dev-harness [component] [--audit-only] [--category <category>]
```

**component**: `session-agent` | `control-plane` | `cli` | `spa` | `helm` | `all`

- `all` runs each component as a parallel mclaude session on its own branch
- Omit component to auto-detect from current directory

**flags**:
- `--audit-only` — report gaps, make no changes
- `--category <name>` — implement only one category (see below)

---

## Reference Docs

Before doing anything, read these in full. They are the source of truth for what every component does, what interfaces it exposes, and what must be tested:

- `docs/plan-k8s-integration.md` — architecture, NATS subjects, KV schema, stream-json protocol, session lifecycle, all failure modes
- `docs/plan-client-architecture.md` — client layers, stores, view models, protocol contract, accumulation algorithm
- `docs/feature-list.md` — feature IDs and platform support matrix
- `docs/ui-spec.md` — cross-platform wireframe spec: screens, components, event types, visual states, interaction patterns (required for `views` category)

---

## Spec Discipline

**The reference docs are the source of truth. Implement exactly what they specify — nothing more.**

- For the `views` category: every screen, component, field, and interaction must match `docs/ui-spec.md` exactly. If a feature is not in the spec, do not build it. If the spec shows two fields, build two fields — not three.
- For backend categories: every endpoint, subject, and payload must match `docs/plan-k8s-integration.md`. Do not add convenience endpoints or extra fields.
- When the spec is ambiguous, implement the minimal interpretation and note the ambiguity in the commit message. Do not invent behavior.
- If the current code has more than the spec describes, that is acceptable — do not remove it. Only new work must be spec-bounded.

---

## Algorithm

```
1. Identify component root (passed as arg or detected from cwd)
2. Read reference docs (above) in full before writing any code
3. Audit — classify each applicable test/monitoring category as:
     implemented | partial | missing
4. Print gap report ordered by dependency
5. If --audit-only: stop here
6. Pick the next missing category (dependency order)
7. Implement the category (see per-component specs below)
   - Implement ONLY what the reference docs specify for this category
   - Do not add helpers, screens, fields, or behaviors not in the spec
8. Run the full test suite: must pass before proceeding
   - If tests fail: fix, don't move to next category
9. Commit: one commit per category, message: "harness(component): add {category}"
10. Push to current branch
11. Re-audit → go to step 4
12. When audit is clean: print summary, stop
```

Convergence: each run moves the branch strictly closer to fully-tested. Sessions are disposable — die and restart, re-audit picks up from last push.

---

## Mock Implementations

All mocks are in `{component-root}/testutil/`. Build them once (category: `mocks`), reuse everywhere.

### mock-claude (session-agent only)

A Go binary that speaks the stream-json protocol over stdin/stdout. Used instead of a real Claude process in all session-agent tests.

```go
// testutil/mock-claude/main.go
// Reads a transcript file path from $MOCK_TRANSCRIPT env var.
// On any stdin input: plays back the next turn from the transcript.
// Transcript format: NDJSON file, one stream-json event per line,
// turns separated by {"type":"__turn_boundary__"}.
// Exits with {"type":"result","subtype":"success","usage":{...}} after last turn.
```

Canned transcripts live in `testutil/transcripts/`:
- `simple_message.jsonl` — single user message, assistant text response, result
- `tool_use.jsonl` — user message, assistant uses Bash, control_request, control_response, tool_result, result
- `parallel_tools.jsonl` — two simultaneous tool uses with parallel control_requests
- `session_resume.jsonl` — init event followed by prior conversation turns, then new turn
- `crash_mid_tool.jsonl` — process exits mid tool_use (no result event)
- `compaction.jsonl` — compact_boundary event mid-session

### mock-nats

Use a **real NATS server** via Docker Compose — do not mock the NATS client library. Tests run against a real broker. This catches subject permission issues, JetStream config errors, and KV behaviour that mocks cannot reproduce.

```yaml
# testutil/docker-compose.yml
services:
  nats:
    image: nats:2.10-alpine
    command: ["-js", "-p", "4222"]
    ports: ["4222:4222", "8222:8222"]  # 8222 = monitoring HTTP

  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: mclaude_test
      POSTGRES_USER: mclaude
      POSTGRES_PASSWORD: mclaude
    ports: ["5432:5432"]
```

Helper: `testutil.StartDeps(t)` — starts compose services, returns cleanup func, waits for health. Uses `github.com/testcontainers/testcontainers-go/modules/compose`.

### mock-k8s (control-plane only)

Use `sigs.k8s.io/controller-runtime/pkg/envtest` for unit/integration tests — real API server, no cluster needed. For E2E, use k3d (already in Phase 5 plan).

```go
// testutil/k8s.go
func StartEnvtest(t *testing.T) *rest.Config {
    env := &envtest.Environment{
        CRDDirectoryPaths: []string{...},
    }
    cfg, err := env.Start()
    t.Cleanup(func() { env.Stop() })
    return cfg
}
```

### mock-anthropic (session-agent only)

HTTP server that intercepts `api.anthropic.com`. Session agent tests set `ANTHROPIC_BASE_URL=http://localhost:9999`. The mock replays canned API responses matching the transcript being used.

```go
// testutil/mock-anthropic.go
func StartMockAnthropic(t *testing.T, transcript string) string {
    // returns base URL, registers t.Cleanup
}
```

---

## Per-Component Categories

### session-agent (Go)

Categories in dependency order:

| Category | What to implement |
|----------|-------------------|
| `mocks` | mock-claude binary, canned transcripts, testutil/docker-compose.yml, StartDeps helper, mock-anthropic |
| `build` | `go build ./...` clean, race detector enabled in test binary |
| `unit` | Pure logic: branch slugification, stream-json event parsing, KV key construction, pendingControls map operations, worktree reference scan |
| `integration` | Real NATS + real Postgres via StartDeps: KV bucket init, session CRUD in KV, heartbeat write/TTL expiry, lifecycle event publish/subscribe |
| `component` | Full session lifecycle using mock-claude: create session → init event → user message → tool_use + control_request → control_response → tool_result → result → KV state transitions at each step |
| `failure` | Failure scenarios using mock-claude transcripts: NATS disconnect during event stream (buffer + flush), ungraceful restart (stale pendingControls cleared, Claude resumes), crash_mid_tool (auto-restart with --resume) |
| `monitoring` | OTEL trace spans on all NATS publishes, Claude process spawns, KV writes. Prometheus metrics: active_sessions, events_published_total, nats_reconnects_total, claude_restarts_total. Structured zerolog on all operations. Verify spans appear in OTEL collector. |
| `e2e` | Real k3d cluster, real session-agent image, mock-claude sidecar. Full session lifecycle end-to-end via NATS. |

**Test file layout:**
```
mclaude-session-agent/
  testutil/
    deps.go              StartDeps (compose), StartMockAnthropic
    mock-claude/
      main.go
    transcripts/
      *.jsonl
  session_test.go        component tests (uses mock-claude)
  router_test.go         unit tests (event parsing)
  state_test.go          unit + integration (KV operations)
  worktree_test.go       unit tests (slugify, reference scan)
  terminal_test.go       component tests (PTY I/O via NATS)
  failure_test.go        failure scenario tests
  monitoring_test.go     verify spans + metrics are emitted
```

### control-plane (Go)

| Category | What to implement |
|----------|-------------------|
| `mocks` | testutil/docker-compose.yml (NATS + Postgres), StartDeps, mock-k8s envtest setup |
| `build` | `go build ./...` clean |
| `unit` | JWT issuance + claim validation, NATS NKey generation, subject permission construction, dbmate migration files parse clean |
| `integration` | Real NATS + Postgres: user create → Postgres insert + KV publish, user delete → NATS JWT revocation + namespace delete (envtest), project create → Deployment + PVC applied (envtest), minClientVersion returned on /version |
| `auth` | Login → JWT issued with correct subject scopes, refresh → new JWT, SSO callback stub, SCIM user push → provisioning flow |
| `break-glass` | Admin port :9090 — project create/delete/list without NATS, session stop, bearer token auth enforced |
| `monitoring` | OTEL traces on all provisioning operations, HTTP request latency histogram, provisioning_errors_total counter. Structured zerolog. |

### cli (Go)

| Category | What to implement |
|----------|-------------------|
| `mocks` | mock unix socket server that replays canned session-agent responses |
| `build` | `go build ./...` clean |
| `unit` | stream-json → text rendering (assistant text, tool_use, control_request y/n prompt) |
| `component` | Full REPL flow against mock unix socket: connect → send message → render streaming response → permission prompt → approve → render result |
| `monitoring` | Structured log output (machine-readable mode flag), exit codes on error |

### spa (TypeScript)

| Category | What to implement |
|----------|-------------------|
| `mocks` | Mock NATSClient (in-memory pub/sub, KV), mock AuthClient, canned SessionState + ConversationModel fixtures |
| `build` | `npm run build` clean, TypeScript strict mode, no type errors |
| `unit` | EventStore accumulation: feed mock events → assert ConversationModel state for each transcript scenario. AuthStore JWT expiry + refresh loop. SessionStore KV watch → SessionState updates. Deduplication: duplicate sequence numbers skipped. |
| `component` | ConversationVM actions: sendMessage publishes correct NATS payload, approvePermission publishes correct control_response, interrupt publishes interrupt. PermissionPromptVM: multiple simultaneous pendingControls rendered. |
| `nats-impl` | Implement the real NATSClient using `nats.ws`. Replace all `throw new Error('not implemented')` stubs. Connect to NATS server URL from config. Implement: `connect`, `disconnect`, `subscribe(subject, cb)`, `publish(subject, data)`, `kvGet(bucket, key)`, `kvWatch(bucket, key, cb)`, `kvPut(bucket, key, value)`. Must work in browser (nats.ws uses WebSocket transport). Use the same in-memory mock from `mocks` category for unit tests — the real implementation is used in `e2e` and production. |
| `views` | Build React UI components per `docs/ui-spec.md` **exactly** — no more, no less. Read the spec in full before writing a single line. Implement in order: (1) design tokens (CSS custom properties matching the spec palette exactly), (2) AuthScreen, (3) DashboardScreen, (4) SessionDetailScreen (nav + tabs + input bar), (5) event renderers listed in the spec, (6) Settings screen, (7) TokenUsage screen. Each screen must match the spec wireframe: same fields, same labels, same layout — do not add fields or interactions not shown. Wire components to the stores/viewmodels already built. |
| `reconnect` | NATS disconnect simulation: EventStore re-subscribes from max(lastSeq+1, replayFromSeq), no duplicate events rendered, no gaps. |
| `forced-update` | X4: minClientVersion below client version → UI blocked, correct platform message shown. |
| `monitoring` | Console structured logs for all store operations, error boundaries on all components, Sentry/OTEL error reporting wired. |
| `e2e` | Playwright: full session flow in browser against mock NATS. Auth → session list → open session → send message → approve permission → see result. |

**Test file layout:**
```
mclaude-web/
  src/
    stores/
      event-store.test.ts
      session-store.test.ts
      auth-store.test.ts
    viewmodels/
      conversation-vm.test.ts
      permission-prompt-vm.test.ts
    transport/
      nats-client.ts        real NATSClient (nats.ws)
      nats-client.test.ts   unit tests using in-memory mock
    components/
      AuthScreen.tsx
      DashboardScreen.tsx
      SessionDetailScreen.tsx
      events/
        UserMessage.tsx
        AssistantText.tsx
        ThinkingBlock.tsx
        ToolCard.tsx        (tool_use + tool_result pair)
        AskUserQuestion.tsx
        AgentGroup.tsx
        SystemEvent.tsx
        DiffView.tsx
      Settings.tsx
      TokenUsage.tsx
    testutil/
      mock-nats.ts        in-memory NATS mock
      fixtures.ts         canned SessionState, events, transcripts
  e2e/
    session-flow.spec.ts  Playwright full flow
```

### helm

| Category | What to implement |
|----------|-------------------|
| `lint` | `helm lint charts/mclaude` — no warnings |
| `template` | `helm template` renders without error for: minimal values, AKS production values, air-gapped values (registry mirrors set) |
| `validate` | Rendered manifests pass kubeconform against K8s 1.29 schema. All required fields present. Resource requests/limits set on all containers. |
| `policy` | Kyverno or conftest policies: no privileged containers, runAsNonRoot enforced, no latest image tags, all secrets from K8s Secrets not env literals. |
| `e2e` | Helm install into k3d cluster, wait for all pods Ready, run smoke test (POST /version returns 200). |

---

## Monitoring Requirements (all components)

Every component must have all of these before the harness is considered complete:

### Traces (OpenTelemetry)
- Every NATS publish/subscribe operation: `nats.publish`, `nats.subscribe` spans with subject as attribute
- Every external call (Anthropic API, K8s API, Postgres query): span with result status
- Every session lifecycle event: `session.create`, `session.resume`, `session.delete` spans
- Trace context propagated via NATS message headers (`traceparent`)

### Metrics (Prometheus)
- `mclaude_active_sessions` gauge (session-agent)
- `mclaude_events_published_total` counter, labeled by event type (session-agent)
- `mclaude_nats_reconnects_total` counter (all Go components)
- `mclaude_claude_restarts_total` counter (session-agent)
- `mclaude_http_request_duration_seconds` histogram (control-plane)
- `mclaude_provisioning_errors_total` counter (control-plane)
- Exposed at `:9090/metrics` (separate from break-glass admin port — use `:9091/metrics`)

### Logs (structured)
- Go: zerolog, JSON output, level configurable via `LOG_LEVEL` env var
- TypeScript: pino, JSON output
- Every log line includes: `component`, `sessionId` (where applicable), `userId` (where applicable)
- No `fmt.Println` or `console.log` in production paths — all through structured logger

### Monitoring test
Each component has a `monitoring_test.go` (or `.test.ts`) that:
1. Runs a full operation (e.g. create session, send message)
2. Asserts spans were emitted with correct attributes
3. Asserts metrics counters incremented
4. Asserts log lines contain required fields

Use `go.opentelemetry.io/otel/sdk/trace/tracetest` (in-memory exporter) for Go trace assertions. Use `prom/client_golang/prometheus/testutil` for metric assertions.

---

## Parallel (`all`)

Parallel orchestration is handled outside the skill:

**1. You create the worktrees** (one per component, each on its own branch):
```bash
git worktree add worktrees/session-agent harness/session-agent
git worktree add worktrees/control-plane harness/control-plane
git worktree add worktrees/cli harness/cli
git worktree add worktrees/spa harness/spa
git worktree add worktrees/helm harness/helm
```

**2. You ask Claude to spawn sessions** — Claude calls `create_session` on each worktree with `/dev-harness {component}` as the initial prompt. All five sessions start immediately in parallel.

Each session runs independently in its worktree. No coordination needed — components don't share branches. When all sessions reach audit-clean, open one PR per branch.

If a session dies before finishing: re-create it on the same worktree. It re-audits, picks up from the last pushed commit, and continues.

---

## Convergence Criteria

A component's harness is complete when:
1. `go test ./... -race` (or `npm test`) passes with zero failures
2. `--audit-only` returns zero missing categories
3. All monitoring requirements above are satisfied
4. `helm lint` + `kubeconform` pass (helm only)
5. At least one E2E test exists and passes

At that point: open a PR. Do not open PRs for partial harnesses.
