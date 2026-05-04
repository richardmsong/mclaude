# Round 5 Audit: Code vs Spec Convergence Check

**Date:** 2026-04-29  
**Prior rounds:** 26 + 19 + 6 + 7 = 58 gaps (all fixed or flagged)  
**This round:** 5 gaps found  
**Confidence:** MEDIUM — not clean; one significant structural gap remains

---

## Gaps Found

### Gap 1: JobEntry struct — 13 field mismatches (spec describes ADR-0034 target, code has ADR-0009 schema)

**Location:** `docs/spec-state-schema.md` § mclaude-job-queue vs `mclaude-session-agent/state.go:136-160`

The spec's `JobEntry` section describes the ADR-0034 target schema as if it were current. The Go struct still carries the pre-ADR-0034 (ADR-0009) fields.

**Fields Go has that spec says "removed":**
- `specPath` (json: `specPath`) — spec says "removed (see ADR-0034)"
- `threshold` (json: `threshold`) — spec says replaced by `softThreshold` + `hardHeadroomTokens`
- `prUrl` (json: `prUrl`) — spec says "removed (see ADR-0034)"

**Fields spec lists that Go struct doesn't have:**
- `claudeSessionID`, `prompt`, `title`, `branchSlug`, `resumePrompt`
- `softThreshold`, `hardHeadroomTokens`
- `permPolicy`, `allowedTools`, `pausedVia`

**No skip marker present** — the text says "are removed" and "are introduced" without any "Not yet implemented" or "Known gap" qualifier.

**Fix:** Add a "Known gap" note to the JobEntry section stating: "The Go `JobEntry` struct has not been migrated to ADR-0034 yet. It still carries `specPath`, `threshold`, `prUrl` (ADR-0009 fields). The ADR-0034 additions (`prompt`, `title`, `branchSlug`, `resumePrompt`, `softThreshold`, `hardHeadroomTokens`, `permPolicy`, `allowedTools`, `claudeSessionID`, `pausedVia`) are not yet implemented in the Go struct."

---

### Gap 2: `ClustersKVKey` Go helper is dead code (unflagged)

**Location:** `mclaude-common/pkg/subj/subj.go:155-163`

`subj.ClustersKVKey()` references the removed `mclaude-clusters` KV bucket. Not called from any production code (only in `subj_test.go`). The TypeScript counterpart (`kvKeyUserClusters`) is already flagged as "Dead code" in `spec-web.md`, but the Go function has no corresponding flag in any spec.

**Fix:** Add to `spec-state-schema.md` NATS KV Buckets section (or to a new dead-code section): "**Dead code:** `subj.ClustersKVKey()` in `mclaude-common/pkg/subj/subj.go` references the removed `mclaude-clusters` KV bucket and is not called from production code."

---

### Gap 3: `LEADER_ELECTION_NAMESPACE` is a phantom env var in spec-controller.md

**Location:** `docs/mclaude-controller/spec-controller.md` § Configuration table

The spec lists:
```
| LEADER_ELECTION_NAMESPACE | No | Defaults to mclaude-system. |
```

Neither the Go code (`mclaude-controller-k8s/main.go`) nor any Helm template references this env var. It does not exist.

**Fix:** Either remove the row or add "**Not yet implemented** — neither the Go binary nor the Helm template reads this variable."

---

### Gap 4: Control-plane Helm template injects `LOG_LEVEL` and `HELM_RELEASE_NAME` — not documented

**Location:** `charts/mclaude-cp/templates/control-plane-deployment.yaml` lines 68-69 and 80-81 vs `docs/mclaude-control-plane/spec-control-plane.md` § Environment Variables

The Helm template injects two env vars into the control-plane container that are:
- Not read by the Go binary
- Not listed in the spec-control-plane.md env var table

| Env Var | Helm Template Line | Go Code Reads? | In Spec? |
|---------|-------------------|----------------|----------|
| `LOG_LEVEL` | 68-69 | No | No |
| `HELM_RELEASE_NAME` | 80-81 | No | No |

For reference, the controller-k8s has identical patterns: `LOG_LEVEL` is flagged "not yet read" in spec-controller.md; `HELM_RELEASE_NAME` is documented and read by the controller. The control-plane equivalents are undocumented.

**Fix:** Add both to spec-control-plane.md env var table with "**Not read by Go code** — injected by Helm template but not consumed by the binary."

---

### Gap 5: `HOSTNAME` and `MACHINE_ID` flags in session-agent are dead code (unflagged)

**Location:** `mclaude-session-agent/main.go:63-64`

The daemon mode defines `--hostname` / `HOSTNAME` and `--machine-id` / `MACHINE_ID` flags. Values are stored in `DaemonConfig.Hostname` and `DaemonConfig.MachineID` but **never accessed** after storage (confirmed: zero references to `cfg.Hostname` or `cfg.MachineID` in any Go file).

These are remnants of the removed laptop collision detection flow. The spec correctly documents collision detection as removed ("slug uniqueness is enforced server-side at registration time") but does not flag these flags/env vars as dead code.

**Fix:** Add to spec-session-agent.md § Configuration: "**Dead code:** `--hostname` / `HOSTNAME` and `--machine-id` / `MACHINE_ID` flags are remnants of the removed collision-detection flow and are not consumed by any production code path."

---

## Borderline Items Considered and Excluded

| Item | Reason for Exclusion |
|------|---------------------|
| Missing `UserHostProjectAPISessionsRestart` Go helper in `subj.go` (SPA has `subjSessionsRestart()`) | No functional impact — session-agent dispatches restart by parsing the subject suffix on its wildcard consumer. Symmetry gap only. |
| `mclaude-heartbeats` and `mclaude-laptops` bucket names in `testutil/deps.go:81-82` and `integration_test.go:37-38` | Test infrastructure, not production code. May be needed for backward-compatibility integration tests. |
| `HTTP_PORT` vs `PORT` mismatch | Already flagged as "Known mismatch" in spec-helm.md. |
| `METRICS_PORT` env var in control-plane | Already flagged as "Known issue" in spec-helm.md. |
| `JWT_REFRESH_THRESHOLD_SECONDS` phantom env var | Already flagged as "Not read by Go code" in spec-helm.md. |
| `LEADER_ELECTION` in controller | Already flagged as "not yet read by the controller binary" in spec-controller.md. |
| `LOG_LEVEL` in controller | Already flagged as "not yet read by the controller binary" in spec-controller.md. |
| `NATS_CREDENTIALS_PATH` in controller | Already flagged as "not read by the controller binary" in spec-controller.md. |
| `JS_DOMAIN` in controller | Already flagged as "not yet read by the controller binary" in spec-controller.md. |
| `subjClusterProvision` / `subjClusterStatus` / `kvKeyUserClusters` in SPA | Already flagged as "Dead code" in spec-web.md. |
| `readyz` returning 200 unconditionally | Already flagged as "Known bug" in spec-control-plane.md. |
| `MCLAUDE_LIFECYCLE` stream not created by agent | Already flagged as "known gap" in spec-state-schema.md. |
| `CLAUDE_CODE_TMPDIR` not yet injected | Already flagged as "not yet injected" in spec-state-schema.md. |
| Login response flat vs nested shape | Already flagged as "Not yet implemented" in spec-state-schema.md. |
| Host JWT UUID-vs-slug bugs | Already flagged as "Known bug" in spec-control-plane.md. |
| Project KV key UUID format (not migrated to slugs) | Already flagged as "deferred per ADR-0050" in spec-state-schema.md. |
