## Run: 2026-05-06T00:00:00Z

ADR: docs/adr-0092-anthropic-token-ux.md
Status: draft

Specs evaluated:
- docs/ui/spec-first-run-flow.md
- docs/ui/mclaude-web/spec-settings-web.md
- docs/mclaude-web/spec-web.md
- docs/spec-state-schema.md
- docs/spec-nats-data-taxonomy.md
- docs/mclaude-session-agent/spec-session-agent.md
- docs/mclaude-user-agent/spec-user-agent.md
- docs/mclaude-cli/spec-cli.md
- docs/mclaude-control-plane/spec-control-plane.md
- docs/ui/mclaude-web/spec-host-picker.md (cross-spec check)

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| L21 | "First-run gate: Connect sheet before everything (before project/session creation)" | spec-first-run-flow.md §Token Gate | REFLECTED | — | "Before the first-run flow, the SPA checks for a Claude token. If `tokens.anthropic` is absent, the Connect sheet (bottom sheet) auto-opens." |
| L22 | "Token entry: OAuth PKCE primary ('Sign in with Claude' button); manual paste collapsed under 'Or enter a token manually'" | spec-web.md §Connect Sheet | REFLECTED | — | Spec: "Sign in with Claude button (opens OAuth PKCE popup)" + "Collapsed 'Or enter a token manually' `<details>`" |
| L23 | "Connect UI: Bottom sheet…OAuth opens popup window, main SPA stays open" | spec-web.md §Connect Sheet; spec-web.md §OAuth Popup Flow | REFLECTED | — | Both described accurately in spec-web.md |
| L24 | "Default platform auto-delivery: Auto-deliver K_wrapped silently on first-run after OAuth/paste completes" | spec-first-run-flow.md §Token Gate | REFLECTED | — | "After the token is written to KV, the SPA auto-delivers K_wrapped to the default platform." |
| L25 | "Token failure UX: Inline banner at point of failure (session view) + state in Settings. Session-agent reports `status: 'token_expired'` via KV." | spec-web.md §Token Failure Banner; spec-session-agent.md §Token failure detection | REFLECTED | — | Both specs describe the detection and banner. |
| L26 | "Settings placement: CLAUDE ACCOUNT section between CONNECTION and ADMINISTRATION" | spec-settings-web.md §Screen: Settings layout | REFLECTED | — | Layout diagram shows CLAUDE ACCOUNT between CONNECTION and ADMINISTRATION. |
| L27 | "Host authorization: Always automatic — all hosts get K_wrapped auto-delivered after grant acceptance. No explicit [Authorize] step." | spec-settings-web.md §Host Detail: CLAUDE ACCESS sub-section | REFLECTED | — | States table has no [Authorize] row; Pending delivery state says "Auto-delivers when user-agent online." |
| L28 | "[Revoke] in host detail with confirmation dialog showing session impact" | spec-settings-web.md §Host Detail: CLAUDE ACCESS sub-section | REFLECTED | — | "[Revoke]: confirmation dialog with session impact count." |
| L29 | "Re-delivery on reconnect: SPA auto-re-delivers to all accepted-grant hosts after new token written" | spec-web.md §Re-delivery on Reconnect | REFLECTED | — | Spec describes full re-delivery flow including endpoint, pubkey fetch, wrap, deliver, and progress indicator. |
| L30 | "Disconnect scope: KV + all hosts. No Anthropic-side revocation (no public endpoint exists)." | spec-settings-web.md §Section: CLAUDE ACCOUNT | REFLECTED | — | "[Disconnect]: Deletes from KV + publishes token-revoke to all accepted-grant hosts. No Anthropic-side revocation…" |
| L31 | "Token KV format: Plain string (unchanged from ADR-0091)" | spec-state-schema.md §mclaude-user-info-{uslug} | REFLECTED | — | Table shows `tokens.anthropic` value as "Anthropic OAuth access token (plaintext string)". |
| L32 | "Admin: default platform opt-out: Not needed — no admin toggle" | No spec mentions an admin toggle | REFLECTED | — | Absence is correct; no spec describes any admin toggle. |
| L65-69 | "[✕] disabled during first-run (token is mandatory to proceed). On reconnect, [✕] is enabled." | spec-web.md §Connect Sheet | REFLECTED | — | "On first-run: [✕] disabled. On reconnect: [✕] enabled." |
| L66 | "popup completes → parent window detects success via `postMessage` → sheet updates to show 'Connected' → auto-dismisses after 1s" | spec-web.md §Connect Sheet | REFLECTED | — | "On success: sheet shows 'Connected' and auto-dismisses after 1 second." |
| L66 | "Paste: write-only password input…Input cleared immediately. Sheet updates inline — no popup needed." | spec-web.md §Connect Sheet | REFLECTED | — | "password input + [Connect] button. Input cleared immediately after submit." |
| L67 | "Error states: OAuth popup blocked → 'Allow popups for this site and try again'. OAuth failure → 'Sign-in failed — try again or enter a token manually'. Paste failure → 'Please enter a valid token'." | spec-web.md §Connect Sheet | REFLECTED | — | Spec lists all three error messages verbatim. |
| L74 | "SPA stores delivery as pending in `localStorage` at key `mclaude.pendingDeliveries` (value: JSON array of `{ hslug, provider }` objects)" | spec-web.md §Pending Delivery Retry; spec-state-schema.md §Client-Side State | REFLECTED | — | Both specs document key and schema exactly. |
| L74 | "session creation is blocked until delivery succeeds — spinner with 'Setting up your environment…' and a timeout message after 30s" | spec-first-run-flow.md §Token Gate | REFLECTED | — | "Session creation is blocked until delivery succeeds (30s timeout with [Try again])." |
| L84 | "[Try again] retries delivery immediately (resets the 30s timer). The Connect sheet remains open behind the error." | spec-first-run-flow.md §Token Gate §Delivery timeout behavior | REFLECTED | — | "After timeout: spinner stops, error message with [Try again] button appears. [Try again] retries delivery immediately and resets the 30s timer. The Connect sheet remains visible behind the error." |
| L84 | "If the user dismisses the Connect sheet (navigates away), the pending delivery stays in `localStorage` and retries on next login. The first-run flow does not proceed until delivery succeeds." | spec-first-run-flow.md §Token Gate §Delivery timeout behavior | REFLECTED | — | "If the user dismisses the Connect sheet (navigates away), the pending delivery is written to `localStorage`…retried on next login. The first-run flow (project + session creation) does not proceed until delivery succeeds." |
| L88-108 | "Settings → CLAUDE ACCOUNT section layout: Connected/Token invalid/Not connected states with actions" | spec-settings-web.md §Section: CLAUDE ACCOUNT | REFLECTED | — | Matching states table present. |
| L118 | "'Token invalid' detection: Settings shows 'Token invalid' when ANY active session reports `status: 'token_expired'`…If no sessions are active, Settings shows 'Connected'" | spec-settings-web.md §Section: CLAUDE ACCOUNT | REFLECTED | — | "'Token invalid' is shown when any active session reports `status: 'token_expired'` in session KV. No active sessions = presumed valid ('Connected')." |
| L121-124 | "[Disconnect]: 1. Delete `tokens.anthropic` from KV. 2. Publish `token-revoke` to all accepted-grant hosts. 3. No Anthropic-side token revocation. Users can revoke manually at `claude.ai/settings/claude-code`." | spec-settings-web.md §Section: CLAUDE ACCOUNT | REFLECTED | — | "No Anthropic-side revocation — the token remains valid at Anthropic until natural expiry (~1 year). Users can manually revoke at `claude.ai/settings/claude-code`." |
| L128-141 | "Token failure banner: inline banner at top of session view…`--warning` background, `--text` foreground. [Reconnect] opens Connect sheet. Banner disappears once session pod confirms token availability." | spec-web.md §Token Failure Banner | REFLECTED | — | "renders an inline banner at the top of the session view (`--warning` background, `--text` foreground)". Banner clears condition also stated. |
| L145 | "Extends the existing host detail screen (spec-host-picker.md § Settings → Hosts → host detail) with Claude authorization status" | spec-host-picker.md:73 | PARTIAL | SPEC→FIX | spec-host-picker.md line 73 says "CLAUDE ACCESS sub-section (see `spec-settings-web.md` § Host Detail: CLAUDE ACCESS)" — it cross-references but does not include the content inline. A developer implementing the host detail screen from spec-host-picker.md alone sees only the cross-reference hint, not the actual states/behavior. The ADR says this spec should contain the section, not just a pointer. The content should either live in spec-host-picker.md or the cross-reference should be more explicit (e.g. "defined in spec-settings-web.md"). Current state is a pointer only. |
| L147 | "All hosts receive K_wrapped automatically after grant acceptance — no explicit [Authorize] step" | spec-settings-web.md §Host Detail: CLAUDE ACCESS | REFLECTED | — | States table confirms no [Authorize] action. |
| L168-176 | "Claude Access states table: Authorized / Pending delivery / Awaiting acceptance / Revoked / Host offline" | spec-settings-web.md §Host Detail: CLAUDE ACCESS | REFLECTED | — | All 5 states present in spec with matching descriptions. |
| L193-197 | "[Revoke]: SPA publishes `token-revoke` request/reply (`mclaude.users.{uslug}.hosts.{hslug}.agent.token-revoke` with `{ 'provider': 'anthropic' }`). User-agent deletes provider key from `user-tokens-{uslug}` Secret." | spec-user-agent.md §NATS Subjects (token-revoke row) | REFLECTED | — | Spec: "Payload: `{provider: 'anthropic'}`. User-agent deletes the provider key from `user-tokens-{uslug}` Secret." with 10s timeout. |
| L197 | "[Re-authorize]: immediate silent re-delivery, no confirmation. On failure (user-agent offline): SPA shows inline error 'Host is offline — try again later'. Pending delivery is NOT written to localStorage (unlike first-run)." | spec-settings-web.md §Host Detail: CLAUDE ACCESS | REFLECTED | — | "[Re-authorize]: immediate silent re-delivery (no confirmation)… On failure…inline error 'Host is offline — try again later'… Pending delivery is NOT written to localStorage (unlike first-run)." |
| L201-208 | "Token reconnect re-delivery: Progress: 'Updating N hosts…' → 'Done' (or 'N of M hosts updated — N offline hosts will receive the token on your next visit')" | spec-web.md §Re-delivery on Reconnect | REFLECTED | — | "Progress states: 'Updating N hosts…' → 'Done' (all succeeded) or 'N of M hosts updated — N offline hosts will receive the token on your next visit' (partial failure)." |
| L210 | "SPA detects stale tokens via `agent.status` subject: `{ 'hasToken': true, 'tokenVersion': '<sha256-prefix>' }`. SPA compares first 8 hex chars of sha256(token). Mismatch → re-deliver." | spec-web.md §Stale Token Detection; spec-user-agent.md §NATS Subjects (status row) | REFLECTED | — | Both specs describe the mechanism consistently. |
| L261-277 | "OAuth popup flow: client_id hardcoded as `mclaude-cli`. No scope parameter. Callback is static HTML served by nginx outside HashRouter. Posts `{type: 'mclaude:oauth-callback', code, state}` to window.opener." | spec-web.md §OAuth Popup Flow | REFLECTED | — | All details present in spec. |
| L276 | "Token exchange: Parent calls CP proxy `POST /api/auth/oauth/exchange` with `{ code, code_verifier, redirect_uri, client_id }`. Returns `{ access_token }`." | spec-control-plane.md HTTP Endpoints table | REFLECTED | — | CP spec lists the endpoint with exact request/response shape: "Request: `{code, code_verifier, redirect_uri, client_id}`…Returns `{access_token}`…CP does not store the token." |
| L278 | "Auth: requires valid mclaude user JWT. CP does not store the token." | spec-control-plane.md §POST /api/auth/oauth/exchange | REFLECTED | — | Spec row states "Auth: requires valid mclaude user JWT. CP does not store the token." |
| L290-292 | "session-agent: Report `status: 'token_expired'` when Anthropic API returns 401/403." | spec-session-agent.md §Token failure detection | REFLECTED | — | "If the Anthropic API returns 401/403 during a session, the session-agent sets `status: 'token_expired'` in session KV." |
| L292-293 | "Recovery: session-agent watches `/secrets/tokens/` via `fsnotify` (or polls every 30s). When token file updated, reads new token, retries, transitions back to `running`." | spec-session-agent.md §Token failure detection | REFLECTED | — | "Recovery: session-agent watches the mounted Secret directory (`/secrets/tokens/`) via `fsnotify` (or polls every 30s)… retries the failed API call, and transitions back to `running`." |
| L296-297 | "`agent.token-revoke`: request/reply. SPA sends `{ 'provider': 'anthropic' }`, user-agent replies `{ok: true}` or `{ok: false, error: '...'}`. SPA uses 10s timeout." | spec-user-agent.md §NATS Subjects; spec-nats-data-taxonomy.md §agent.token-revoke | REFLECTED | — | Both specs document the subject with matching payload and timeout. |
| L297-298 | "`agent.status`: request/reply. SPA sends `{}`, user-agent replies `{hasToken: bool, tokenVersion: '<first-8-hex>'}`. 5s timeout. User-agent also publishes unsolicited on startup and token change." | spec-user-agent.md §NATS Subjects (status row) | REFLECTED | — | All details match exactly. |
| L300-301 | "`mclaude auth anthropic --revoke`: delete from KV, then iterate accepted-grant hosts sending token-revoke (10s timeout per host). Exit code: `ExitAuthSuccess` if KV delete succeeded. Summary: 'Token removed. Revoked on N/M hosts.' Partial failure: 'Token removed. Revoked on N/M hosts.' All fail: 'Token removed from account. Warning: N hosts could not be reached — token may still be cached on those hosts.'" | spec-cli.md §mclaude auth anthropic | REFLECTED | — | CLI spec at line 155: "all hosts succeeded → 'Token removed. Revoked on N/N hosts.'. Partial failure → 'Token removed. Revoked on N/M hosts.'. All hosts failed → 'Token removed from account. Warning: N hosts could not be reached — token may still be cached on those hosts.'". Exact match. |
| L324-330 | "`token_expired` added to canonical `SessionState.status` enum in spec-state-schema.md" | spec-state-schema.md §mclaude-sessions-{uslug} status enum | REFLECTED | — | "`token_expired` — model-provider token invalid (401/403 from Anthropic); session-agent sets this, SPA renders reconnect banner (ADR-0092)" present in enum. |
| L330-332 | "`mclaude.pendingDeliveries` localStorage schema: JSON array of `{ hslug, provider }`. Retried on login/NATS reconnect, cleared on logout." | spec-state-schema.md §Client-Side State | REFLECTED | — | Table at line 920 of spec-state-schema.md documents the key, value schema, behavior, and ADR attribution. |
| L336-344 | "Error Handling table: 'User-agent token-revoke fails' → user sees 'Could not reach {host} — token may still be active. Try again when the host is online.'" | spec-settings-web.md §Host Detail: CLAUDE ACCESS (Revoke row) | REFLECTED | — | spec-settings-web.md line 94: "On NATS timeout/failure: SPA shows inline error 'Could not reach {host} — token may still be active. Try again when the host is online.'" |
| L347-348 | "Security §Token in KV: protected by NATS RBAC + JetStream at-rest encryption. Plaintext string." | spec-state-schema.md §mclaude-user-info-{uslug} | REFLECTED | — | "JetStream at-rest encryption (`store_key` in NATS server config) is required." |
| L348 | "Security §Token version: 8-char hex prefix of sha256(token). Not reversible." | spec-user-agent.md §NATS Subjects (status row) | REFLECTED | — | "tokenVersion: '<first-8-hex-of-sha256>'" |
| L350 | "Security §No Anthropic-side revocation: token remains valid at Anthropic until ~1-year expiry after Disconnect." | spec-settings-web.md §Section: CLAUDE ACCOUNT | REFLECTED | — | Spec states this explicitly in the Disconnect description. |
| L351 | "Security §Pending deliveries in localStorage: contains only `{ hslug, provider }` — no token data." | spec-web.md §Pending Delivery Retry; spec-state-schema.md §Client-Side State | REFLECTED | — | Schema shows no token field. |
| L62 (NATS taxonomy) | `agent.token-revoke` and `agent.status` subjects listed under `agent.>` hierarchy | spec-nats-data-taxonomy.md §Resource Hierarchy | REFLECTED | — | Lines 67-68 of taxonomy spec list both subjects with correct descriptions. |
| L322 | "Falls under the existing `agent.>` wildcard in both user-agent and user JWT permissions." | spec-nats-data-taxonomy.md; spec-state-schema.md §User JWT permissions | REFLECTED | — | Taxonomy shows subjects under `agent.>`. User JWT table shows wildcard `mclaude.users.alice.hosts.*.>` pub/sub. |

