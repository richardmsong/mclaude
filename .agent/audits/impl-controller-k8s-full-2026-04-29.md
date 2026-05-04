# Full Implementation Audit: mclaude-controller-k8s

**Date:** 2026-04-29
**Scope:** All production code in `mclaude-controller-k8s/` vs authoritative specs
**Specs consulted:**
- `docs/mclaude-controller/spec-controller.md` (primary controller spec)
- `docs/spec-state-schema.md` (Kubernetes Resources section, NATS subjects, CRD)
- `docs/adr-0040-fix-controller-k8s-boot.md` (boot fix — implemented)
- `docs/adr-0014-controller-separation.md` (superseded by ADR-0035 — historical only)

**Code files reviewed:**
- `mclaude-controller-k8s/main.go`
- `mclaude-controller-k8s/reconciler.go`
- `mclaude-controller-k8s/nats_subscriber.go`
- `mclaude-controller-k8s/mcproject_types.go`
- `mclaude-controller-k8s/nkeys.go`

---

## Summary

| Severity | Count |
|----------|-------|
| MISSING  | 5     |
| PARTIAL  | 3     |
| MISMATCH | 2     |

---

## Gap 1: Provision reply does not wait for Ready phase

**Severity: MISSING**

**Spec says** (spec-controller.md § NATS subscriptions):
> `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.provision` — Request/reply. Resolves userSlug, hostSlug, projectSlug from the subject + payload; creates the MCProject CR; **returns success when reconcile reaches Ready** (or 503-style failure).

**Code does:**
`nats_subscriber.go` `handleCreate()` creates the MCProject CR and immediately replies `{ok: true}` without waiting for the reconciler to reach `Ready` phase:
```go
if err := p.k8sClient.Create(ctx, mcp); err != nil {
    if k8serrors.IsAlreadyExists(err) {
        return nil
    }
    return fmt.Errorf("create MCProject CR: %w", err)
}
// ... immediately returns nil → replyOK
```

The control-plane uses a 10-second PROVISION_TIMEOUT for the NATS request/reply. The code should poll the MCProject CR status or use a watch to wait for `Phase == Ready` before replying, or reply failure if the phase reaches `Failed` or the timeout expires.

---

## Gap 2: Controller JWT has no permission scopes

**Severity: MISMATCH**

**Spec says** (spec-controller.md § Authentication, spec-state-schema.md § Per-cluster leaf / controller JWT permissions):
> Per-cluster leaf / controller JWT permissions:
> ```
> publish:   mclaude.users.*.hosts.{cluster-slug}.>, _INBOX.>, $JS.*.API.>, $SYS.ACCOUNT.*.CONNECT, $SYS.ACCOUNT.*.DISCONNECT
> subscribe: mclaude.users.*.hosts.{cluster-slug}.>, _INBOX.>, $JS.*.API.>
> ```

**Code does:**
`main.go` `generateNATSUserCreds()` creates the controller JWT with **no explicit permission scopes**:
```go
claims := natsjwt.NewUserClaims(userPub)
claims.Name = "controller-k8s"
claims.IssuerAccount, _ = accountKP.PublicKey()
jwt, err := claims.Encode(accountKP)
```
No `claims.Permissions.Pub.Allow` or `claims.Permissions.Sub.Allow` is set. The JWT has unrestricted account-level permissions. A compromised controller could publish/subscribe to any subject in the account, not just its own cluster's scope.

---

## Gap 3: `Pending` phase never set

**Severity: MISMATCH**

**Spec says** (spec-controller.md § Reconciler loop, spec-state-schema.md § CRD):
> Updates MCProject status: phase (`Pending → Provisioning → Ready` or `Failed`)

**Code does:**
`mcproject_types.go` defines `PhasePending = "Pending"` as a constant, but it is never used in any code path. In `reconciler.go`, when a new MCProject CR is created its `Status.Phase` is `""` (empty string). The reconciler checks:
```go
if mcp.Status.Phase == "" {
    if err := r.setPhase(ctx, &mcp, PhaseProvisioning); err != nil { ... }
}
```
It skips `Pending` and goes directly from empty to `Provisioning`. Any consumer watching for the `Pending` → `Provisioning` transition (e.g. control-plane or SPA) would never see it.

---

## Gap 4: CLAUDE_CODE_TMPDIR env var not injected

**Severity: MISSING**

**Spec says** (spec-state-schema.md § Deployment):
> `CLAUDE_CODE_TMPDIR`: specified in the spec target but **not yet injected** by the reconciler's `buildPodTemplate()`. When implemented, should be set to `/data/claude-tmp` (PVC subPath on the `project-data` volume) so shell output files persist across pod restarts.

