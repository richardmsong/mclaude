## Run: 2026-04-30T12:00:00Z (Round 3)

CLEAN — no blocking gaps found.

### Scope of review

End-to-end consistency check of the full document after rounds 1-2 fixes (7 gaps total resolved). Verified:

1. **Permission specs (all 4 identity types + one-shot JWT):** Every subject in Pub.Allow and Sub.Allow cross-referenced against the NATS Subject Derivation Reference table. KV direct-get subject form (`$JS.API.DIRECT.GET.<stream>.$KV.<bucket>.<key>`) verified for all buckets. Consumer create filtered form (`$JS.API.CONSUMER.CREATE.<stream>.<consumer>.<filter>`) verified — `*` for consumer name, explicit filter subject for per-project scoping. Flow control subjects present in both Pub and Sub (server sends, client replies). ACK subjects scoped per-stream. No cross-user resource references in any non-CP identity.

2. **Wire formats:** Credential issuance (`api.agents.credentials`), agent refresh, host refresh, and user JWT refresh — all use consistent `{ok, jwt, error, code}` envelope pattern for NATS-based protocols. User refresh via HTTP has its own format (`{nats_jwt, nats_seed}`) which differs in field names from the login response (`jwt`, `nkeySeed`) — this is a deliberate rename, not an inconsistency; both formats are fully specified.

3. **Provisioning flow (CP intermediation verified in code):** Confirmed the control-plane IS already the intermediary for project provisioning. SPA publishes `mclaude.users.{uslug}.hosts.{hslug}.api.projects.create` → CP subscribes on wildcard `mclaude.users.*.hosts.*.api.projects.create` → CP creates in Postgres → CP publishes `mclaude.hosts.{hslug}.api.projects.provision` to controller. The ADR's host subject scheme change only affects the CP→controller leg; the SPA→CP leg uses user-scoped subjects and is unchanged.

4. **KV key format consistency:** Sessions (`{hslug}.{pslug}.{sslug}`), projects (`{hslug}.{pslug}`), job-queue (`{hslug}.{pslug}.{jobid}`), hosts (`{hslug}`) — all consistent with per-user bucket naming (uslug moves from key to bucket name). Permission entries match key formats in all Pub/Sub/direct-get/consumer-create subjects.

5. **Stream consolidation:** Three streams (`MCLAUDE_EVENTS`, `MCLAUDE_API`, `MCLAUDE_LIFECYCLE`) → `MCLAUDE_SESSIONS_{uslug}`. Session subject hierarchy table is complete (9 subjects). Stream filter `mclaude.users.{uslug}.hosts.*.projects.*.sessions.>` covers all. Terminal I/O subjects explicitly excluded (remain under `api.terminal.*`). Ordered push consumers specified — no `$JS.API.CONSUMER.MSG.NEXT` in any identity's permissions (pull consumer subject correctly omitted).

6. **Credential lifecycle:** Initial issuance (host→CP via `api.agents.credentials`), proactive refresh (before 5-min TTL), on-demand refresh signal, revocation (NATS revocation list with auto-expiry after JWT TTL). NKey pair ownership is clear: caller generates keypair, sends public key; CP signs JWT against it and never handles private key material.

7. **Cross-spec consistency with `spec-state-schema.md`:** The ADR's Data Model section specifies changes to hosts table schema (add `group_id`, remove `user_id`/`role`, rename `user_jwt`→`nats_jwt`), KV key formats, and stream names. All differences from the current spec-state-schema are intentional design changes, and the ADR's Impact section correctly lists `spec-state-schema.md` as a spec to update.

8. **Group management:** Schema (Postgres `groups` + `group_members` tables), REST API endpoints, CLI commands, membership cache, and revocation flow — all specified. `is_everyone` column present in schema with deferred semantics (the ADR says "the operator can later convert" — clearly not v1 scope; column exists for forward compatibility). Registration→group binding flow is unambiguous for both BYOH and cluster hosts.

9. **Round 1-2 fix integration:** All 7 previously identified gaps are properly resolved with no regressions. Agent JWT 5-min TTL is consistent throughout. NKey refresh protocol (client generates keypair) is reflected in all wire formats. Ordered push consumers are reflected in permission specs. `SessionAgentSubjectPermissions(uslug, hslug, pslug)` signature is used consistently. `import_jwt` field is present in credential issuance response. `IssueHostJWT`/`IssueSessionAgentJWT` rewrite notes correctly describe external public key acceptance.
