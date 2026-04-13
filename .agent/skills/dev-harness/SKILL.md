---
name: dev-harness
description: Implementation loop for any mclaude component. Reads the spec, audits gaps, implements production code + tests, and commits. Called after /feature-change has updated the spec. Run repeatedly — converges to fully-implemented, fully-tested.
---

# Dev Harness

Implements and tests a component against its spec. Always called after `/feature-change` has updated the spec. Run repeatedly — each session audits what's implemented vs what the spec requires, implements the next gap, runs tests, and commits.

## Usage

```
/dev-harness [component] [--audit-only] [--category <category>]
```

**component**: `control-plane` | `session-agent` | `spa` | `cli` | `helm` | `all`

- Omit to auto-detect from current directory
- `all` spawns parallel sessions on separate worktrees (see below)

**flags:**
- `--audit-only` — report gaps only, no changes
- `--category <name>` — implement one category only

---

## Reference Docs

Read these in full before writing any code. The spec is the source of truth — implement exactly what it says, nothing more.

| Doc | Owns |
|-----|------|
| `docs/plan-k8s-integration.md` | NATS subjects, KV schema, session lifecycle, provisioning, failure modes |
| `docs/plan-client-architecture.md` | Stores, viewmodels, protocol contract, accumulation algorithm |
| `docs/ui-spec.md` | Screens, wireframes, components, interactions |
| `docs/feature-list.md` | Feature IDs and platform support matrix |
| `docs/spec-*.md` | Any feature-specific spec created by `/feature-change` |

---

## Spec Discipline

- Implement exactly what the spec says. If behavior isn't in the spec, don't build it.
- If you find something in the code that isn't in the spec, that's fine — don't remove it. Only new work must be spec-bounded.
- If the spec is ambiguous, implement the minimal interpretation and note the ambiguity in the commit message.
- **If you discover the spec is missing something required** — stop, go back to `/feature-change` to update the spec first, then return here.

---

## The Loop

```
1. Read ALL relevant spec docs

2. Phase 1 — Spec compliance audit (spec → production code)
   For each feature the spec defines, ask: is it implemented?
   
   control-plane:
   - Every NATS subject listed as "→ control-plane": is there a Subscribe() call?
   - Every HTTP endpoint in the spec: is there a HandleFunc/Handle() for it?
   - Every KV bucket mentioned: is it created on startup?
   
   session-agent:
   - Every NATS subject listed as "→ session-agent": is there a Subscribe() call?
   - Every KV key schema in the spec: does the code write/read those keys?
   - Every session lifecycle state: does the code transition through them?
   
   spa:
   - Every screen in ui-spec.md: does a component exist for it?
   - Every store/viewmodel interface in plan-client-architecture.md: is it implemented?
   - Every NATS subject the client publishes/subscribes: is it wired?
   
   This phase catches "spec says X, no code does X" — the most dangerous gap.
   A feature that exists in the spec but has no production code is MISSING,
   regardless of whether tests exist.

3. Phase 2 — Test coverage audit (production code → tests)
   For each piece of production code: is it tested?
   Classify each category as: implemented | partial | missing

4. Print unified gap report — Phase 1 gaps first, then Phase 2 gaps

5. If --audit-only: stop

6. Pick next gap — Phase 1 gaps take priority over Phase 2 gaps
   (missing production code is more urgent than missing tests)

7. Implement: production code + tests together
   - Production code: exactly what the spec requires
   - Tests: verify the production code matches the spec
   
8. Run full test suite — must pass before continuing
   - On failure: fix, don't skip to the next category

9. Commit: one commit per category
   - Message: "feat(<component>): <category> — <what was implemented>"
   - Never bundle multiple categories in one commit

10. Push

11. Re-audit (both phases) → go to 2

12. When both audits are clean: print summary and stop
```

**Summary must include:**
- Phase 1 gaps found (spec features with no production code) and what was implemented
- Phase 2 gaps found (production code with no tests) and what was added
- Files changed/created with one-line descriptions
- Test count before → after

---

## Per-Component Categories

### control-plane

