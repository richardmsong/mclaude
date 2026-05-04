## Run: 2026-04-28T00:00:00Z

### ADR: docs/adr-0048-fix-session-store-slug-regression.md
### Status: accepted

---

### Phase 0 — Gather

ADR-0048 fixes a regression introduced by ADR-0046 where `SessionStore` watches `mclaude-projects` and `mclaude-sessions` KV buckets using `userSlug` (e.g. `"dev"`) after ADR-0046 populated `LoginResponse.UserSlug`, but the actual KV data is still UUID-prefixed. The fix is to pass `authState.userId` (UUID) as the `userSlug` argument to `SessionStore` until the KV buckets are migrated.

**ADR-stated impact:** No spec changes. "Specs already document the intended slug-based key format everywhere. The UUID-based interim state is an implementation artifact, not a design decision."

**Specs referenced in ADR:** None explicitly named. ADR states no spec updates needed.

**Specs identified as relevant for verification:**
- `docs/spec-state-schema.md` — KV bucket key formats
- `docs/mclaude-web/spec-web.md` — SPA state management, SessionStore usage
- `docs/ui/mclaude-web/spec-dashboard.md` — Dashboard and SessionStore references

---

### Phase 1 — ADR → Spec (forward pass)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions table row 1 | "`SessionStore` uses `userId` (UUID) for KV watches — Pass `authState.userId` (UUID) as the `userSlug` argument to `SessionStore`. `HeartbeatMonitor` continues to receive `authState.userSlug` (slug)." | `docs/spec-state-schema.md` — `mclaude-projects` key format: `{uslug}.{hslug}.{pslug}`; `mclaude-sessions` key format: `{uslug}.{hslug}.{pslug}.{sslug}`. Spec describes *intended* slug-based keys. No spec statement about `App.tsx` wiring or what argument is passed to `SessionStore` constructor. | REFLECTED | — | ADR explicitly frames this as an interim implementation workaround, not a design decision. The spec correctly describes the *intended* (future, post-migration) key format using slugs. The ADR's claim that "specs already document the intended slug-based key format everywhere" is accurate — `spec-state-schema.md` documents the slug-based key scheme, which remains the target design. Passing UUID-as-userSlug is a temporary workaround that should not be encoded in the spec. A developer reading the spec would correctly implement the slug-based scheme when KV data is eventually migrated. |
| Decisions table row 2 | "`mclaude-hosts` KV watch keeps using `userSlug` — `HeartbeatMonitor` keeps receiving `userSlug` (`"dev"`). `mclaude-hosts` entries are slug-keyed (`dev.local`) per ADR-0046." | `docs/spec-state-schema.md` — `mclaude-hosts` key format: `{uslug}.{hslug}`; `docs/mclaude-control-plane/spec-control-plane.md` — `$SYS` events write `{uslug}.{hslug}` keys. `docs/mclaude-web/spec-web.md` references `HeartbeatMonitor` (via ADR-0046's decisions being implemented) watching `mclaude-hosts`. | REFLECTED | — | Spec confirms `mclaude-hosts` is slug-keyed. Keeping `HeartbeatMonitor` on `userSlug` is correct and consistent with the spec. This is the unchanged, correct path — no gap. |
| Decisions table row 3 | "No spec change — Specs already document the intended slug-based key format everywhere. The UUID-based interim state is an implementation artifact, not a design decision." | All relevant specs (`spec-state-schema.md`, `spec-web.md`, `spec-dashboard.md`) document slug-based key formats consistently. | REFLECTED | — | The claim is verified: specs uniformly describe the slug-based key scheme as the target design. There is no statement in any spec that would need to be contradicted or overwritten to reflect this interim UUID workaround. A developer implementing from the spec alone would correctly target slug-based keys — which is the intended end state. |
| Overview / Motivation (background) | "`mclaude-projects` and `mclaude-sessions` KV buckets still use UUID-prefixed keys (`{userUUID}.{projectUUID}`)" | `docs/spec-state-schema.md` documents these buckets with slug-based key formats — i.e. the spec describes the *intended* post-migration state, not the current data shape. | REFLECTED | — | The spec correctly describes the target key format (slug-based). The mismatch described in the ADR is between current data and the spec's intended design — a code gap, not a spec gap. The ADR's interim fix is the minimum necessary to restore dashboard function without changing the spec. |
| Component Changes | "`mclaude-web`: `App.tsx` — pass `authState.userId` (not `authState.userSlug`) as `userSlug` to `SessionStore` constructor." | No spec describes the specific argument wiring in `App.tsx` (`authState.userId` vs `authState.userSlug`). `spec-web.md` describes `SessionStore` watches at a high level (buckets watched, reconnect behavior) without specifying what value is passed for the user identifier in the interim state. | REFLECTED | — | This is an implementation detail of a temporary workaround. Specs describe *intended* behavior (slug-keyed), not interim-fix wiring. No spec update is warranted because encoding "pass UUID temporarily" would introduce deliberate spec debt. |

---

### Phase 2 — Summary

```markdown
### Summary

- Reflected: 5
- Gap: 0
- Partial: 0
```

---

## Verdict

CLEAN — 5 ADR decisions reflected in spec (or correctly absent from spec), 0 gaps.

**Verification of ADR's "No spec changes" claim:**

The claim is **valid**. The ADR's decisions are all either:
1. Consistent with what the specs already document (slug-based KV key formats as the target design), or
2. Deliberately absent from the spec because they describe a temporary implementation workaround (UUID-as-userSlug interim state) that should not be codified in the living spec.

The specs uniformly and correctly describe the intended slug-based key scheme for `mclaude-projects`, `mclaude-sessions`, and `mclaude-hosts` KV buckets. ADR-0048's UUID workaround for `mclaude-projects` and `mclaude-sessions` watches is an implementation artifact that keeps the data working until a separate KV migration ADR is written — it is explicitly not a design decision and does not warrant a spec change.
