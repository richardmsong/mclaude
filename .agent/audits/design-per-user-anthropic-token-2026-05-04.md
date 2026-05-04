## Run: 2026-05-04T00:00:00Z

Design document: `docs/adr-0091-per-user-anthropic-token.md`

**Gaps found: 10**

1. **ID column type mismatch: `user_host_tokens.id` declared UUID, all existing tables use TEXT** — The ADR declares `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`. Every existing table in `db.go` uses `id TEXT PRIMARY KEY` with UUIDs stored as text (e.g. `users`, `hosts`, `projects`, `attachments`). A developer implementing the migration would not know which type to use.
   - **Doc**: "id UUID PRIMARY KEY DEFAULT gen_random_uuid()"
   - **Code**: `mclaude-control-plane/db.go` lines 835, 847, 867, 878, 981, 996 all use `id TEXT PRIMARY KEY`.

2. **`user_host_tokens` missing from state schema** — The state schema (`docs/spec-state-schema.md`) is the authoritative schema document and explicitly requires updates when new state is added. The ADR's Impact section lists "add `user_host_tokens` table … to `docs/spec-state-schema.md`" as a required spec update, but the ADR is only about the design decision — the state schema has not been updated. A developer reading only the state schema (which they must follow per its own header) sees no `user_host_tokens` table definition to implement against.
   - **Doc**: "docs/spec-state-schema.md: add `user_host_tokens` table … remove DEV_OAUTH_TOKEN"
   - **Code**: `docs/spec-state-schema.md` has no `user_host_tokens` entry, no `x25519_public_key` on `hosts`, no `user-anthropic-{uslug}` Secret, no `mclaude-host-key` Secret.

3. **`hosts.x25519_public_key` not in state schema** — The ADR adds a new column `x25519_public_key TEXT` to the `hosts` table. The state schema's `hosts` table definition does not include this column. The state schema is the authoritative schema; a developer implementing the migration has no authoritative definition.
   - **Doc**: "ALTER TABLE hosts ADD COLUMN x25519_public_key TEXT;"
   - **Code**: `docs/spec-state-schema.md` `hosts` table (lines 57–84) lists no `x25519_public_key` column.

4. **`mclaude.hosts.{hslug}.token.updated` not in state schema NATS subject catalog** — The ADR introduces a new host-scoped NATS subject for token update notifications. The state schema's NATS subject catalog is the authoritative list of all subjects. This subject is absent from it. A developer cannot determine: whether it is request/reply or fire-and-forget, what the payload is, what authorization is required, or whether the host JWT already has permission to subscribe to it.
   - **Doc**: "Notifies host via NATS: `mclaude.hosts.{hslug}.token.updated`"
   - **Code**: `docs/spec-state-schema.md` NATS subjects section (lines 361–445) has no `token.updated` subject entry. The host JWT `Sub.Allow` (line 678) only includes `mclaude.hosts.{hslug}.>` — this subject falls under that wildcard — but the spec schema needs the entry to be authoritative.

5. **Host JWT permission not verified to cover `token.updated`** — The controller subscribes to `mclaude.hosts.{hslug}.token.updated` to receive K from CP. The existing host JWT `Sub.Allow` in the state schema is `mclaude.hosts.{hslug}.>` which would cover this subject, but the ADR does not state this explicitly and does not update the JWT permissions section in the state schema. If the host JWT `Sub.Allow` is ever tightened, this subscription will silently break. The subject also needs a `Pub.Allow` on the CP side — the ADR does not specify which NATS identity CP uses to publish this subject.
   - **Doc**: "Subscribes to `mclaude.hosts.{hslug}.token.updated` — on receipt: decrypts K from `K_wrapped`…"
   - **Code**: CP's NATS connection has unrestricted publish in the account; the host JWT has `Sub.Allow: mclaude.hosts.{hslug}.>`. Not specified in the ADR.

