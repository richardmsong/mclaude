# ADR: E2E state-snapshot testing scaffolding

**Status**: draft
**Status history**:
- 2026-05-06: draft

## Overview

Add an end-to-end test scaffolding that codifies the "wipe → install → snapshot internal state → action → snapshot internal state → assert diff" pattern the operator runs manually today. Each scenario is a self-contained Go `*_test.go` file under `mclaude-e2e/` that owns a real cluster, drives actions through the same paths a human would (helm, kubectl, HTTP, NATS), and asserts on a strongly-typed `ClusterState` snapshot covering Postgres + NATS + Kubernetes + HTTP responses. Tests are gated by `//go:build e2e` so they don't run on every commit — invoked manually via `make e2e` or scheduled (nightly cron + manual workflow_dispatch).

The bug ADR-0098 just caught ("login created 2/4 buckets, not 4/4") is the canonical example of what this catches: a state assertion that wouldn't have made it through CI's existing tests, but is trivially expressible as a snapshot diff.

## Motivation

Today's loop:
1. Manually run wipe-and-deploy.
2. Manually `kubectl get`, `psql`, `curl`, `nats kv list` to inspect state.
3. Notice gaps by eyeball.

That cycle just caught two real bugs (BW_SESSION mis-scoped, ensureUserResources only running 2/4 ensures), but it's:
- **Laborious**: eight commands to verify one happy path.
- **Forgetful**: easy to skip checking a surface that wasn't broken last time.
- **Non-reproducible**: no record of which states were checked when.
- **Single-perspective**: only the operator sees the diffs.

A programmable equivalent runs the same checks — same commands, same assertions — every time, on demand or on a schedule, and surfaces the diffs as test failures. The same surface that caught these bugs by hand becomes a regression net for free.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Scaffolding shape | Go test package `mclaude-e2e/` with `//go:build e2e` tag | Same toolchain as the rest of the project. `go test -tags e2e` runs the suite. Helpers can import the production Go types directly (no schema duplication). |
| Test cadence | Manual + nightly cron + `workflow_dispatch` in CI | Not on every commit (slow + flaky-prone). Nightly catches drift; manual covers local dev iteration. |
| Assertion target | Typed `ClusterState` struct (Postgres rows, NATS streams/KVs/JWTs, k8s pod/Service/Ingress/Certificate state, HTTP probe responses) | One snapshot type means tests can use `cmp.Diff` for full-state comparison or assert on specific fields. Versions cleanly with the codebase. |
| Cluster ownership | Tests attach to current `kubectl config current-context` cluster (k3d for local, ephemeral k3d for CI) | No magic environment provisioning; user runs `make k3d-up` first. CI workflow spins k3d for each suite run. |
| Action vocabulary | Wrappers over the same primitives the operator uses: `cluster.HelmInstall`, `cluster.KubectlDelete`, `cluster.HTTPLogin`, `cluster.NATSPublish` | If a human can do it, the test can do it — same surface. |
| Initial scope | (decision pending: see Open questions) | |
| Investment level (1/2/3 from session conversation) | (decision pending: which level — light Go package, YAML scenarios DSL, or terratest/kuttl) | |

## User Flow

(operator-facing flow, not user-facing — this is a test framework)

1. Operator opens a terminal in the repo.
2. Runs `make e2e` (or `go test -tags e2e ./mclaude-e2e/...`).
3. Scaffolding attaches to the current k3d cluster, runs each scenario:
   a. Calls helpers to set up a known starting state (`cluster.Wipe()`).
   b. Captures `state0 := cluster.Snapshot()`.
   c. Performs an action (`cluster.HelmInstall("mclaude-cp", ...)`).
   d. Captures `state1 := cluster.Snapshot()`.
   e. Asserts on the diff with `s.AssertX(...)` helpers.
4. Test failures print the failing field with full state context (which streams existed, which were missing, etc.).
5. CI nightly runs the same suite against an ephemeral k3d, posts results to a GitHub Actions summary.

## Component Changes

### `mclaude-e2e/` (NEW Go package)

