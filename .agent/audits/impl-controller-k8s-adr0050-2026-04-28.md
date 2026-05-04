## Run: 2026-04-28T00:00:00Z

Component: mclaude-controller-k8s
ADR: docs/adr-0050-fix-session-creation-pipeline.md (status: accepted)
Specs: docs/spec-state-schema.md, docs/mclaude-controller/spec-controller.md

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| ADR-0050 Decision 2 | `ProvisionRequest` gains `UserID` and `ProjectID` string fields alongside existing `UserSlug`, `HostSlug`, `ProjectSlug`. HTTP handler populates all five. | nats_subscriber.go:18-27 | IMPLEMENTED | — | Struct has `UserID`, `ProjectID`, `UserSlug`, `HostSlug`, `ProjectSlug`, `GitURL`, `GitIdentityID`. Matches spec-state-schema NATS subjects table payload `{userID, userSlug, hostSlug, projectID, projectSlug, gitUrl, gitIdentityId}`. |
| ADR-0050 Decision 3 | Add `UserSlug` and `ProjectSlug` string fields to `MCProjectSpec`. Controller's `handleCreate` populates from `ProvisionRequest`. | mcproject_types.go:37-48, nats_subscriber.go:99-108 | IMPLEMENTED | — | `MCProjectSpec` has `UserSlug`, `ProjectSlug`. `handleCreate` sets `Spec.UserSlug = req.UserSlug`, `Spec.ProjectSlug = req.ProjectSlug`. |
| ADR-0050 Decision 4 | `USER_ID = Spec.UserID` | reconciler.go:289 | IMPLEMENTED | — | `{Name: "USER_ID", Value: userID}` where `userID = mcp.Spec.UserID` (line 283). |
| ADR-0050 Decision 4 | `PROJECT_ID = Spec.ProjectID` | reconciler.go:290 | IMPLEMENTED | — | `{Name: "PROJECT_ID", Value: projectID}` where `projectID = mcp.Spec.ProjectID` (line 282). |
| ADR-0050 Decision 4 | `USER_SLUG = Spec.UserSlug` | reconciler.go:298 | IMPLEMENTED | — | `{Name: "USER_SLUG", Value: mcp.Spec.UserSlug}`. |
| ADR-0050 Decision 4 | `PROJECT_SLUG = Spec.ProjectSlug` | reconciler.go:300 | IMPLEMENTED | — | `{Name: "PROJECT_SLUG", Value: mcp.Spec.ProjectSlug}`. |
| ADR-0050 Decision 4 | `HOST_SLUG = tpl.hostSlug` (cluster slug, unchanged) | reconciler.go:299 | GAP | CODE→FIX | `{Name: "HOST_SLUG", Value: tpl.hostSlug}` — BUT `tpl.hostSlug` is **always empty string**. The `sessionAgentTpl.hostSlug` field (reconciler.go:609) is never populated: `loadTemplate()` doesn't read it from ConfigMap, `defaultTemplate()` doesn't set it, and the reconciler struct has no `clusterSlug` field. The Helm session-agent-template ConfigMap also doesn't include a `hostSlug` key. Session-agent pods get `HOST_SLUG=""`. |
| ADR-0050 Decision 4 | Remove TODO comments | reconciler.go (entire file) | IMPLEMENTED | — | No TODO comments remain in reconciler.go related to slug resolution. |
| ADR-0050 Decision 5 | `IssueSessionAgentJWT` gains `userSlug string` param | nkeys.go:26 | IMPLEMENTED | — | Signature: `IssueSessionAgentJWT(userID, userSlug string, accountKP nkeys.KeyPair)`. |
| ADR-0050 Decision 5 | `SessionAgentSubjectPermissions(userID, userSlug)` returns PubAllow = `[mclaude.{UUID}.>, mclaude.users.{uslug}.hosts.*.>, _INBOX.>, $JS.*.API.>]`, SubAllow = same | nkeys.go:10-18 | IMPLEMENTED | — | Returns exactly these four patterns for both pub and sub. |
| ADR-0050 Decision 5 | Session-agent JWT signed by account key | nkeys.go:41 | IMPLEMENTED | — | `claims.Encode(accountKP)` signs with the account key pair. |
| spec-state-schema MCProject CRD | `spec.userId: string (required — UUID v4)` | mcproject_types.go:39 | IMPLEMENTED | — | `UserID string json:"userId"`. |
| spec-state-schema MCProject CRD | `spec.projectId: string (required — UUID v4)` | mcproject_types.go:41 | IMPLEMENTED | — | `ProjectID string json:"projectId"`. |
| spec-state-schema MCProject CRD | `spec.userSlug: string (required — typed slug, ADR-0050)` | mcproject_types.go:43 | IMPLEMENTED | — | `UserSlug string json:"userSlug"`. |
| spec-state-schema MCProject CRD | `spec.projectSlug: string (required — typed slug, ADR-0050)` | mcproject_types.go:45 | IMPLEMENTED | — | `ProjectSlug string json:"projectSlug"`. |
| spec-state-schema MCProject CRD | `spec.gitUrl: string (optional)` | mcproject_types.go:47 | IMPLEMENTED | — | `GitURL string json:"gitUrl,omitempty"`. |
| spec-state-schema MCProject CRD | `spec.gitIdentityId: string (optional)` | mcproject_types.go:48 | IMPLEMENTED | — | `GitIdentityID string json:"gitIdentityId,omitempty"`. |
| spec-state-schema MCProject CRD | `status.phase: Pending | Provisioning | Ready | Failed` | mcproject_types.go:58-63 | IMPLEMENTED | — | All four phases defined as constants. |
| spec-state-schema MCProject CRD | `status.conditions` with type/status/reason/message/lastTransitionTime | mcproject_types.go:69, reconciler.go:460-472 | IMPLEMENTED | — | Uses `metav1.Condition` with `meta.SetStatusCondition`. |
| spec-state-schema MCProject CRD | `status.lastReconciledAt: date-time` | mcproject_types.go:71, reconciler.go:164 | IMPLEMENTED | — | Set on successful reconcile. |
| spec-state-schema MCProject CRD | `status.userNamespace: string` | mcproject_types.go:70, reconciler.go:165 | IMPLEMENTED | — | Set to `mclaude-{userId}`. |
| spec-state-schema MCProject CRD | `Name: {projectId}` | nats_subscriber.go:96 | GAP | SPEC→FIX | Code uses `crName := fmt.Sprintf("%s-%s", req.UserSlug, req.ProjectSlug)` — human-readable slug-based naming. The spec says `{projectId}` (UUID). The slug-based name is more practical for `kubectl` debugging and aligns with ADR-0050's slug-oriented approach. Spec should be updated to reflect the actual naming convention. |
| spec-state-schema MCProject CRD | `Scope: Namespaced (in mclaude-system)` | nats_subscriber.go:102 | IMPLEMENTED | — | `Namespace: p.controlPlaneNs` which defaults to `mclaude-system`. |
| spec-state-schema Namespace | `mclaude-{userId}` with labels `mclaude.io/user-id={userId}`, `mclaude.io/managed=true` | reconciler.go:179-199 | IMPLEMENTED | — | Namespace created/updated with correct labels. |
| spec-state-schema Secret | `user-secrets` with `nats-creds` key containing NATS credentials | reconciler.go:224-258 | IMPLEMENTED | — | Issues session-agent JWT via `IssueSessionAgentJWT` and stores formatted creds. |
| spec-state-schema ConfigMap | `user-config` in user namespace | reconciler.go:213-218 | IMPLEMENTED | — | Created with empty data map. |
| spec-state-schema PVC | `project-{projectId}` with ReadWriteOnce, configurable size/storageClass | reconciler.go:354, 387-407 | IMPLEMENTED | — | PVC name, access mode, and size from template. |
| spec-state-schema PVC | `nix-{projectId}` with ReadWriteOnce, configurable size/storageClass | reconciler.go:357, 387-407 | IMPLEMENTED | — | Same pattern as project PVC. |
| spec-state-schema Deployment | `mclaude-session-agent-{projectId}` in `mclaude-{userID}` | reconciler.go:362 | GAP | SPEC→FIX | Code uses `deployName := "project-" + projectID`. Spec says `mclaude-session-agent-{projectId}`. The shorter name is more practical (K8s name length limits). Spec should be updated. |
| spec-state-schema Deployment | Replicas: 1 | reconciler.go:373 | IMPLEMENTED | — | `replicas := int32(1)`. |
| spec-state-schema Deployment | Strategy: Recreate | reconciler.go:375 | IMPLEMENTED | — | `appsv1.RecreateDeploymentStrategyType`. |
| spec-state-schema Deployment | `CLAUDE_CODE_TMPDIR` set to `/data/claude-tmp` | reconciler.go:288-306 | GAP | CODE→FIX | Env var `CLAUDE_CODE_TMPDIR` is missing from `buildPodTemplate`. Spec-state-schema line 473 says it should be set to `/data/claude-tmp`. Not present in code. |
| spec-state-schema Deployment | Volumes: project PVC, nix PVC, user-config ConfigMap, user-secrets Secret | reconciler.go:308-314 | IMPLEMENTED | — | All four volumes present plus `claude-home` EmptyDir. |
| spec-state-schema Deployment | Volume mounts at /data, /nix, /home/node/.claude-seed, /home/node/.user-secrets | reconciler.go:316-322 | IMPLEMENTED | — | All mount paths match spec. |
| spec-state-schema session-agent JWT | PubAllow = `mclaude.{userID}.>, mclaude.users.{userSlug}.hosts.*.>, _INBOX.>, $JS.*.API.>` | nkeys.go:11-16 | IMPLEMENTED | — | Exact match. |
| spec-state-schema session-agent JWT | SubAllow = same as PubAllow | nkeys.go:17 | IMPLEMENTED | — | `return perms, perms` — identical pub and sub. |
| spec-state-schema session-agent-template ConfigMap | Watched by reconciler; changes re-enqueue all MCProject CRs | reconciler.go:487-504 | IMPLEMENTED | — | `Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(...))` with predicate filtering by template CM name. |
| spec-state-schema RBAC | ServiceAccount `mclaude-session-agent` | reconciler.go:204 | GAP | SPEC→FIX | Code uses `mclaude-sa` instead of `mclaude-session-agent`. Spec says the SA name should be `mclaude-session-agent`. Code's shorter name is equally functional. Spec should be updated. |
| spec-state-schema RBAC | Role: allows get/watch on ConfigMaps | reconciler.go:210 | IMPLEMENTED | — | Role allows get, watch, patch on configmaps (user-config) and get on secrets (user-secrets). Slightly broader than spec (adds patch + secrets). |
| spec-state-schema NATS subjects | `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.provision` subscription | nats_subscriber.go:54-57 | IMPLEMENTED | — | Subscribes to `mclaude.users.*.hosts.{clusterSlug}.api.projects.>` — wildcard catches provision and all other operations. |
| spec-state-schema NATS subjects | `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.create` subscription | nats_subscriber.go:54-57,80-81 | IMPLEMENTED | — | Handled by the `>` wildcard subscription + switch on `create`/`provision`. |
| spec-state-schema NATS subjects | `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.delete` subscription | nats_subscriber.go:54-57,90 | IMPLEMENTED | — | Handled by the `>` wildcard subscription + switch on `delete`. |
| spec-state-schema NATS subjects | `mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.update` subscription | nats_subscriber.go:54-57,96 | GAP | CODE→FIX | The `update` operation falls through to the `default` case which returns an error. The spec says the controller should handle `update` to reconcile secrets and re-apply pod template. |
| spec-controller.md provisioning request | Shape: `{userSlug, hostSlug, projectSlug, gitUrl, gitIdentityId}` | nats_subscriber.go:18-27 | GAP | SPEC→FIX | Code has additional `userID` and `projectID` fields per ADR-0050 Decision 2. spec-controller.md was not updated in the ADR-0050 commit. |
| spec-controller.md reconciler step 4 | "NATS credentials in the Secret are the per-host JWT issued by control-plane (the controller does not mint JWTs)" | nkeys.go:26-46 | GAP | SPEC→FIX | Per ADR-0050 Decision 5, the controller DOES mint session-agent JWTs via `IssueSessionAgentJWT`. spec-controller.md is stale. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| main.go:1-10 | INFRA | Package declaration, imports. |
| main.go:22-70 | INFRA | `main()` — standard controller-runtime bootstrap: logger, env parsing, scheme setup, manager creation, health probes. |
| main.go:72-86 | INFRA | NATS connection with JWT auth. Standard setup. |
| main.go:88-97 | INFRA | NATSProvisioner creation and start. |
| main.go:99-105 | INFRA | Manager.Start with signal handler. |
| main.go:108-115 | INFRA | `loadAccountKey` — loads account NKey from env or creates ephemeral. |
| main.go:119-140 | INFRA | `generateNATSUserCreds` — creates ephemeral NATS user JWT for the controller's own connection. |
| main.go:143-149 | INFRA | `detectNamespace` — reads pod namespace from service account mount. |
| main.go:152-163 | INFRA | `sessionAgentNATSURL` — FQDN NATS URL for cross-namespace pod access. |
| main.go:165-171 | INFRA | `envOr` — env var helper. |
| nats_subscriber.go:29-34 | INFRA | `ProvisionReply` struct. |
| nats_subscriber.go:37-43 | INFRA | `NATSProvisioner` struct fields. |
| nats_subscriber.go:46-57 | INFRA | `StartNATSSubscriber` — subscribes to NATS subject. |
| nats_subscriber.go:60-98 | INFRA | `handleProvisionRequest` — message dispatch to operation handlers. |
| nats_subscriber.go:100-131 | INFRA | `handleCreate` — creates MCProject CR. Covered by Decision 3. |
| nats_subscriber.go:134-152 | INFRA | `handleDelete` — deletes MCProject CR. |
| nats_subscriber.go:154-172 | INFRA | `replyOK`, `replyError` — NATS reply helpers. |
| nats_subscriber.go:175-182 | INFRA | `extractOperation` — subject parsing. |
| nkeys.go:1-8 | INFRA | Package, imports. |
| nkeys.go:10-18 | INFRA | `SessionAgentSubjectPermissions` — covered by Decision 5. |
| nkeys.go:21-46 | INFRA | `IssueSessionAgentJWT` — covered by Decision 5. |
| reconciler.go:1-30 | INFRA | Package declaration, imports. |
| reconciler.go:34-48 | INFRA | `MCProjectReconciler` struct. |
| reconciler.go:50-172 | INFRA | `Reconcile` main loop — orchestrates sub-reconcilers. Covered by spec. |
| reconciler.go:174-199 | INFRA | `reconcileNamespace` — covered by spec. |
| reconciler.go:201-220 | INFRA | `reconcileRBAC` — covered by spec. |
| reconciler.go:222-279 | INFRA | `reconcileSecrets` — covered by spec (user-secrets + imagePullSecrets copy). |
| reconciler.go:281-353 | INFRA | `buildPodTemplate` — covered by Decision 4. |
| reconciler.go:355-416 | INFRA | `reconcileDeployment` — covered by spec. |
| reconciler.go:418-440 | INFRA | `ensurePVCCR` — PVC creation. |
| reconciler.go:442-455 | INFRA | `ensureOwned` — generic create-if-not-exists. |
| reconciler.go:457-477 | INFRA | `loadTemplate` — loads session-agent template ConfigMap. |
| reconciler.go:479-510 | INFRA | `SetupWithManager` — controller-runtime wiring. |
| reconciler.go:512-622 | INFRA | `sessionAgentTpl` struct, `defaultTemplate`, `applyDefaultResources`, condition/phase helpers. |
| reconciler.go:339-353 | INFRA | `reconcilerCAConfigMapHash` — corporate CA hash for rollout annotation. |
| mcproject_types.go:1-113 | INFRA | CRD type definitions, scheme registration, DeepCopy. Covered by spec. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| ADR-0050 Decision 2 | ProvisionRequest gains UserID and ProjectID | None | None | UNTESTED | No test files exist in mclaude-controller-k8s/. |
| ADR-0050 Decision 3 | MCProjectSpec gains UserSlug/ProjectSlug; handleCreate populates | None | None | UNTESTED | No test files exist. |
| ADR-0050 Decision 4 | Reconciler env vars USER_ID, PROJECT_ID, USER_SLUG, PROJECT_SLUG, HOST_SLUG | None | None | UNTESTED | No test files exist. |
| ADR-0050 Decision 5 | Session-agent JWT permissions | None | None | UNTESTED | No test files exist. Critical auth flow — should have integration tests per evaluator bar. |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| 001-new-project-crash-no-git-credentials.md | — | N/A | Component not controller-k8s. |
| 002-ptt-race-condition.md | — | N/A | Component not controller-k8s. |
| 003-initial-message-injection-race.md | — | N/A | Component not controller-k8s. |
| 005-getting-started-canned-text.md | — | N/A | Component not controller-k8s. |

