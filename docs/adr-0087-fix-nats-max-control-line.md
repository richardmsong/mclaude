# ADR: Increase NATS max_control_line to accommodate multi-host user JWTs

**Status**: accepted
**Status history**:
- 2026-05-03: accepted — Class C spec gap; NATS max_control_line undocumented and unset; default 4096 bytes broken by multi-host JWTs

## Overview

The hub NATS server uses the default `max_control_line` of 4096 bytes. User JWTs that include per-host subjects for multiple cluster grants can exceed this limit — the NATS CONNECT message (which carries the JWT) fails with `maximum control line exceeded`. The fix: explicitly configure `max_control_line: 16384` in the hub NATS server and document it in the spec.

## Motivation

After ADR-0085 (cluster grant in TestMain) and ADR-0086 (no public_key copy in adminGrantCluster), the integration test user is granted k3d-dev access before login. The resulting user JWT includes per-host KV subjects for both the default host and k3d-dev, growing to ~3941 bytes. The NATS CONNECT message (JWT + protocol framing) exceeds the 4096-byte default `max_control_line`, producing:

```
nats: maximum control line exceeded
```

This affects any user with ≥2 cluster grants. The cap is a NATS server-side limit on the length of any single line in the NATS protocol stream (INFO/CONNECT/SUB/etc.). Increasing it to 16384 accommodates users with up to ~15 host grants before hitting the limit again.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| `max_control_line` value | 16384 bytes | NATS recommends 32768 for JWT-heavy deployments; 16384 is conservative but handles 15+ concurrent host grants comfortably — each additional host adds ~240 bytes of subjects to the JWT |
| Expose as `values.yaml` field | Yes — `nats.config.maxControlLine` | Consistent with `maxPayload` pattern; allows per-environment override without chart changes |
| Spec update | Yes — add to Hub NATS config table in `docs/spec-state-schema.md` | `max_control_line` is a runtime server constraint that affects JWT issuance strategy; it belongs in the server config spec |
| Leaf-node config | Not needed — `mclaude-worker` chart does not have a hub NATS server | Only the hub NATS (mclaude-cp) serves user connections |

## Component Changes

### `charts/mclaude-cp/templates/nats-configmap.yaml`

Add `max_control_line` directive after `max_payload`:

```yaml
# Max NATS protocol control line: 16KB for large JWTs with multi-host subjects
max_control_line: {{ .Values.nats.config.maxControlLine }}
```

### `charts/mclaude-cp/values.yaml`

Add `maxControlLine` under `nats.config`:

```yaml
nats:
  config:
    maxPayload: "8388608"
    maxFileStoreSize: "10737418240"
    ## Max NATS protocol control line in bytes. Default NATS value is 4096, which
    ## is too small for user JWTs with multiple host grants. 16384 handles ~15 hosts.
    maxControlLine: "16384"
```

## Error Handling

If `max_control_line` is set too low for the deployed user base, users with many host grants will see NATS connection failures. The spec documents the formula: each additional host adds ~240 bytes; 16384 handles ~62 host grants per user (`(16384 - 1000_base_size) / 240 ≈ 64`).

## Security

No security regression. Increasing `max_control_line` allows longer protocol lines — this does not change auth or permissions. The limit still prevents pathological oversized CONNECT messages (e.g. from buggy clients).

## Impact

Spec update: `docs/spec-state-schema.md` §NATS Server Configuration — add `max_control_line: 16384` to the Hub NATS config block.

Components implementing the change:
- `charts/mclaude-cp` (`templates/nats-configmap.yaml`, `values.yaml`)

## Scope

**v1:**
- Add `max_control_line: {{ .Values.nats.config.maxControlLine }}` to `nats-configmap.yaml`
- Add `maxControlLine: "16384"` to `values.yaml` under `nats.config`

## Integration Test Cases

Observable outcome: `TestIntegration_Import_HappyPath` passes — the test user (granted k3d-dev access before login) connects to NATS without `maximum control line exceeded`. Previously returned that error immediately on NATS connect.

## Implementation Plan

| Component | New/changed lines (est.) | Notes |
|-----------|--------------------------|-------|
| `charts/mclaude-cp/templates/nats-configmap.yaml` | 2 | One comment + one directive |
| `charts/mclaude-cp/values.yaml` | 3 | One comment + one key under nats.config |

**Total estimated tokens:** ~4k
