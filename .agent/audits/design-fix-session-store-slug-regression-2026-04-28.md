## Run: 2026-04-28T00:00:00Z

CLEAN — no blocking gaps found.

### Verification summary

**Design doc**: `docs/adr-0048-fix-session-store-slug-regression.md`

**Change scope**: Single line change in `mclaude-web/src/components/App.tsx` — the `SessionStore` constructor call must pass `authState.userId` (UUID) as its third argument instead of `authState.userSlug`.

**Codebase cross-checks performed**:

1. **`SessionStore` constructor** (`mclaude-web/src/stores/session-store.ts`):
   - Signature: `constructor(natsClient, userId: string, userSlug: string = userId)` — third param is `userSlug`, which is used as the KV watch prefix via `kvKeySessionsForUser(this.userSlug as UserSlug)`. Passing a UUID string is compatible at runtime (the code already uses `as UserSlug` cast). ✓

2. **`HeartbeatMonitor` constructor** (`mclaude-web/src/stores/heartbeat-monitor.ts`):
   - Signature: `constructor(natsClient, userId: string, userSlug: string = userId)` — third param is `userSlug`, used for `mclaude-hosts` KV watch. Keeping `authState.userSlug` (slug) is correct since `mclaude-hosts` entries are slug-keyed per ADR-0046. ✓

3. **Current `App.tsx` code**:
   - Both `SessionStore` and `HeartbeatMonitor` are created inside the `if (authState.status === 'authenticated' && authState.userId)` guard, so `authState.userId` is guaranteed non-null at the call site. ✓
   - Current code: `const store = new SessionStore(natsClient, authState.userId, uslug)` where `uslug = authState.userSlug ?? authState.userId`. The fix is to replace that third arg with `authState.userId` directly.

4. **KV key builders** (`mclaude-web/src/lib/subj.ts`):
   - `kvKeySessionsForUser(uslug)` returns `${uslug}.>` — passing UUID produces `{UUID}.>`, which matches existing KV entries keyed `{UUID}.{UUID}`. ✓
   - `kvKeyProjectsForUser(uslug)` returns `${uslug}.>` — same analysis applies. ✓

5. **`SessionListVM`** (`mclaude-web/src/viewmodels/session-list-vm.ts`):
   - Uses `userSlug` only for NATS subjects (not KV watch keys). It continues to receive `authState.userSlug ?? authState.userId` and is unaffected by the ADR-0048 change. ✓

6. **spec-state-schema.md cross-check**:
   - Spec documents `{uslug}.{hslug}.{pslug}.{sslug}` as the intended KV key format. ADR-0048 explicitly acknowledges this inconsistency as "an implementation artifact, not a design decision" and defers migration. The ADR correctly calls out "No spec change" — the interim UUID-prefix fix does not require a schema update. ✓

**Implementation is unambiguous**: A developer reading the ADR knows exactly what to change — one constructor call in `App.tsx` — and has no need to ask clarifying questions.