### Phase 0b — Cross-Spec Consistency

The ADR introduces two NATS subject names (`agent.token-revoke`, `agent.status`) and the `token_expired` status value. Checked all spec files for inconsistency:

- `agent.token-revoke`: consistent across spec-user-agent.md, spec-nats-data-taxonomy.md, spec-settings-web.md, spec-cli.md.
- `agent.status`: consistent across spec-user-agent.md, spec-nats-data-taxonomy.md, spec-web.md.
- `token_expired`: consistent across spec-state-schema.md, spec-session-agent.md, spec-web.md, spec-settings-web.md.
- `mclaude.pendingDeliveries`: consistent across spec-web.md and spec-state-schema.md.
- `claude.ai/settings/claude-code`: present in both spec-settings-web.md and the ADR. Not mentioned in spec-web.md banner section (no gap — the banner doesn't reference the Anthropic revocation URL, only Settings does, which is correct).

No cross-spec inconsistencies found.

### Summary

- Reflected: 46
- Gap: 0
- Partial: 1

---

## Verdict

CLEAN (with one minor partial) — 46 ADR decisions reflected across 9 spec files.

**PARTIAL [SPEC→FIX]:**
`spec-host-picker.md:73` — "CLAUDE ACCESS sub-section (see `spec-settings-web.md` § Host Detail: CLAUDE ACCESS)" is a cross-reference pointer only. The ADR (line 145) says it "Extends the existing host detail screen (spec-host-picker.md)" — implying spec-host-picker.md should contain the section inline or at minimum a more prominent forward declaration. A developer implementing the host detail screen from spec-host-picker.md alone sees only a terse parenthetical cross-reference. Consider either embedding the CLAUDE ACCESS content in spec-host-picker.md or promoting the cross-reference to a named sub-section with explicit detail.