| Category | Production code | Tests |
|----------|----------------|-------|
| `build` | compiles clean | `go build ./...` |
| `unit` | JWT issuance, NKey generation, subject permission construction | `TestIssueUserJWT`, `TestDecodeUserJWT`, `TestNKeySubjectScope` |
| `projects` | Subscribe `mclaude.*.api.projects.create` → Postgres + `mclaude-projects` KV. Also: `GET/DELETE mclaude.*.api.projects.*` | Request/reply: create project, reply has `{id}`. Error cases: missing name, db unavailable. KV entry present after create. |
| `auth` | `POST /auth/login` → JWT + NKeySeed. `POST /auth/refresh`. `GET /auth/me` | Login success + failure. Refresh with valid/expired token. `natsUrl` absent from response when `NATS_WS_URL` unset. |
| `integration` | Postgres CRUD wired end-to-end | Real NATS + Postgres via `testutil.StartDeps(t)`: user create → Postgres row exists; project create → KV entry appears. |
| `break-glass` | Admin port `:9091` — user/project CRUD, bearer token enforced | Admin routes return 403 without token. CRUD operations via admin port. |
| `monitoring` | OTEL spans on all NATS subscribes and Postgres queries. Prometheus: `mclaude_http_request_duration_seconds`, `mclaude_provisioning_errors_total`, `mclaude_nats_reconnects_total`. zerolog on all ops. | `TestHandleLoginEmitsSpans`, `TestProjectsCreateEmitsMetrics` — invoke real production code, assert spans/metrics from that path. |

### session-agent

| Category | Production code | Tests |
|----------|----------------|-------|
| `build` | compiles clean | `go build ./...` |
| `mocks` | mock-claude binary (stream-json protocol over stdin/stdout). Transcripts: `simple_message.jsonl`, `tool_use.jsonl`, `parallel_tools.jsonl`, `session_resume.jsonl`, `crash_mid_tool.jsonl`, `compaction.jsonl`. `testutil.StartDeps(t)`. `testutil.StartMockAnthropic(t, transcript)`. | — |
| `unit` | Branch slugification, stream-json event parsing, KV key construction, pendingControls map operations | Pure functions, no I/O |
| `integration` | KV bucket init, session CRUD, heartbeat write/TTL expiry | Real NATS + Postgres via StartDeps |
| `component` | Full session lifecycle: create → init → user message → tool_use → control_request → control_response → tool_result → result → KV state transitions | Uses mock-claude |
| `failure` | NATS disconnect buffer+flush, ungraceful restart (stale pendingControls cleared), crash_mid_tool auto-restart with --resume | Uses crash/disconnect transcripts |
| `daemon` | `--daemon` mode: spawn child per project on `projects.create`, restart on crash, JWT refresh goroutine, hostname collision detection via KV | daemon spawns child, restarts crashed child, refreshes JWT before expiry |
| `monitoring` | OTEL spans: Claude spawn, NATS publish per event, KV write. Prometheus: `mclaude_active_sessions`, `mclaude_events_published_total`, `mclaude_nats_reconnects_total`, `mclaude_claude_restarts_total`. zerolog. | `TestSessionStartEmitsSpans`, `TestEventPublishEmitsMetrics` — through production entry points |
| `e2e` | Real k3d cluster, real image, mock-claude sidecar | Full session lifecycle end-to-end via NATS |

### spa

| Category | Production code | Tests |
|----------|----------------|-------|
| `build` | `npm run build` clean, TypeScript strict, no errors | — |
| `mocks` | In-memory NATSClient (pub/sub + KV), mock AuthClient, canned fixtures | — |
| `unit` | EventStore accumulation, AuthStore JWT expiry+refresh, SessionStore KV watch, deduplication | Feed mock events → assert ConversationModel state for each transcript scenario |
| `component` | ConversationVM: sendMessage, approvePermission, interrupt. PermissionPromptVM: multiple simultaneous pendingControls | Assert correct NATS subjects + payloads published |
| `nats-impl` | Real NATSClient via `nats.ws`: connect, disconnect, subscribe, publish, kvGet, kvWatch, kvPut | Unit tests use in-memory mock; real impl used in e2e |
| `views` | Components per `docs/ui-spec.md` exactly: design tokens, AuthScreen, DashboardScreen, SessionDetailScreen, event renderers, Settings, TokenUsage | Visual: rendered output matches wireframe. All wired to stores/viewmodels. |
| `reconnect` | EventStore re-subscribes from `max(lastSeq+1, replayFromSeq)` on NATS disconnect | No duplicate events, no gaps after reconnect simulation |
| `monitoring` | Structured pino logs for all store ops, error boundaries on all components | — |
| `e2e` | Playwright: auth → session list → open session → send message → approve permission → see result | — |

