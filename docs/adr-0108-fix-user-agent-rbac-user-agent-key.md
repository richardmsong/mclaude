# ADR: Fix User-Agent RBAC — Add user-agent-key Secret Access

**Status**: accepted
**Status history**:
- 2026-05-07: accepted

## Overview

The `mclaude-user-agent` Role created by the host controller in each user namespace grants Secret access only to `user-tokens-{uslug}`. The user-agent also needs `get`, `update`, and `patch` on `user-agent-key` — the Secret that holds the NKey seed (controller-written) and the X25519 private key (user-agent-written on first startup). Without this access the user-agent fatals immediately on every start, preventing JWT issuance and blocking all host-scoped operations for the user.

## Motivation

On startup the user-agent calls:

```
get secret user-agent-key → read nkey-seed (for NATS challenge-response)
                           → read x25519-private (if present)
patch/update secret user-agent-key → write x25519-private (generated on first start)
```

The pod is deployed with `user-agent-key` mounted at `/secrets/agent/` (per spec). But the Role only permits `user-tokens-{uslug}` in `resourceNames` for the Secrets resource. The mount resolves (volume mounts are handled by the kubelet using the node's credentials, not the pod's ServiceAccount) but the in-process `client-go` calls to read/write `user-agent-key` via the RBAC-constrained ServiceAccount token fail with 403.

The result is:

```
"load/generate X25519 keypair: get secret user-agent-key: secrets \"user-agent-key\" is forbidden"
```

ADR-0091 specified the Role as granting "Secret (`user-tokens-{uslug}` only)" — omitting `user-agent-key` was an oversight. The user-agent spec (`mclaude-user-agent/spec-user-agent.md`) describes reading and writing `user-agent-key` on startup but the controller spec never extended the Role to allow it.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Verbs on `user-agent-key` | `get`, `update`, `patch` | Read on every start; write only on first start (generate + store X25519 private key). No `create` or `delete` — controller creates the Secret; user-agent only patches existing fields |
| ResourceNames scope | `user-agent-key` (exact name) | Matches the existing pattern for `user-tokens-{uslug}` — resourceNames-scoped rules, not a broad Secret wildcard |
| Role change location | `mclaude-controller-k8s` namespace provisioning code | Controller builds the Role when it processes `grants.create`; same place the `user-tokens-{uslug}` rule is constructed |
| Existing Role remediation | Controller is idempotent on `grants.create` re-delivery; a re-published grant re-ensures the Role. For already-provisioned namespaces (like the current broken one), patch the existing Role | Only the rule addition is needed — no namespace teardown |

## User Flow

No user-visible change. After the fix, the user-agent starts successfully, performs challenge-response with CP, registers its X25519 pubkey, and subscribes to its NATS scope. The SPA can then deliver the wrapped Anthropic token to the host.

## Component Changes

### mclaude-controller-k8s

**File(s) modified:** wherever the `mclaude-user-agent` Role rules are built (the function that constructs the PolicyRules for the user-agent Role in the user namespace provisioning flow).

**Change:** Add a second Secrets rule alongside the existing `user-tokens-{uslug}` rule:

```yaml
- apiGroups: [""]
  resources: ["secrets"]
  resourceNames: ["user-agent-key"]
  verbs: ["get", "update", "patch"]
```

The existing `user-tokens-{uslug}` rule is unchanged.

## Data Model

No schema changes.

## Error Handling

No change. After the fix, the user-agent no longer fatals on startup. If the Secret is absent for any reason, the existing error path ("load NKey seed: read file: no such file") still applies.

## Security

`user-agent-key` contains the NKey seed (used for NATS authentication with CP) and the X25519 private key (used to unseal token_wrapped from the SPA). Restricting access to `resourceNames: [user-agent-key]` ensures the user-agent can only read/modify its own credential Secret — it cannot enumerate or access other Secrets in the namespace.

## Impact

**Specs updated in this commit:**
- `docs/mclaude-controller-k8s/spec-k8s-architecture.md` — add `user-agent-key` to the `mclaude-user-agent` Role's allowed secrets (step 3 of the provisioning flow).

**Components implementing the change:**
- `mclaude-controller-k8s`

## Scope

**In scope:**
- Add `user-agent-key` rule to `mclaude-user-agent` Role in controller provisioning code
- Update `docs/mclaude-controller-k8s/spec-k8s-architecture.md`

**Explicitly deferred / out of scope:**
- Fan-out RBAC patch to all existing user namespaces — operators re-trigger by re-publishing `grants.create` per ADR-0091 provisioning pattern; the current broken namespace is patched operationally

## Integration Test Cases

No integration tests — change is a Kubernetes RBAC rule addition. The fix is validated by user-agent starting successfully and registering its X25519 pubkey with CP (`user_host_agents.x25519_pub` non-null after startup).

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| mclaude-controller-k8s | ~5 lines added | ~20k | One additional PolicyRule in the Role builder |

**Total estimated tokens:** ~20k
**Estimated wall-clock:** 0.05h
