## Run: 2026-05-01T00:00:00Z

CLEAN — no blocking gaps found.

### Verification summary

All gaps from prior rounds were confirmed resolved:

- **Round 6 gap 1 (CONTROL_PLANE_URL injection mechanism):** The ADR now explicitly specifies adding a `controlPlaneURL string` field to the reconciler struct, injecting it directly at reconcile time into the session-agent pod spec, and explicitly says "Do **not** add it to the `session-agent-template.yaml` ConfigMap." This is unambiguous.

- **Round 6 gap 2 (spec-state-schema.md field name inconsistencies):** The ADR's Impact section now lists all five corrections: (1) line 431 `"Leafnode"` → `"Client"`, (2) line 409 `nkeyPublic` → `nkey_public`, (3) line 64 `nkey_public` → `public_key`, (4) line 60 `owner_id` → `user_id`, (5) lines 61/70 `UNIQUE (slug)` → `UNIQUE (user_id, slug)`. All five corrections match the live DDL at `db.go:842–860`.

### Key facts confirmed against codebase

- `db.go:844`: hosts FK column is `user_id` (not `owner_id`); `db.go:853`: host NKey column is `public_key` (not `nkey_public`); `db.go:858`: constraint is `UNIQUE (user_id, slug)` (not globally unique).
- `db.go:859`: the CHECK constraint `(type = 'machine' OR (js_domain IS NOT NULL AND leaf_url IS NOT NULL AND account_jwt IS NOT NULL))` exists and must be dropped — correctly identified and included in the data model migration.
- `challenge.go:88–95`: CP returns HTTP 404 (`StatusNotFound`) with `code: "NOT_FOUND"` when `nkey_public` is not registered — matches the boot-loop detection described in the ADR.
- `lifecycle.go:237–241`: `NATSHostRegisterRequest` uses `json:"nkey_public"` — matches the ADR's instruction to correct spec-state-schema.md line 409 from `nkeyPublic` to `nkey_public`.
- `sys_subscriber.go:82–157`: current code filters `type = 'machine'` on the `"Client"` branch and `type = 'cluster'` on `"Leafnode"` branch — the required changes (remove type filter from `"Client"`, drop `"Leafnode"` branch) are unambiguously specified.
- `reconciler.go:218–222, 239–245`: `IssueSessionAgentJWT` and `FormatNATSCredentials` calls exist exactly at the lines referenced — the removal instruction is precise.
- `reconciler.go:44`: `accountKP nkeys.KeyPair` field present; `main.go:50`: `loadAccountKey()` present — removal targets confirmed.
- `mclaude-controller-local/host_auth.go`: `NewHostAuthFromCredsFile` constructor present; `HostAuth` struct with `kp`, `cpURL`, `currentJWT` fields present — `NewHostAuthFromSeed` addition is clearly specified and the existing type's fields are sufficient to implement it.
- `mclaude-common/go.mod:7`: `github.com/nats-io/nkeys v0.4.15` is already a direct dependency — the new `mclaude-common/pkg/hostauth/` package can use it without a module change.
- `mclaude-controller-k8s/main.go:207–220`: `sessionAgentNATSURL()` derivation function exists and will be dropped per the ADR — confirmed.
- `charts/mclaude-worker/templates/controller-deployment.yaml`: all env vars to be dropped (`CLUSTER_SLUG`, `JS_DOMAIN`, `NATS_ACCOUNT_SEED`, `NATS_CREDENTIALS_PATH`, `SESSION_AGENT_NATS_URL`) are present; all volumes to be removed (`leaf-creds`) are present — the chart strip instructions are complete and accurate.
- `docs/mclaude-controller/spec-controller.md` exists; `docs/mclaude-controller-k8s/` and `docs/mclaude-controller-local/` do not yet exist — the file migration targets are consistent with reality.
