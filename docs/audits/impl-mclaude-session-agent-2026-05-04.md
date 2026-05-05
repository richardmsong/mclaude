## Run: 2026-05-04T00:00:00Z

Component: mclaude-session-agent
Scope: ADR-0091 entrypoint.sh changes only
Spec files: docs/mclaude-session-agent/spec-session-agent.md (Standalone Mode K8s section), docs/adr-0091-per-user-anthropic-token.md (mclaude-session-agent Component Changes section)

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0091:324-329 | Remove legacy `oauth-token` read: `[ -f "/home/node/.user-secrets/oauth-token" ] && export CLAUDE_CODE_OAUTH_TOKEN=$(cat /home/node/.user-secrets/oauth-token)` | entrypoint.sh (entire file) | IMPLEMENTED | — | File contains no reference to `user-secrets/oauth-token` or legacy read. Confirmed absent. |
| adr-0091:330-334 | Add per-user-tokens read: `export CLAUDE_CODE_OAUTH_TOKEN=$(cat /secrets/tokens/${TOKEN_PROVIDER})`. Fails fast (`set -e`) if missing or empty. | entrypoint.sh:29-46 | IMPLEMENTED | — | Code reads TOKEN_PROVIDER check (line 33-36), constructs token_file="/secrets/tokens/${TOKEN_PROVIDER}" (line 37), checks file existence (line 38-41), exports CLAUDE_CODE_OAUTH_TOKEN (line 42), checks empty (line 43-46). `set -e` is at line 2. All three failure cases (missing provider, missing file, empty file) are explicit. |
| adr-0091:335 | Remove `--nats-creds` flag from the `exec session-agent` call. | entrypoint.sh:91-97 | IMPLEMENTED | — | `exec session-agent` block (lines 91-97) has no `--nats-creds` flag. Flag is only referenced in comment on line 89 explaining its removal. |
| adr-0091:336 | Add `--auth-url "${CONTROL_PLANE_URL}"` to the `exec session-agent` call to activate the challenge-response path. | entrypoint.sh:93 | IMPLEMENTED | — | `--auth-url    "${CONTROL_PLANE_URL}"` is present on line 93. |
| adr-0091:338-347 | Final `exec` is exactly: `exec session-agent --nats-url "${NATS_URL}" --auth-url "${CONTROL_PLANE_URL}" --user-id "${USER_ID}" --project-id "${PROJECT_ID}" --data-dir /data --mode k8s` | entrypoint.sh:91-97 | IMPLEMENTED | — | Code matches spec exactly. Six flags: --nats-url, --auth-url, --user-id, --project-id, --data-dir, --mode. No --nats-creds. |
| adr-0091:349 | No HTTP call for the token. No user-agent dependency at runtime — token is on disk before the agent starts. | entrypoint.sh:29-46 | IMPLEMENTED | — | Token is read from disk via `cat` in a conditional block. No curl/wget/HTTP call present. |
| spec-session-agent.md:21-27 | Anthropic OAuth token sourced exclusively from `/secrets/tokens/${TOKEN_PROVIDER}` (mount of `user-tokens-{uslug}` Secret). `entrypoint.sh` exports as `CLAUDE_CODE_OAUTH_TOKEN`. TOKEN_PROVIDER supplied by user-agent. No HTTP call, no NATS round-trip. If missing or empty, exits with clear error. Legacy `oauth-token` field on `user-secrets` removed; session-agent must not read it. | entrypoint.sh:29-46 | IMPLEMENTED | — | Matches spec in full. |
| spec-session-agent.md:29-39 | `--nats-creds` is NOT passed. `entrypoint.sh` passes `--auth-url "${CONTROL_PLANE_URL}"` to activate self-bootstrap path. Final exec block listed. | entrypoint.sh:88-97 | IMPLEMENTED | — | Comment on line 88-90 explains removal; exec block matches spec exactly. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| entrypoint.sh:1 | INFRA | shebang (`#!/bin/bash`) — required shell interpreter declaration |
| entrypoint.sh:2 | INFRA | `set -e` — fail-fast mode required by spec ("fails fast (`set -e`)") |
| entrypoint.sh:4-11 | INFRA | Nix store bootstrap (seeding /nix from tarball on first boot). Spec'd in spec-session-agent.md Standalone Mode section: "seeds the Nix store from a bootstrap tarball". Outside ADR-0091 scope. |
| entrypoint.sh:13-17 | INFRA | PATH setup for Nix profile dirs and nix.sh sourcing. Supporting infrastructure for Nix bootstrap. Outside ADR-0091 scope. |
| entrypoint.sh:19-27 | INFRA | Consume secrets from user-secrets: id_rsa SSH key and .gitconfig. Spec'd in spec-session-agent.md: "consumes secrets from the mounted `user-secrets` Secret (CLI configs only — no token field)". Outside ADR-0091 scope. |
| entrypoint.sh:29-46 | INFRA | Token read from /secrets/tokens/${TOKEN_PROVIDER}. Fully spec'd (ADR-0091, spec-session-agent.md). Reviewed in Phase 1. |
| entrypoint.sh:48-56 | INFRA | Seed user config from user-config ConfigMap. Spec'd in spec-session-agent.md: "seeds Claude Code config from the `user-config` ConfigMap". Outside ADR-0091 scope. |
| entrypoint.sh:58-60 | INFRA | Symlink JSONL history to PVC. Spec'd in spec-session-agent.md: "symlinks JSONL history to the project PVC". Outside ADR-0091 scope. |
| entrypoint.sh:62-64 | INFRA | Skip onboarding / bypassPermissions. Spec'd in spec-session-agent.md: "disables onboarding and permission dialogs". Outside ADR-0091 scope. |
| entrypoint.sh:66-69 | INFRA | Comment noting git setup is done by Go binary. Supporting comment. Outside ADR-0091 scope. |
| entrypoint.sh:71-81 | INFRA | Shared memory loop symlinking worktree memory dirs. Spec'd in spec-session-agent.md Auto-Memory Sharing section. Outside ADR-0091 scope. |
| entrypoint.sh:83-85 | INFRA | Wait for dockerd socket if DOCKER_ENABLED. Spec'd in spec-session-agent.md Pod Structure section (dockerd-rootless sidecar). Outside ADR-0091 scope. |
| entrypoint.sh:87-97 | INFRA | exec session-agent with flags. Fully spec'd (ADR-0091). Reviewed in Phase 1. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0091:324-329 | Remove legacy oauth-token read | entrypoint_test.go:207-209 (TestEntrypointExecBlock checks `user-secrets/oauth-token` absent) | None found | UNIT_ONLY |
| adr-0091:330-334 | Add /secrets/tokens/${TOKEN_PROVIDER} read, fail fast if missing/empty | entrypoint_test.go:58-175 (TestEntrypointTokenMissingProvider, TestEntrypointTokenMissingFile, TestEntrypointTokenEmptyFile, TestEntrypointTokenValidFile, TestEntrypointTokenWhitespaceOnlyFile) | None found | UNIT_ONLY |
| adr-0091:335 | Remove --nats-creds from exec | entrypoint_test.go:192-206 (TestEntrypointExecBlock checks --nats-creds absent from non-comment lines) | None found | UNIT_ONLY |
| adr-0091:336 | Add --auth-url "${CONTROL_PLANE_URL}" | entrypoint_test.go:188-191 and 221-225 (TestEntrypointExecBlock checks --auth-url and CONTROL_PLANE_URL present) | None found | UNIT_ONLY |
| adr-0091:338-347 | Final exec block exact shape | entrypoint_test.go:181-226 (TestEntrypointExecBlock static analysis of script) | None found | UNIT_ONLY |

### Phase 4 — Bug Triage

No open bugs with Component matching `mclaude-session-agent`.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | — | — | No bugs filed against this component |

### Summary

- Implemented: 7
- Gap: 0
- Partial: 0
- Infra: 13
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 5
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0