6. **`user-anthropic-{uslug}` Secret not in state schema Kubernetes resources section** — The ADR introduces a new per-user k8s Secret in the user namespace (`mclaude-{userSlug}`). The state schema's Kubernetes resources section is authoritative for all K8s resources in the user namespace. This new Secret is not added to that section. A developer implementing the CP side (creating the Secret) or the session-agent side (mounting it) has no authoritative specification of the Secret's namespace, owner references, RBAC (the existing Role only grants `get` on `user-secrets` by name — it does not cover `user-anthropic-{uslug}`), or lifecycle.
   - **Doc**: "CP fetches `(token_ct, k_wrapped)` … and writes both into a k8s Secret `user-anthropic-{uslug}` in the user's namespace"
   - **Code**: `docs/spec-state-schema.md` K8s resources section (lines 449–566) lists `user-secrets`, `user-config`, PVCs, Deployments, RBAC — no `user-anthropic-{uslug}` Secret. The RBAC Role at line 563 grants `get` on `user-secrets` by `resourceNames` — it does not grant the session-agent pod access to `user-anthropic-{uslug}`.

7. **CLI `mclaude auth anthropic` key derivation contradicts the SPA flow** — The ADR describes two incompatible K derivation strategies in the same document. The SPA uses "fresh ephemeral K generated per token-set operation (not derived from seed)" per the Decisions table. The CLI "Component Changes" section instead uses `K = HKDF(seed, "anthropic-token-key-v1", host_id)` — a deterministic derivation from the NKey seed. A developer implementing the CLI does not know which strategy to use; the Decisions table explicitly rejects HKDF in favor of ephemeral K.
   - **Doc (Decisions table)**: "Symmetric key K | Fresh ephemeral K generated per token-set operation (not derived from seed) | Simpler; K_wrapped in DB is sufficient for recovery"
   - **Doc (CLI Component Changes)**: "Derives K = HKDF(seed, 'anthropic-token-key-v1', host_id)"

8. **AES-256-GCM nonce encoding and openssl flags explicitly marked TBD** — The session-agent decryption step in `entrypoint.sh` includes a comment `(exact openssl flags TBD based on GCM nonce encoding chosen in CLI)`. The nonce prepending format, IV length, and openssl invocation flags are unspecified. A developer implementing `entrypoint.sh` cannot proceed without knowing these.
   - **Doc**: "`(exact openssl flags TBD based on GCM nonce encoding chosen in CLI)`"
   - **Code**: `mclaude-session-agent/entrypoint.sh` currently reads `/home/node/.user-secrets/oauth-token` as a plain file (line 28–29). No AES-GCM decryption exists.

9. **`mclaude-host-key` Secret not in state schema Kubernetes resources section, and RBAC for session-agent access unspecified** — The session-agent pod must mount `/secrets/host-key/sym-key` from `mclaude-host-key`. This Secret lives in `mclaude-system` (the host namespace), not in `mclaude-{userSlug}` (the user namespace). Cross-namespace Secret mounts are not supported in Kubernetes. The ADR does not specify the namespace of `mclaude-host-key`, the cross-namespace access mechanism, or the RBAC needed for the session-agent pod's ServiceAccount to read it.
   - **Doc**: "Session-agent pod mounts: `/secrets/host-key/sym-key` — from `mclaude-host-key` (host-level Secret, read-only)"
   - **Code**: `mclaude-controller-k8s/reconciler.go` session-agent pod is in namespace `mclaude-{userSlug}`. The reconciler's RBAC Role (line 194) grants `get` on `user-secrets` only, with no reference to `mclaude-host-key` or cross-namespace access.

10. **CP endpoint `POST /api/users/{uslug}/hosts/{hslug}/anthropic-token` auth model — "self only" not defined** — The ADR states auth is "self only" for this endpoint. It is not specified whether this means the caller's JWT user slug must equal `{uslug}`, or whether it requires a specific challenge-response flow, or whether host owners can set tokens on behalf of users. The existing auth middleware enforces `JWT.subject == {uslug}` for user endpoints — a developer must know if this standard check applies or if a different constraint is needed.
   - **Doc**: "Auth: self only" (CP component changes section)
   - **Code**: `docs/mclaude-control-plane/spec-control-plane.md` authentication section describes slug-match middleware but does not cover this endpoint, since the endpoint does not yet exist.
