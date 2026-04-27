# ADR: Fix mclaude-worker Helm Chart Bootstrap

**Status**: implemented
**Status history**:
- 2026-04-27: accepted
- 2026-04-27: implemented — worker NATS Running with leaf-node auth, trust chain from hub

## Overview

Fix the mclaude-worker chart so it boots in the single-cluster degenerate install alongside mclaude-cp. Three issues prevent the worker NATS pod from starting.

## Motivation

After ADR-0035/0037/0038 landed and mclaude-cp is fully running, the worker chart (deployed by the same CI workflow) fails:

1. **Worker NATS** — `include /etc/nats/trust/accountPublicKey.conf` uses an absolute path that NATS doubles to `/etc/nats/etc/nats/trust/accountPublicKey.conf`. Same bug fixed in mclaude-cp by ADR-0038 (relative path).
2. **Worker trust Secret** — `accountPublicKey.conf` key has wrong format (missing quotes around account public key, no system account JWT). The key should be `resolverPreload` and contain both the system account and application account entries.
3. **Missing trust chain values** — In single-cluster degenerate install, the deploy workflow passes no `trustChain.operatorJwt` or `trustChain.accountJwt`. The worker chart needs these from the hub's `operator-keys` Secret. The simplest fix: the deploy workflow reads the operator-keys Secret and passes the values to `helm upgrade --set`.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Worker NATS include path | Change to relative `trust/resolverPreload` | Same fix as ADR-0038 for the CP chart — NATS resolves includes relative to the config file directory. |
| Worker trust Secret key | Rename `accountPublicKey.conf` to `resolverPreload`. Format as `"<sysPub>": <sysJWT>\n"<acctPub>": <acctJWT>`. Add `trustChain.sysAccountPublicKey` and `trustChain.sysAccountJwt` values. | NATS resolver_preload needs both system and application account JWTs with quoted public keys. |
| Deploy workflow trust chain | In `deploy-main.yml`, read `operatorJwt`, `accountJwt`, `accountPublicKey` from the `operator-keys` Secret via kubectl and pass them as `--set` to the worker helm install. Also read the system account fields. | The single-cluster install shares the hub's trust chain. The operator-keys Secret is already created by the CP's init-keys Job before the worker deploys. |
| Leaf credentials | For single-cluster degenerate install, the worker leaf node doesn't need JWT auth since it connects to the in-cluster hub NATS. But the leaf-creds Secret still needs a valid `.creds` file. For now, generate a user JWT from the account key and format it as a NATS creds file in the deploy workflow. | Without valid leaf creds, the leaf-node connection fails. In production multi-cluster, `mclaude cluster register` would produce these. |

## Impact

No specs updated — bug fix restoring compliance with spec-helm.md's worker chart description.

**Components:**
- `charts/mclaude-worker/templates/nats-configmap.yaml` — fix include path
- `charts/mclaude-worker/templates/nats-trust-secret.yaml` — fix resolver_preload format, add system account
- `.github/workflows/deploy-main.yml` — pass trust chain from operator-keys Secret to worker helm install
- `charts/mclaude-worker/values-k3d-ghcr.yaml` — no changes needed (values come from --set)

## Scope

Bug fix for single-cluster degenerate install only. Multi-cluster trust chain distribution via `mclaude cluster register` is a separate concern.
