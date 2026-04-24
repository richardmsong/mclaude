<!-- sdd:begin -->
# Project Rules

## Change detected → invoke /feature-change immediately

When the user asks for **any change** — feature, bug fix, refactor, config, UI tweak, backend change — invoke `/feature-change` as your **first action**. Do not analyze the code first. Do not start implementing. Do not explore the codebase. Invoke the skill and let it handle discovery, classification, and implementation.

**Never write implementation code directly.** The master session authors ADRs, updates specs, and orchestrates agents. All code changes go through dev-harness subagents invoked by `/feature-change`.

Heuristic: if the user says "fix", "change", "update", "refactor", "remove", "add X to Y", "make X do Y", or describes any modification to how the system behaves → that's `/feature-change`. Don't ask permission; invoke the skill immediately.

The loop: `/feature-change` reads specs → classifies → authors ADR → updates spec → spec-evaluator verifies spec alignment → commits spec → calls dev-harness → implementation-evaluator verifies code → done.

## New feature detected → invoke /plan-feature immediately

When the user describes anything that looks like a potential **new feature**, jump straight into `/plan-feature` — don't wait for the full picture, don't rely on keeping it in memory.

Planning context is lost when you get compacted or switched out. The ADR on disk is the durable form. Start `/plan-feature` on the first mention, even mid-conversation, even if there are still open questions — drafts are first-class and can be paused, committed, and resumed.

Heuristic: if the user says something like "maybe we should…", "what if…", "could we add…", "I want to…", or describes a capability the app doesn't have yet → that's `/plan-feature`. Don't ask permission; just start the skill and let the Q&A surface the rest.

## Never edit source files directly

The master session authors ADRs, updates specs, and orchestrates agents. It does **not** write production code, tests, config, or templates. All source file changes go through dev-harness subagents invoked by `/feature-change`.

If tests fail, code is missing, or implementation is wrong — invoke dev-harness, don't fix it yourself. If agents are failing (permissions, context limits), fix the agent infrastructure, not the source code.

## Parallelism — use subagents for independent work

When requests can be parallelized, use subagents extensively rather than handling them sequentially.

Launch multiple agents in a single message when their work is independent. Don't serialize tasks that can overlap.
<!-- sdd:end -->

# MClaude Project Rules

## Deployments — CI owns builds, you observe

**Never run `helm upgrade`, `docker build`, or `k3d image import` yourself.**
CI builds images and deploys. Your tools are `kubectl` (read-only observation) and `gh` (triggering + status checks).

**Never use `gh run watch`** — it blocks until timeout. Use `gh run view {id}` (one-shot poll) instead.

**Before triggering a new CI run**, cancel any in-progress runs for the same workflow + branch:
```bash
gh run cancel $(gh run list --workflow=X.yml --branch=Y --status=in_progress --json databaseId -q '.[].databaseId') 2>/dev/null || true
```

**To clean up a Helm release** (broken state or orphaned), use helm uninstall — it knows all resources including cluster-scoped (ClusterRoles, ClusterRoleBindings) and Ingresses that `kubectl delete all` misses:
```bash
helm uninstall "{release}" -n mclaude-system
```
If that fails, use `helm get manifest {release} -n mclaude-system` to get the full resource list first.

## Polling — keep checking until done

After triggering any async operation (CI run, pod rollout), **keep polling** until it resolves. Do not summarize and hand back to the user mid-flight. Check `gh run view` + `kubectl get pods` on every cycle.

## DNS — mclaude.internal via CoreDNS, never sslip.io

**Never suggest sslip.io, nip.io, or any external wildcard DNS service.**
All URLs use `*.mclaude.internal`. DNS is served by the CoreDNS container on the k3d host via Tailscale split DNS. If DNS doesn't resolve, debug CoreDNS (`charts/coredns-preview/deploy.sh`) — don't suggest external DNS workarounds.

## Before debugging a config mismatch — read the source

Before guessing at a port, command, or env var, read the relevant Dockerfile / config file first. The answer is always there.

## Components

Source layout — each directory below has a matching `docs/<component>/` spec folder:

- `mclaude-control-plane/` — Kubernetes reconciler (Go)
- `mclaude-session-agent/` — per-session sidecar (Go)
- `mclaude-cli/` — CLI binary (Go)
- `mclaude-relay/` — WebSocket tunnel, deployed to VM (Go)
- `mclaude-connector/` — local bridge to relay (Go)
- `mclaude-server/` — macOS launchd service (Go)
- `mclaude-web/` — web SPA (TypeScript/React)
- `mclaude-common/` — shared Go types
- `mclaude-mcp/` — MClaude MCP server (Go)