### cli

| Category | Production code | Tests |
|----------|----------------|-------|
| `build` | compiles clean | — |
| `mocks` | Mock unix socket server replaying canned session-agent responses | — |
| `unit` | stream-json → text rendering: assistant text, tool_use, control_request y/n prompt | — |
| `component` | Full REPL: connect → send → render streaming → permission prompt → approve → result | Against mock socket |
| `monitoring` | Structured log output (machine-readable flag), exit codes on error | — |

### helm

| Category | Production code | Tests |
|----------|----------------|-------|
| `lint` | — | `helm lint charts/mclaude` — zero warnings |
| `template` | — | `helm template` clean for: minimal values, production values, air-gapped values |
| `validate` | — | kubeconform against K8s 1.29. Resource requests/limits on all containers. |
| `policy` | — | conftest: no privileged, runAsNonRoot, no `latest` tags, secrets from K8s Secrets only |
| `e2e` | — | Install into k3d, all pods Ready, smoke test: `POST /version` → 200 |

---

## Monitoring Requirements (all Go components)

Every Go component must have all of these:

**Traces (OTEL):** span on every NATS publish/subscribe (subject as attribute), every Postgres query, every external API call, every session lifecycle event. Trace context via `traceparent` NATS header.

**Metrics (Prometheus):** exposed at `:9091/metrics`. See per-component table above for required counters/gauges/histograms.

**Logs (zerolog):** JSON, level via `LOG_LEVEL` env. Every line includes `component`. Include `sessionId` and `userId` where applicable. No `fmt.Println` in production paths.

**Monitoring test pattern:** `TestXxxEmitsSpans` and `TestXxxEmitsMetrics` must invoke **real production entry points** (an HTTP handler, `session.start()`, etc.) — not call metric/span helpers directly. Use `tracetest.NewInMemoryExporter()` and `prometheus.NewRegistry()`.

---

## Mock Implementations

All mocks in `{component-root}/testutil/`. Built once under `mocks` category, reused everywhere.

**mock-claude** (session-agent): Go binary, reads `$MOCK_TRANSCRIPT`, plays back NDJSON turns on any stdin. Turns separated by `{"type":"__turn_boundary__"}`.

**mock-nats**: Real NATS server via Docker Compose — never mock the client library. `testutil.StartDeps(t)` starts compose, waits for health, registers cleanup.

**mock-k8s** (control-plane): `sigs.k8s.io/controller-runtime/pkg/envtest` — real API server, no cluster needed.

**mock-anthropic** (session-agent): HTTP server at `ANTHROPIC_BASE_URL`. Replays canned API responses matching the active transcript.

---

## Parallel (`all`)

```bash
# Create one worktree per component, each on its own branch
git worktree add worktrees/control-plane harness/control-plane
git worktree add worktrees/session-agent harness/session-agent
git worktree add worktrees/spa           harness/spa
git worktree add worktrees/cli           harness/cli
git worktree add worktrees/helm          harness/helm
```

Spawn a mclaude session on each worktree with `/dev-harness <component>` as the initial prompt. Sessions run independently. When all reach audit-clean, open one PR per branch.

If a session dies: re-create on the same worktree — it re-audits from last push and continues.

---

## Convergence Criteria

Done when:
1. `go test ./... -race` (or `npm test`) passes with zero failures
2. `--audit-only` returns zero missing or partial categories
3. All monitoring requirements satisfied
4. `helm lint` + `kubeconform` pass (helm only)
5. At least one E2E test exists and passes

Open a PR only when all five criteria are met.