**Code does:**
`reconciler.go` `buildPodTemplate()` does not include `CLAUDE_CODE_TMPDIR` in the env vars and does not mount a subPath for `/data/claude-tmp`.

**Note:** The spec acknowledges this as a known gap ("not yet injected"), but it remains unimplemented.

---

## Gap 5: NATS `update` handler not implemented

**Severity: MISSING**

**Spec says** (spec-controller.md § NATS subscriptions):
> `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.update` — Request/reply. Reconciles per-user user-secrets Secret (NATS creds, OAuth tokens, CLI configs) and re-applies the pod template. **Not yet implemented** — the NATS handler dispatches create, provision, and delete but has no handler for update.

**Code does:**
`nats_subscriber.go` `handleProvisionRequest()` switch statement handles `create`, `provision`, and `delete`. The `update` operation falls through to the `default` case which replies with `{ok: false, error: "unknown operation: update", code: "unknown_operation"}`.

While the spec documents this as "not yet implemented", the code actively rejects `update` with an error code `unknown_operation` rather than a more informative `not_implemented` code. Control-plane sending an update request gets an error reply that looks like a bug rather than a planned deferral.

**Note:** The spec itself marks this as a known gap.

---

## Gap 6: Leader election not configured

**Severity: MISSING**

**Spec says** (spec-controller.md § Deployment):
> Leader election enabled — a future HA scale-out is supported by configuration; v1 runs a single replica.

And (spec-controller.md § Configuration):
> `LEADER_ELECTION`: Injected by Helm as `"true"` but **not yet read by the controller binary**. Leader election is not currently configured in `ctrl.Options`.

**Code does:**
`main.go` creates the manager without leader election:
```go
mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
    Scheme:                 scheme,
    Metrics:                metricsserver.Options{BindAddress: envOr("METRICS_ADDR", ":8082")},
    HealthProbeBindAddress: envOr("HEALTH_PROBE_ADDR", ":8081"),
})
```
No `LeaderElection: true` or `LeaderElectionID` is set. Running multiple replicas would cause conflicting reconciles.

**Note:** The spec itself documents this as a known gap ("not yet read by the controller binary").

---

## Gap 7: Shared RBAC resources have single-owner controller reference

**Severity: PARTIAL**

**Spec says** (spec-state-schema.md § RBAC):
> ServiceAccount: `mclaude-sa`, Role: `mclaude-role`, RoleBinding: `mclaude-role` — per-user namespace resources.

And (spec-controller.md § Reconciler loop):
> Ensures RBAC resources (ServiceAccount, Role, RoleBinding).

**Code does:**
`reconciler.go` `reconcileRBAC()` calls `ensureOwned()` which sets `SetControllerReference(mcp, obj, ...)` making the **first** MCProject to create these resources the sole Kubernetes owner. The `ensureOwned` function returns immediately if the resource already exists, without adding additional owner references:
```go
if err := r.client.Get(ctx, key, obj); err == nil {
    return nil // Already exists — don't add another owner
}
```

**Impact:** If User A has two projects (MCProject-A and MCProject-B), and MCProject-A was created first (owning the ServiceAccount, Role, RoleBinding), deleting MCProject-A triggers Kubernetes garbage collection of the shared RBAC resources. MCProject-B's session-agent pod loses its ServiceAccount, breaking its NATS auth mount and RBAC permissions.

The same issue applies to the `user-config` ConfigMap (also created via `ensureOwned`).

---

## Gap 8: `user-secrets` Secret not owned — won't cascade on last project delete

**Severity: PARTIAL**

**Spec says** (spec-controller.md § Reconciler loop, implied):
> On delete: Tears down the MCProject CR (and cascaded namespace/RBAC/PVCs/Deployment).

**Code does:**
`reconciler.go` `reconcileSecrets()` creates/updates the `user-secrets` Secret **without** `SetControllerReference`. The Secret has no owner reference, so:
1. It will **never** be garbage collected when MCProject CRs are deleted.
2. Orphaned `user-secrets` Secrets accumulate when all projects for a user are deleted.

This is arguably correct behavior for a shared resource (multiple projects share the same user-secrets), but it means cleanup of per-user resources on full project teardown requires manual intervention or a separate garbage-collection mechanism that the spec doesn't describe.

---

## Gap 9: LOG_LEVEL and LEADER_ELECTION_NAMESPACE env vars not read

**Severity: MISSING**

