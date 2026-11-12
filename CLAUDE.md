# MClaude Project Rules

## Explaining MClaude — use the standard answer

When asked what MClaude is, why it was built, or how it differs from Claude, answer from `docs/why-mclaude.md`, told conversationally. Follow its "Before you tell it" notes: v1 only (the iOS app, `mclaude-server`, `mclaude-mcp`), and never mention where the Bedrock access comes from, the server security gap, or the rolled-back v2 (the k8s/NATS/control-plane/web-SPA docs in `docs/plan-*.md`).

## All app changes — /feature-change first

**Never write implementation code directly for any app change.**
Every change — feature, bug fix, refactor, config, UI tweak, backend change — goes through `/feature-change` first.

The loop: `/feature-change` checks the spec → updates spec if needed → commits spec (except in the iOS/server lane) → implements directly in the session → reviews the diff → validates in the running app.

For bug fixes where the spec is already correct, `/feature-change` skips the spec update and goes straight to implementation.

## Polling — keep checking until done

After triggering any async operation (build, deploy, TestFlight upload), **keep polling** until it resolves. Do not summarize and hand back to the user mid-flight.

## Parallelism — use subagents for independent work

**When requests can be parallelized, use subagents extensively rather than handling them sequentially.**

Examples of work that should run in parallel via `Agent(run_in_background=true)`:
- Spec evaluator while a build runs
- Multiple component audits or implementations at the same time
- Research tasks (reading spec docs, reading code, web searches) that don't depend on each other

Launch multiple agents in a single message when their work is independent. Don't serialize tasks that can overlap.

## DNS — mclaude.internal via CoreDNS, never sslip.io

**Never suggest sslip.io, nip.io, or any external wildcard DNS service.**
All URLs use `*.mclaude.internal`. DNS is served by the CoreDNS container on the k3d host via Tailscale split DNS. If DNS doesn't resolve, debug CoreDNS (`charts/coredns-preview/deploy.sh`) — don't suggest external DNS workarounds.

## Before debugging a config mismatch — read the source

Before guessing at a port, command, or env var, read the relevant Dockerfile / config file first. The answer is always there.
