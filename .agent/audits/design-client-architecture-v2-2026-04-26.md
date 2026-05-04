## Run: 2026-04-26

**Gaps found: 6**

1. `sessions.restart` vs `sessions.resume` — ADR claims system uses `resume` but code uses `restart`
2. `UserImageBlock` field names wrong — ADR says `mediaType`/`data`, code uses `mimeType`/`dataUrl`
3. `SessionKVState` missing `hostSlug`, `createdAt`, `joinWorktree`
4. `createProject` subject scope contradicts spec-state-schema (user-level vs host-scoped)
5. Host-scope constructor migration underspecified in Implementation Plan
6. HeartbeatMonitor replacement scope incomplete in Implementation Plan

**Round 1 fixes applied by master session (factual):**
- Gap 1: Fixed — both `restart` and `resume` exist. `restart` is in subj.ts/code. `resume` is in spec-state-schema MCLAUDE_API stream. ADR updated to list both.
- Gap 2: Fixed — UserImageBlock fields corrected to match code (`mimeType`, `dataUrl`)
- Gap 3: Fixed — noted these fields exist in spec but not yet in code; Implementation Plan updated
- Gap 4: Fixed — clarified as ADR-0035 transition: current code uses user-level, target state is host-scoped
- Gap 5: Fixed — Implementation Plan expanded to cover all call-site changes
- Gap 6: Fixed — Implementation Plan expanded to cover deletion + wiring changes

## Run: 2026-04-26 (round 2)

**Gaps found: 4**

1. SessionListVM constructor wrong dependency (lifecycleStore vs HostStatusStore) — Fixed: constructor updated to `(sessionStore, hostStatusStore, natsClient, userSlug)`
2. `set_max_thinking_tokens` payload field (`max_thinking_tokens` vs `budget`) — Fixed: ADR corrected to `budget`
3. `createProject` missing `gitIdentityId` param — Fixed: added as optional param
4. `deleteProject` listed but absent from code — Fixed: removed from ADR (not implemented, not in scope)