No open bugs match `mclaude-controller-k8s`.

### Summary

- Implemented: 27
- Gap: 6
- Partial: 0
- Infra: 32
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 0
- Untested: 4
- Bugs fixed: 0
- Bugs open: 0

### Gap Details

```
GAP [CODE→FIX]: "HOST_SLUG = tpl.hostSlug (cluster slug, unchanged)" → tpl.hostSlug is never populated. The sessionAgentTpl struct has the field (reconciler.go:609) but neither loadTemplate() nor defaultTemplate() sets it. The reconciler doesn't receive CLUSTER_SLUG from main.go. The Helm session-agent-template ConfigMap doesn't include a hostSlug key. Result: session-agent pods get HOST_SLUG="" (empty string). (reconciler.go:299, reconciler.go:457-477, reconciler.go:512-528)

GAP [CODE→FIX]: "CLAUDE_CODE_TMPDIR: set to /data/claude-tmp" → Env var CLAUDE_CODE_TMPDIR is missing from buildPodTemplate. Spec-state-schema line 473 requires it for shell output file persistence across pod restarts. (reconciler.go:288-306)

GAP [CODE→FIX]: "mclaude.users.*.hosts.{CLUSTER_SLUG}.api.projects.update" → The update operation is not handled. It falls through to the default case in handleProvisionRequest which returns an error reply. Spec says it should reconcile user-secrets and re-apply the pod template. (nats_subscriber.go:96)

GAP [SPEC→FIX]: "Name: {projectId}" → Code uses slug-based naming: crName = fmt.Sprintf("%s-%s", req.UserSlug, req.ProjectSlug). This is more human-readable for kubectl. Spec should be updated. (nats_subscriber.go:96)

GAP [SPEC→FIX]: "Deployment: mclaude-session-agent-{projectId}" → Code uses deployName = "project-" + projectID. Shorter name avoids K8s name length issues. Spec should be updated. (reconciler.go:362)

GAP [SPEC→FIX]: spec-controller.md provisioning request shape and JWT minting statement are stale — not updated by ADR-0050 commit. (spec-controller.md)
```
