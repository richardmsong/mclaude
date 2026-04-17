## Run: 2026-04-16T00:00:00Z

**Document:** docs/plan-reconciler-env-sync.md

**Gaps found: 3**

1. **`gitIdentityId` already exists in Go types but is absent from the CRD YAML — the gap is real but misdescribed** — The design doc says `gitIdentityId` is "currently tolerated as an additional property" and that the fix is to add it to the CRD schema. This is incorrect: `MCProjectSpec` in `mcproject_types.go` already declares `GitIdentityID string \`json:"gitIdentityId,omitempty"\`` (line 47), and `reconcileDeployment` already reads `mcp.Spec.GitIdentityID` (line 306) and conditionally sets `GIT_IDENTITY_ID` (lines 357–360). The field is fully wired in Go. What is actually missing is the `gitIdentityId` property in `charts/mclaude/templates/mcproject-crd.yaml` (confirmed absent — the YAML `spec.properties` only lists `userId`, `projectId`, and `gitUrl`). A developer implementing the doc as written would correctly add the YAML field, but might also assume the Go struct and reconciler env-var logic need to be added — they do not. The doc needs to accurately state that the Go types and env-var logic are already correct; only the CRD YAML schema is missing.
   - **Doc**: "The reconciler already computes the correct env vars (including `GIT_IDENTITY_ID` when `gitIdentityId` is non-empty) and volumes. The fix is to apply them to the existing Deployment instead of only updating the image."
   - **Code**: `mclaude-control-plane/mcproject_types.go:47` has the field; `reconciler.go:306,357-360` reads and emits it on the create path. The update path at `reconciler.go:320-326` does not.

2. **Update path fix specification is incomplete — no guidance on `imagePullSecrets` rebuild** — The design doc says the update path must "rebuild containers[] with current env vars, image, command, volumeMounts" and "rebuild volumes[] with current volume list." The create path at `reconciler.go:338-345` dynamically discovers `imagePullSecrets` by listing Secrets in the user namespace at create time. The doc does not say whether the update path should re-discover and sync `imagePullSecrets` as well. If a new registry credential Secret is added after initial Deployment creation, the existing pod template will lack it. A developer must decide: (a) sync `imagePullSecrets` on every update (consistent with "full container spec rebuild"), or (b) leave them as-is (not a full rebuild). The doc is silent on this point.
   - **Doc**: "rebuild containers[] with current env vars, image, command, volumeMounts" — no mention of `imagePullSecrets`
   - **Code**: `reconciler.go:338-345` builds `imagePullSecrets` dynamically during create. `reconciler.go:390-391` sets `ImagePullSecrets: imagePullSecrets` in the pod spec. The update path at `reconciler.go:319-326` does not touch `imagePullSecrets`.

3. **State schema inconsistency: `MCProject` CRD spec schema is missing `gitIdentityId`** — The canonical state schema at `docs/plan-state-schema.md` defines the MCProject CRD spec as having only `userId`, `projectId`, and `gitUrl` (lines 305-318). It does not include `gitIdentityId`. The design doc adds `gitIdentityId` to the CRD YAML without noting that the state schema must be updated to match. A developer checking the state schema would see the field is not listed there and face a contradiction between the schema doc (authoritative for schemas) and the design doc (which is adding the field). The state schema must be updated as part of this change.
   - **Doc**: "Add `gitIdentityId` to the OpenAPI v3 schema under `spec.properties`" — no mention of updating `docs/plan-state-schema.md`
   - **Code/Schema**: `docs/plan-state-schema.md:305-318` — MCProject spec lists only `userId`, `projectId`, `gitUrl`. `gitIdentityId` is absent.

---

## Run: 2026-04-16T12:00:00Z (re-audit after gap fixes)

**Document:** docs/plan-reconciler-env-sync.md

**Fixes claimed:**
1. Clarified that Go types already have `gitIdentityId` — only CRD YAML schema is missing
2. Added `imagePullSecrets` to the update path sync list
3. Added `gitIdentityId` to `docs/plan-state-schema.md`

**Verification against current files:**

- Gap 1 (Go types / CRD YAML): The design doc now correctly states "The Go struct (`MCProjectSpec`) and reconciler create path already use this field, but the CRD YAML schema omits it." This accurately describes the code state. `mcproject_types.go:47` has the field; `mcproject-crd.yaml` does not. Gap 1 is closed.

- Gap 2 (imagePullSecrets): The design doc now explicitly includes "re-discover imagePullSecrets (list Secrets in user namespace, same as create path)" in the update path pseudocode. Gap 2 is closed.

- Gap 3 (state schema): `docs/plan-state-schema.md` lines 299-320 now show `gitIdentityId: string  # optional — oauth_connections.id for git credential resolution` in the MCProject CRD spec table. Gap 3 is closed.

CLEAN — no blocking gaps found.
