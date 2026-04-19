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

## All app changes — /feature-change first

**Never write implementation code directly for any app change.**
Every change — feature, bug fix, refactor, config, UI tweak, backend change — goes through `/feature-change` first.

The loop: `/feature-change` checks the spec → updates spec if needed → commits spec → calls `/dev-harness <component>` → implements and tests.

For bug fixes where the spec is already correct, `/feature-change` skips the spec update and goes straight to `/dev-harness`.

## New feature detected → invoke /plan-feature immediately

**When the user describes anything that looks like a potential new feature, jump straight into `/plan-feature` — don't wait for the full picture, don't rely on keeping it in memory.**

Planning context is lost when you get compacted or switched out. The ADR on disk is the durable form. Start `/plan-feature` on the first mention, even mid-conversation, even if there are still open questions — drafts are first-class (see `docs/adr-2026-04-19-adr-status-lifecycle.md`) and can be paused, committed, and resumed.

Heuristic: if the user says something like "maybe we should…", "what if…", "could we add…", "I want to…", or describes a capability the app doesn't have yet → that's `/plan-feature`. Don't ask permission; just start the skill and let the Q&A surface the rest.

## Polling — keep checking until done

After triggering any async operation (CI run, pod rollout), **keep polling** until it resolves. Do not summarize and hand back to the user mid-flight. Check `gh run view` + `kubectl get pods` on every cycle.

## Parallelism — use subagents for independent work

**When requests can be parallelized, use subagents extensively rather than handling them sequentially.**

Examples of work that should run in parallel via `Agent(run_in_background=true)`:
- Spec evaluator + CI polling while a build runs
- Multiple component audits or implementations at the same time
- Research tasks (reading spec docs, reading code, web searches) that don't depend on each other

Launch multiple agents in a single message when their work is independent. Don't serialize tasks that can overlap.

## DNS — mclaude.internal via CoreDNS, never sslip.io

**Never suggest sslip.io, nip.io, or any external wildcard DNS service.**
All URLs use `*.mclaude.internal`. DNS is served by the CoreDNS container on the k3d host via Tailscale split DNS. If DNS doesn't resolve, debug CoreDNS (`charts/coredns-preview/deploy.sh`) — don't suggest external DNS workarounds.

## Before debugging a config mismatch — read the source

Before guessing at a port, command, or env var, read the relevant Dockerfile / config file first. The answer is always there.