```
mclaude-e2e/
├── cluster/        # cluster handle: helm, kubectl, port-forward, etc.
│   ├── cluster.go          // type Cluster struct{ kubeconfig, ns string }
│   ├── helm.go             // (c *Cluster) HelmInstall(release, chart, values)
│   ├── kubectl.go          // (c *Cluster) KubectlGet, KubectlDelete, etc.
│   └── wipe.go             // (c *Cluster) Wipe() — namespace + PVC + helm releases
├── snapshot/       # ClusterState type + serializer
│   ├── state.go            // type ClusterState struct { Postgres, NATS, K8s, HTTP }
│   ├── postgres.go         // snap from psql query (users, hosts, ...)
│   ├── nats.go             // snap from /jsz monitoring API
│   ├── k8s.go              // snap from kubectl get (pods, certs, secrets, ...)
│   └── http.go             // snap from probe URLs (login, healthz, etc.)
├── assert/         # typed assertions on ClusterState fields
│   ├── postgres.go         // (s *Snapshot) Postgres.AssertUsers(want []User)
│   ├── nats.go             // (s *Snapshot) NATS.AssertStreamsExactly(want []string)
│   └── ...
├── scenarios/      # one scenario per file
│   ├── fresh_install_test.go
│   ├── first_login_per_user_resources_test.go
│   ├── helm_uninstall_preserves_certificate_test.go
│   ├── tls_lefencrypt_issuance_test.go
│   └── ...
└── README.md       # how to run, how to add a scenario
```

**Build tag:** every test file starts with `//go:build e2e` so `go test ./...` (no tag) skips them entirely. CI invokes `go test -tags e2e -timeout 30m ./mclaude-e2e/...`.

### `Makefile` (or root justfile)

Add `make e2e` target that runs `go test -tags e2e ./mclaude-e2e/...` against the current `kubectl` context.

### `.github/workflows/e2e.yml` (NEW)

- Triggers: `workflow_dispatch` (manual) + `schedule: '0 6 * * *'` (06:00 UTC nightly).
- Spins ephemeral k3d, runs deploy-main equivalent of `mclaude-postgres` + `mclaude-cp`, then runs the e2e suite.
- Posts pass/fail summary to the workflow run.
- Does NOT block PRs (no `pull_request` trigger).

## Data Model

The `ClusterState` struct is the contract. Sketch (Go):

```go
type ClusterState struct {
    Timestamp time.Time
    Postgres  PostgresState
    NATS      NATSState
    K8s       K8sState
    HTTP      HTTPState
}

type PostgresState struct {
    Users            []User
    Hosts            []Host
    HostGrants       []HostGrant
    UserHostAgents   []UserHostAgent
    Projects         []Project
    AgentCredentials []AgentCredential
    OAuthConnections []OAuthConnection
}

type NATSState struct {
    Accounts          []string         // mclaude-account, $SYS, $G
    Streams           []NATSStream     // KV_mclaude-hosts, MCLAUDE_SESSIONS_{uslug}, etc.
    KVBuckets         []NATSKVBucket   // KV_mclaude-sessions-{uslug}, etc.
    Consumers         []NATSConsumer
}

type K8sState struct {
    Pods         []Pod
    Services     []Service
    Ingresses    []Ingress
    Certificates []Certificate    // cert-manager.io/v1
    Secrets      []SecretMetadata // names+keys, NOT values
    Jobs         []Job
    HelmReleases []HelmRelease
}

type HTTPState struct {
    Probes []HTTPProbe   // {URL, Status, TLSCert, ResponseHash}
}
```

Snapshots are `cmp.Diff`-compatible. Each substruct has its own `Assert*` helpers so test authors don't reach into internals.

## Error Handling

- **Cluster unreachable**: snapshot fails with a clear "kubectl: connection refused" — test fails fast with the underlying error wrapped.
- **Helm timeout**: `cluster.HelmInstall` times out after a configurable window (default 10m). Test fails with helm's own logs captured.
- **Assertion failure**: `cmp.Diff` output shows exactly which field diverged. State JSON is dumped to `mclaude-e2e/_artifacts/<test>-<timestamp>.json` for post-mortem.
- **Cleanup leak**: each test runs `t.Cleanup(cluster.Wipe)` so tests don't pollute each other's cluster state. Cleanup failures are logged but don't fail the test.

## Security

No new secrets. Tests read from the operator's existing `~/.kube/config` and the existing CI Bitwarden flow. The `mclaude-postgres` Secret is pre-created the same way as in `deploy-main.yml`.

The `HTTPState.Probes` snapshot includes TLS certificate metadata (issuer, dnsNames, expiry) but **NOT** request bodies or response bodies that might contain secrets — careful schema choice here.

## Impact

**No spec changes** — this is testing scaffolding, not a behavior change.

- `docs/spec-doc-layout.md`: add a one-line note pointing at `mclaude-e2e/` for end-to-end scenarios.
- `CLAUDE.md` (project rules): add a note "When the user runs a manual smoke test, consider whether it should be an e2e scenario." (low priority — could defer.)