**Spec says** (spec-controller.md § Configuration):
> `LOG_LEVEL`: Injected by Helm but **not yet read by the controller binary**. Zerolog uses its default level.
> `LEADER_ELECTION_NAMESPACE`: Defaults to mclaude-system. **Not yet implemented** — neither the Go binary nor the Helm template reads this variable.

**Code does:**
Neither `LOG_LEVEL` nor `LEADER_ELECTION_NAMESPACE` is read. Zerolog defaults are used; leader election namespace is irrelevant since leader election itself is not enabled (Gap 6).

**Note:** The spec itself documents these as known gaps.

---

## Gap 10: Session-agent JWT `IssuerAccount` set but spec says controller doesn't mint JWTs

**Severity: PARTIAL**

**Spec says** (spec-controller.md § Reconciler loop step 4):
> Ensures the user-config ConfigMap and user-secrets Secret. NATS credentials in the Secret are the **per-host JWT issued by control-plane** (the controller does not mint JWTs).

**But spec-state-schema.md says** (§ Per-session-agent JWT permissions):
> Issued by `mclaude-controller-k8s` (`IssueSessionAgentJWT(userID, userSlug, accountKP)`), signed by the account signing key.

**Code does:**
`reconciler.go` `reconcileSecrets()` calls `IssueSessionAgentJWT()` in `nkeys.go`, which mints session-agent JWTs signed by the account key. This matches `spec-state-schema.md` but contradicts `spec-controller.md`.

This is a **spec self-contradiction**, not a code bug. The code correctly mints session-agent JWTs. The controller spec's statement "the controller does not mint JWTs" is outdated and should be corrected.

---

## Areas with Full Compliance (No Gaps)

The following areas were audited and found compliant with the spec:

| Area | Verdict | Notes |
|------|---------|-------|
| CRD group/version/kind | ✅ PASS | `mcprojects.mclaude.io/v1alpha1` |
| CRD fields (spec + status) | ✅ PASS | All 6 spec fields, 4 status fields, 4 conditions |
| CR naming convention | ✅ PASS | `{userSlug}-{projectSlug}` |
| NATS subscription subject | ✅ PASS | `mclaude.users.*.hosts.{clusterSlug}.api.projects.>` |
| NATS JWT auth (ADR-0040) | ✅ PASS | `generateNATSUserCreds` using account key |
| Reconciler step order | ✅ PASS | namespace → RBAC → secrets → imagePullSecrets → PVCs → deployment → status |
| Namespace naming + labels | ✅ PASS | `mclaude-{userId}` with `mclaude.io/user-id`, `mclaude.io/managed` |
| RBAC names + rules | ✅ PASS | `mclaude-sa`, `mclaude-role` with correct verbs |
| PVC names + config | ✅ PASS | `project-{projectId}`, `nix-{projectId}`, sizes from ConfigMap |
| Deployment name | ✅ PASS | `project-{projectId}` |
| Deployment strategy | ✅ PASS | `Recreate` per ADR-0043 |
| Deployment replicas | ✅ PASS | 1 |
| Pod env vars | ✅ PASS | USER_ID, PROJECT_ID, NATS_URL, USER_SLUG, HOST_SLUG, PROJECT_SLUG |
| HOST_SLUG sourcing | ✅ PASS | From `CLUSTER_SLUG` env (functionally correct per spec) |
| Pod volumes + mounts | ✅ PASS | project-data, nix-store, claude-home, user-config, user-secrets — all correct paths |
| ImagePullSecrets copy | ✅ PASS | Copies from controller namespace to user namespace |
| ConfigMap watch + re-enqueue | ✅ PASS | `SetupWithManager` watches template CM, re-enqueues all MCProjects |
| Corporate CA support | ✅ PASS | Label, volume, mount, env var, annotation hash — all implemented |
| Session-agent JWT permissions | ✅ PASS | All 12 pub/sub permission entries match spec-state-schema |
| Delete idempotency | ✅ PASS | NotFound → ok:true |
| ProvisionRequest/Reply shape | ✅ PASS | All fields present |
| CLUSTER_SLUG required | ✅ PASS | Fatal on empty |
| NATS retry config | ✅ PASS | `RetryOnFailedConnect(true)`, `MaxReconnects(-1)` |
| Namespace detection | ✅ PASS | Reads SA mount, falls back to `mclaude-system` |
| DEV_OAUTH_TOKEN injection | ✅ PASS | Injected into `user-secrets` when set |
| SESSION_AGENT_NATS_URL | ✅ PASS | Env override or FQDN derivation |
