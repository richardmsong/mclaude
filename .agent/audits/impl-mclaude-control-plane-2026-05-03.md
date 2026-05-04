## Run: 2026-05-03T00:00:00Z

Component: mclaude-control-plane
Root: mclaude-control-plane/
Primary spec: docs/mclaude-control-plane/spec-control-plane.md
ADRs evaluated: adr-0086-fix-admin-grant-cluster-public-key.md (accepted); adr-0085 (implemented, skipped — CLI component)
Focus: ADR-0086 — adminGrantCluster public_key handling

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-control-plane.md:121 | `POST /admin/clusters/{cslug}/grants` — Creates a new `hosts` row for that user with `public_key = NULL` | admin.go:517-521 | IMPLEMENTED | — | SELECT (492-495) fetches only `direct_nats_url`; INSERT (517-521) column list is `(id, user_id, slug, name, type, role, direct_nats_url, user_jwt)` — `public_key` absent, DB default NULL applies |
| spec-control-plane.md:121 | copies `direct_nats_url` from the cluster owner row | admin.go:492-495, 521 | IMPLEMENTED | — | `SELECT direct_nats_url FROM hosts WHERE slug = $1 AND type = 'cluster' LIMIT 1` → `$5 = directNATSURL` in INSERT |
| spec-control-plane.md:121 | mints a per-user JWT via `IssueHostJWTLegacy(user.Slug, cslug, accountKP)` | admin.go:510 | IMPLEMENTED | — | Exact call matches spec |
| spec-control-plane.md:121 | Idempotent — `ON CONFLICT (user_id, slug) DO NOTHING` | admin.go:520 | IMPLEMENTED | — | Clause present |
| spec-control-plane.md:121 | returns 201 without creating a duplicate row | admin.go:527 | IMPLEMENTED | — | `w.WriteHeader(http.StatusCreated)` unconditionally after exec (conflict-skip does not error) |
| adr-0086:33-57 | Remove `public_key` from the SELECT in `adminGrantCluster` | admin.go:492-495 | IMPLEMENTED | — | SELECT is `SELECT direct_nats_url FROM hosts WHERE slug = $1 AND type = 'cluster' LIMIT 1` — no `public_key` column selected |
| adr-0086:33-57 | Remove `public_key` from the INSERT in `adminGrantCluster` | admin.go:517-521 | IMPLEMENTED | — | INSERT column list `(id, user_id, slug, name, type, role, direct_nats_url, user_jwt)` — no `public_key` |
| adr-0086:26 | Keep copying `direct_nats_url` from owner row | admin.go:492-495, 521 | IMPLEMENTED | — | Confirmed |
| spec-control-plane.md:119 | `POST /admin/clusters` owner row carries `public_key` (cluster's NKey) | admin.go:400-403 | IMPLEMENTED | — | `INSERT INTO hosts … public_key … VALUES … clusterNKey.PublicKey` — owner row keeps the NKey |
| spec-control-plane.md:119 | Known bug: response omits `accountJwt`/`operatorJwt` | admin.go:414-428 | PARTIAL | SPEC→FIX | `accountJWTStr` is set to account *public key* (not the JWT), `operatorJwt` never populated. Spec says "fields exist but unpopulated" — code matches. Intentional deferral (KNOWN-07). |

---

### Phase 2 — Code → Spec (ADR-0086 scope only)

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| admin.go:373 | UNSPEC'd | `_ = clusterNKey` is present after `clusterNKey` is generated, but `clusterNKey.PublicKey` is used at line 403. The comment "The generated NKey public key is embedded in clusterJWT" is misleading — `IssueHostJWTLegacy` generates its own internal NKey; `clusterNKey` and the JWT embed different NKeys. This is a pre-existing issue in `adminRegisterCluster`, not introduced by ADR-0086. No spec describes the relationship between `clusterNKey` and the JWT NKey, so this is UNSPEC'd rather than a GAP. |
| admin.go:490 | INFRA | Comment `// ADR-0086: public_key is the cluster controller's NKey identity — do not copy to per-user rows.` — inline rationale, no action needed. |

---

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| spec-control-plane.md:121 / adr-0086 | `adminGrantCluster` does NOT include `public_key` in SELECT or INSERT | breakglass_test.go:TestAdminGrantCluster_NilDB (nil-DB guard only; no DB-backed assertion that `public_key` is NULL in the inserted row) | mclaude-cli/cmd/integration_main_test.go:`grantClusterAccess` exercised in TestMain setup (returns 201 on real cluster with `public_key` set — this is the E2E validation that the unique constraint no longer fires) | UNIT_ONLY (partial) — unit test exercises nil-DB path only; E2E integration test through the CLI `grantClusterAccess` helper validates the full SQL INSERT round-trip against a real cluster that has a non-NULL `public_key` |
| spec-control-plane.md:121 | `ON CONFLICT DO NOTHING` idempotency | breakglass_test.go:TestAdminGrantCluster_RequestValidation (struct only; no idempotency DB test) | CLI TestMain calls `grantClusterAccess` once; no duplicate-grant test | UNIT_ONLY |
| spec-control-plane.md:121 | `direct_nats_url` copied from owner row | No unit test with a real DB row | No dedicated E2E test | UNTESTED |

---

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` have `**Component**: mclaude-control-plane`. No bug triage entries to add.

---

### Summary

- Implemented: 9
- Gap: 0
- Partial: 1 (spec-control-plane.md:119 `accountJwt`/`operatorJwt` — SPEC→FIX, intentional deferral already noted in spec)
- Infra: 1
- Unspec'd: 1 (admin.go:373 misleading comment about clusterNKey vs JWT NKey)
- Dead: 0
- Tested: 0
- Unit only: 2
- E2E only: 0
- Untested: 1 (`direct_nats_url` copy assertion)
- Bugs fixed: 0
- Bugs open: 0
