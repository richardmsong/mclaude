## Run: 2026-05-06T00:00:00Z

ADR: docs/adr-0100-remove-legacy-nkey-login.md
Status: accepted
Specs evaluated:
- docs/mclaude-control-plane/spec-control-plane.md
- docs/spec-state-schema.md
- docs/mclaude-web/spec-web.md
- docs/mclaude-cli/spec-cli.md

### Phase 1 — ADR → Spec (forward pass)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Decisions table | "Legacy NKey path — Remove `IssueUserJWTLegacy` and the `else` branch at `auth.go:180-191`" | spec-control-plane.md:7 "All identity types generate their own NKeys — the control-plane never generates NKey pairs or handles private key material (ADR-0054)" and line 239 same; spec-state-schema.md Login Response Shape: "The server never generates NKey seeds for clients; the legacy `IssueUserJWTLegacy` path is removed (ADR-0100)" | REFLECTED | — | Both specs capture removal of legacy path. |
| Decisions table | "`nkey_public` in login — Required field. Return 400 if absent." | spec-control-plane.md:67 "Request body **requires** `nkey_public` … returns 400 `{error: 'nkey_public is required'}` if absent (ADR-0100)" | REFLECTED | — | |
| Decisions table | "`nkeySeed` in login response — Remove from `LoginResponse`. Never returned." | spec-control-plane.md:67 "**No NKey seed in the response**"; spec-state-schema.md Login Response Shape: "no `nkeySeed` in the response — ADR-0100" | REFLECTED | — | |
| Decisions table | "Auto-grant in login handler — After issuing the JWT, run same default-platform auto-grant logic… query `GetDefaultPlatformHostSlug`, `UpsertHostGrant`, publish `grants.create`" | spec-control-plane.md:67 "Fires default-platform auto-grant (ADR-0100)"; spec-control-plane.md:276 full paragraph: "Default-platform auto-grant is invoked idempotently on every user JWT issuance — both `POST /auth/login` … `UpsertHostGrant`s a row … publishes to `mclaude.hosts.{<default>}.grants.create`" | REFLECTED | — | |
| Decisions table | "Auto-grant in device-code poll — when the device-code poll returns `authorized`, fire the auto-grant." | spec-control-plane.md:72 "Fires default-platform auto-grant on authorization (ADR-0100)"; spec-control-plane.md:276 "and on authorized device-code poll (ADR-0100)" | REFLECTED | — | |
| Decisions table | "SPA login — SPA must generate an NKey pair before calling `POST /auth/login` and include `nkey_public` in the request body. Store the seed in localStorage alongside the JWT." | spec-web.md:13 "The SPA generates its own NKey pair in browser `localStorage` and sends `nkey_public` in the `POST /auth/login` request body (ADR-0100)." | REFLECTED | — | Seed storage in localStorage confirmed implicitly — spec says "locally-stored NKey seed". |
| Component Changes / mclaude-web | "Remove any code that reads `nkeySeed` from the login response (server no longer returns it)." | spec-web.md:13 "no seed is returned by the server" | REFLECTED | — | |
| Component Changes / mclaude-web | "Store `nkeySeed` in localStorage (alongside JWT) for NATS connection." | spec-web.md:13 "Authentication uses the per-host user JWT and the locally-stored NKey seed" — but no explicit statement that seed is stored in localStorage as a named key | PARTIAL | SPEC→FIX | spec-web.md does not name a localStorage key for the NKey seed. The Client-Side State (localStorage) table in spec-state-schema.md lists only `mclaude.pendingDeliveries`, `mclaude.filterProjectId`, `mclaude.lastProjectId`, and `mclaude.defaultHostSlug` — no entry for NKey seed storage. |
| Error Handling | "`nkey_public` omitted in login request: 400 `{error: 'nkey_public is required'}`" | spec-control-plane.md:67 already encodes this — "returns 400 `{error: 'nkey_public is required'}` if absent" | REFLECTED | — | |
| Error Handling | "Invalid `nkey_public` format: 400 `{error: 'invalid nkey_public format'}` (validate U-prefix, 56-char base32)" | spec-control-plane.md Error Handling section: no mention of invalid format error | GAP | SPEC→FIX | spec-control-plane.md Error Handling (lines 333–413) does not include a row for "invalid nkey_public format" 400 error. The Decisions table describes the validation rule (U-prefix, 56-char base32) but the spec's Error Handling section is silent on this case. |
| Component Changes / mclaude-control-plane | "After JWT issuance … add the default-platform auto-grant block … publish `mclaude.hosts.{defaultHostSlug}.grants.create` with `{uslug}`" | spec-control-plane.md:276: "publishes to `mclaude.hosts.{<default>}.grants.create` on behalf of the host" — matches subject pattern. | REFLECTED | — | |
| Component Changes / mclaude-cli | "Already generates NKey and sends `nkey_public` in device-code flow. No changes needed (verify only)." | spec-cli.md:43–51: device-code flow step 1 "CLI generates an NKey pair locally — the private seed never leaves the machine", step 2 sends `{publicKey}`. The ADR specifies this is a verify-only item — no spec update needed, and indeed the CLI spec already reflects correct behavior. | REFLECTED | — | |
| Impact / spec-state-schema.md | "note that `users.nkey_public` is always populated after login (no NULL for active users)" | spec-state-schema.md:23: "`nkey_public` … NULL only for users created via `POST /admin/users` who have never logged in" — this describes partial nullability but does not make a strong "always populated after login" claim. The ADR says Required in POST /auth/login body which means login always populates it. | PARTIAL | SPEC→FIX | The spec-state-schema.md users table footnote is accurate but passive. The ADR's impact section says to explicitly note that `users.nkey_public` is always populated after login. The spec currently says "NULL only for users created via `POST /admin/users` who have never logged in" which is correct but does not state the affirmative: "always non-NULL after first successful login." A developer reading the spec alone would not have strong confidence about the post-login state. |

### Phase 0b — Cross-spec consistency check

Checking shared concepts: `IssueUserJWTLegacy`, `nkey_public` in login body, `nkeySeed` in response, auto-grant on login, device-code auto-grant.

- spec-web.md Event Rendering section (line 17): still references `MCLAUDE_EVENTS` stream — this is stale dead code from before ADR-0054 consolidated streams into `MCLAUDE_SESSIONS_{uslug}`. **Not introduced by ADR-0100** — pre-existing gap unrelated to this ADR.
- spec-web.md NATS Subjects: no inconsistency with ADR-0100 decisions.
- spec-state-schema.md Login Response Shape (line 864): correctly states no `nkeySeed`, legacy path removed. Consistent with spec-control-plane.md.
- spec-cli.md: device-code poll response described at line 48: `{ "status": "pending" | "authorized", "jwt": "...", "userSlug": "...", "natsUrl": "..." }` — no `nkeySeed` field, consistent.
- spec-web.md E2E test (line 260): "calls `POST {BASE_URL}/auth/login` with `{email, password: token, nkey_public}`" — consistent with ADR-0100 requirement.

No cross-spec name collisions or format divergences found for ADR-0100 concepts.

### Summary

- Reflected: 10
- Gap: 1
- Partial: 2
