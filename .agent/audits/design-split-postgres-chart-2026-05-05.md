## Run: 2026-05-05T00:00:00Z

**Gaps found: 4**

1. **ADR internal contradiction: bootstrap-admin location described as both resolved and TBD** — The Decisions table says "Bootstrap admin location: Stay in `init-keys` pre-install hook" and the Open Questions section says "(none — all resolved)". But the User Flow section says "Whichever Job runs the admin bootstrap (TBD — see open question) executes:" and the Deferred section says "The `init-keys` Job's bootstrap-admin location… is the open question below." A developer reading the ADR cannot determine whether the bootstrap-admin location is settled or still open.
   - **Doc**: Line 74: "Whichever Job runs the admin bootstrap (TBD — see open question)"; Line 214: "the precise location is the open question below"; Line 218: "_(none — all resolved)_"
   - **Code**: `mclaude-control-plane/init_keys.go` — bootstrap admin runs in `runInitKeys()`, called by the pre-install Job. No ambiguity in the code, but the ADR text would leave a developer unsure whether to treat this as settled.

2. **`init_keys.go` control flow does not match the "UPSERT on every helm upgrade" requirement** — The ADR and both updated specs say the bootstrap admin UPSERT must run on every `helm upgrade`. Current code in `runInitKeys()` (line 53–59 of `init_keys.go`) returns immediately when the `operator-keys` Secret already exists, so `bootstrapAdminUser` (line 112–118) is never reached on any upgrade after initial install. The ADR's implementation note says "One SQL change" but the required fix is a Go control-flow restructuring: the Secret-exists early-exit must be scoped to key generation only, leaving the admin bootstrap to proceed unconditionally. The ADR does not specify this structural change.
   - **Doc**: ADR Decisions table row "Bootstrap admin idempotence": "Each helm upgrade attempts admin creation; second+ runs are no-ops." Spec `spec-control-plane.md` line 42: "Idempotent on every helm upgrade: `INSERT ... ON CONFLICT (email) DO UPDATE …`"
   - **Code**: `mclaude-control-plane/init_keys.go:53–59` — early return when Secret exists; `bootstrapAdminUser` at line 112 is never reached on upgrade. `mclaude-control-plane/init_keys.go:154` — `if password != "" && existing.PasswordHash == ""` only sets the password when the hash is empty, so the integration test case "Bootstrap idempotence on password rotation" (ADR line 226) would also fail: rotating a password for a user who already has a hash is not handled.

3. **`charts/mclaude-postgres/values-k3d.yaml` is referenced in the CI workflow but its contents are unspecified** — The ADR's CI step passes `-f ./charts/mclaude-postgres/values-k3d.yaml` (ADR line 152). The spec (`spec-helm.md` line 217) says "Local k3d uses these [defaults] as-is plus `--set existingSecret=mclaude-postgres`" — implying no k3d-specific values file is needed. The ADR does not specify what `values-k3d.yaml` should contain or whether it should exist at all. If the file is absent, the CI step fails. If the file is present, a developer cannot determine its contents from the ADR.
   - **Doc**: ADR line 152: `-f ./charts/mclaude-postgres/values-k3d.yaml`; ADR line 234: "StatefulSet, two Services, values.yaml, values-k3d.yaml, values-aks.yaml, NOTES.txt" (lists it as a deliverable). `spec-helm.md` line 217: "Local k3d uses these as-is plus `--set existingSecret=mclaude-postgres`" (implies defaults-only, no k3d file).
   - **Code**: `charts/mclaude-postgres/` does not exist yet. No existing equivalent to compare against.

4. **`dbmate` init-container in `control-plane-deployment.yaml` is not covered by the env-block update** — The ADR specifies updating "the Control-plane Deployment env block" with the new `database.*` shape (ADR lines 126–136). The existing `control-plane-deployment.yaml` has a second env block inside the optional `dbmate` init-container (lines 33–47) that hardcodes `name: mclaude-postgres` and `{release}-postgres.{namespace}.svc:5432`. The ADR does not mention this init-container. A developer who follows the ADR would update the main container but leave the `dbmate` init-container with a broken hardcoded DSN, causing `helm upgrade` to fail for any deployment where `controlPlane.migrations.enabled=true`.
   - **Doc**: ADR line 126–136: "Control-plane Deployment env block: [new POSTGRES_PASSWORD + DATABASE_URL]" — no mention of init-container.
   - **Code**: `charts/mclaude-cp/templates/control-plane-deployment.yaml:33–47` — `dbmate` init-container reads from hardcoded `mclaude-postgres` Secret (line 37) and constructs a hardcoded DSN pointing at `{release}-postgres.{namespace}.svc:5432` (line 40). Both must be parameterized with `database.*` values the same way as the main container.

## Run: 2026-05-05T00:01:00Z

CLEAN — no blocking gaps found.

All 4 round-1 gaps are resolved:
1. TBD/"open question" wording removed; User Flow section unambiguously names the init-keys Job; Deferred section no longer lists bootstrap-admin location.
2. Component Changes — mclaude-control-plane now has a three-point control-flow restructure: early-return scoped to key generation only, unconditional admin bootstrap when BOOTSTRAP_ADMIN_EMAIL is set, and UPSERT SQL replacing the conditional INSERT.
3. values-k3d.yaml ghost reference removed from the CI workflow snippet; Implementation Plan deliverables list values.yaml / values-aks.yaml / values-e2e.yaml / NOTES.txt only.
4. Dedicated "control-plane-deployment.yaml — dbmate init container" subsection added, requiring the same database.* env block as the main container.