## Scope

**In v1 (decision pending: which scenarios):**
- Skeleton package (`cluster/`, `snapshot/`, `assert/`).
- 3-5 starter scenarios drawn from the manual workflow this session:
  - Fresh install produces healthy cluster + 1 admin row + LE cert + bucket
  - First login provisions all 4 per-user resources (regression net for ADR-0098)
  - `helm uninstall mclaude-cp` preserves Certificate + TLS Secret (regression net for ADR-0097)
  - Wipe → re-install does not burn LE rate-limit slot
- `Makefile` `e2e` target + GitHub Actions workflow (nightly + manual).

**Deferred:**
- YAML scenario DSL (level 2 in conversation). Defer until the Go-test approach is exercised for ≥2 weeks.
- terratest / kuttl integration. Defer; build the small thing first, see what's missing.
- Snapshot golden-file comparison (commit `ClusterState` JSON to disk and assert equality on changes). Powerful but invites churn from incidental NATS sequence numbers, pod IDs, etc.
- Property-based scenario generation. Way too far ahead.

## Open questions

- **Investment level**: lightweight Go test package (level 1) vs YAML scenarios DSL (level 2) vs adopt terratest/kuttl (level 3). Recommendation in conversation was level 1; user hasn't confirmed.
- **Initial scenarios**: which 3–5 manual checks to codify first. Suggested set above; user may add/remove.
- **CI ephemeral cluster**: spin k3d on a self-hosted runner (current pattern) vs ubuntu-latest with kind. Self-hosted has Tailscale + DO DNS already; kind would need new bootstrap.
- **Snapshot stability**: NATS streams have ever-incrementing sequence numbers; pod UIDs change every restart. Need a redaction layer before `cmp.Diff` to strip non-deterministic fields, or assertions only target deterministic fields.
- **Scenario authoring ergonomics**: should `cluster.HelmInstall` take values as a struct, a YAML string, or `--set` flags? Affects how readable scenarios are.
- **Production-host blast radius**: tests will be destructive (wipe). Need a safety check that aborts if `kubectl current-context` resolves to anything matching `aks-prod` / `production-*` / etc.

## Integration Test Cases

(meta — the framework itself needs tests)

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| `cluster.Snapshot()` round-trips | Snapshot a known state, marshal to JSON, unmarshal, re-snapshot — assert equal | Pre-loaded fixture in k3d | snapshot package |
| `cluster.Wipe()` is idempotent | Wipe twice in a row — second is a no-op, no errors | k3d with prior state | cluster.wipe |
| `assert.NATS.AssertStreamsExactly` rejects extra streams | Pre-load 5 streams, assert only 4 expected, expect failure | k3d with NATS | assert.NATS |
| Production-host safety check | Run `make e2e` against a context named `aks-prod` — must abort before any destructive action | Mock kubeconfig with bad context name | cluster.New |
| Nightly CI run on ephemeral k3d | Workflow triggers on schedule, k3d spins, suite runs, results posted | GH Actions | workflow + suite |

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| `mclaude-e2e/cluster/` | ~250 lines | 50k | helm shell-out wrapper, kubectl wrappers, port-forward helper, wipe helper |
| `mclaude-e2e/snapshot/` | ~400 lines | 80k | typed structs + collectors for each backend (Postgres SQL, NATS /jsz, k8s API, HTTP probes) |
| `mclaude-e2e/assert/` | ~150 lines | 30k | helper assertions on each substate; cmp.Diff harness |
| `mclaude-e2e/scenarios/` (3–5 starters) | ~300 lines | 60k | the actual scenarios, drawn from this session's manual workflow |
| `Makefile` + `.github/workflows/e2e.yml` | ~80 lines | 20k | manual + cron + workflow_dispatch |

**Total estimated tokens:** 240k
**Estimated wall-clock:** 3–4h

---

## Context (this section will be removed before promotion to accepted)

This ADR was authored mid-session while ADR-0098 (per-user resource provisioning fix) is in flight. The conversation that led here:

> Operator: "do you see the way that im testing this one by one? clean cluster state, validate internal state. then do an action, validate internal state. how do i implement this type of testing programmatically?"

A small Go test package owning the cluster as test scaffolding emerged as the recommended shape. The bug surfaced by ADR-0098 (login → 2/4 buckets) is the canonical scenario this framework would prevent regressions on.

The ADR is intentionally `draft` rather than `accepted` — the user has not confirmed the investment level, scenario set, or several other open questions yet. Resume via `/plan-feature --resume e2e-state-snapshot-testing` to continue.
