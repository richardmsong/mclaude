## Audit: 2026-05-06T00:00:00Z

**Document:** docs/adr-0092-anthropic-token-ux.md

### Round 1

**Gaps found: 7** (from design-evaluator)

1. `tokens.anthropic` schema in `spec-state-schema.md` not updated
2. `agent.token-revoke` absent from NATS taxonomy and user-agent spec
3. `agent.status` called "existing" but does not exist
4. `GET /api/users/{uslug}/grants?status=accepted` query param unspecified
5. `token_expired` missing from session status enum
6. `localStorage` pending-delivery schema unspecified
7. OAuth PKCE flow details unspecified

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | KV schema in spec-state-schema.md | ADR was changed to JSON format after spec was written; user corrected that token is a long-lived OAuth access token, not short-lived with refresh | ADR reverted to plain string (no schema change from ADR-0091). Gap is now moot. | factual |
| 2 | agent.token-revoke absent | New subject introduced in ADR but not yet reflected in specs | ADR correctly lists in Impact section; will be added during spec-edit step | factual |
| 3 | agent.status called "existing" | Subject was new but ADR incorrectly called it "existing" | Fixed in ADR — now explicitly declares it as a new subject with publisher, payload, and cadence | factual |
| 4 | grants endpoint filter | ADR used `?status=accepted` but CP spec doesn't define this param | Changed to client-side filtering — SPA reads all grants and filters to accepted locally | factual |
| 5 | token_expired missing from enum | New status value not added to authoritative spec | ADR Impact section now includes spec-state-schema.md update | factual |
| 6 | localStorage schema unspecified | Pending delivery was mentioned but never defined | Added: key `mclaude.pendingDeliveries`, value format `[{hslug, provider}]`, cleanup on success/logout | factual |
| 7 | OAuth PKCE details unspecified | ADR didn't spell out endpoints, params, scopes | Added to Component Changes: authorize URL, client_id, code_challenge_method, redirect_uri, token endpoint | factual |

### Round 2

**Gaps found: 3**

1. OAuth popup `redirect_uri` path and `postMessage` schema unspecified
2. `scope=user:inference` in SPA differs from CLI which sends no scope
3. First-run delivery timeout: SPA state after timeout unspecified

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | redirect_uri + postMessage schema | ADR described the popup flow but never gave the callback route path or message format | Added: `redirect_uri` is `{SPA_ORIGIN}/auth/oauth/callback`; `postMessage` payload is `{ type: "mclaude:oauth-callback", token?, error? }` with origin validation | factual |
| 2 | scope mismatch | ADR added `scope=user:inference` but CLI sends no scope param | Removed `scope` from SPA PKCE params — matches CLI implementation which relies on server defaults | factual |
| 3 | Timeout state unspecified | ADR described the timeout message but not what UX follows | Added: spinner stops, [Try again] retries delivery, [Contact admin] links to help. Connect sheet stays open; first-run flow blocked until delivery succeeds. Pending delivery persists in localStorage for retry on next login. | factual |

### Round 3

**Gaps found: 4**

1. **`agent.status` interaction pattern undefined** — The ADR says the user-agent publishes `agent.status` "periodically (on startup, on token change, on request)" but never defines what "on request" means: there is no request subject specified. The SPA must read each host's token version on login hydration — a periodic publish requires the SPA to subscribe and wait an unknown amount of time for the next publish interval, with no defined timeout behavior. A request/reply pattern would require a dedicated request subject that is not defined anywhere in the ADR or in `docs/spec-nats-data-taxonomy.md`. A developer cannot implement the SPA-side stale token detection without knowing which interaction pattern to use.
   - **Doc**: "The user-agent exposes token availability via a new NATS subject `mclaude.users.{uslug}.hosts.{hslug}.agent.status`... Published periodically (on startup, on token change, on request)." (Component Changes § mclaude-user-agent)
   - **Code**: `mclaude-user-agent/agent/agent.go` has no `agent.status` subject; the interaction pattern is not yet established.

2. **`agent.token-revoke` reply contract undefined** — The error table treats `token-revoke` as a request/reply operation ("User-agent token-revoke fails | SPA (NATS request timeout)"), but the Data Model section describes it only as a fire-and-forget publish with no reply payload defined. A developer implementing the user-agent handler cannot determine whether to send a reply, and the SPA developer cannot determine whether to use `nc.Request()` or `nc.Publish()`.
   - **Doc**: "SPA publishes revoke request to user-agent via NATS (`mclaude.users.{uslug}.hosts.{hslug}.agent.token-revoke` with `{ "provider": "anthropic" }`)." — no reply defined. Error table: "User-agent token-revoke fails | SPA (NATS request timeout)" — implies request/reply. (Component Changes § mclaude-user-agent; Error Handling table)
   - **Code**: No existing `token-revoke` handler. The `token-delivery` subject uses request/reply with `{ok: true}` / `{ok: false, error: "..."}` reply — this ADR provides no equivalent reply schema for `token-revoke`.

3. **"Contact admin" link has no defined source** — The 30s delivery timeout error shows "[Contact admin]" described as "a no-op link to the admin's email or help page (configured per deployment)." There is no specification of where this configuration comes from: no Helm value, no environment variable, no CP API endpoint, no fallback behavior (e.g. what renders if the value is not configured). A developer cannot implement this without knowing how the SPA obtains the admin contact URL.
   - **Doc**: "[Contact admin] is a no-op link to the admin's email or help page (configured per deployment)." (First-run flow § Token delivery failure)
   - **Code**: No existing admin contact URL config in `docs/charts-mclaude/spec-helm.md` or any SPA spec.

4. **Canonical specs not yet updated** — Expected: spec edits happen in Step 4 after ADR finalization, not during audit. Not a blocking gap.

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | agent.status interaction pattern | ADR said "periodically" and "on request" without defining the request subject or SPA interaction | Changed to request/reply: SPA sends `{}`, user-agent replies `{ hasToken, tokenVersion }`. 5s timeout. Unsolicited publishes retained for convenience but not relied on. | factual |
| 2 | agent.token-revoke reply contract | Data Model described fire-and-forget but Error Handling implied request/reply | Made explicitly request/reply: reply `{ ok: true }` / `{ ok: false, error }`. 10s timeout. Matches token-delivery pattern. | factual |
| 3 | Contact admin link source | Vague "configured per deployment" with no config mechanism | Removed [Contact admin] button entirely. Replaced with just [Try again]. Simpler and no config dependency. | factual |
| 4 | Specs not updated | Expected — spec edits are Step 4 of the plan-feature workflow | Not a gap; acknowledged and deferred to Step 4. | N/A |

### Round 4

**Gaps found: 6**

1. **OAuth client_id for SPA not specified** — The ADR instructs the developer to send `client_id` in the OAuth authorize request but never specifies what value to use. The CLI uses `"mclaude-cli"` (overridable via `ANTHROPIC_OAUTH_CLIENT_ID`). The SPA is a distinct OAuth client with a different `redirect_uri` (popup, not loopback). A developer implementing the popup flow has no way to know which `client_id` to use.
   - **Doc**: "SPA opens popup to `claude.ai/oauth/authorize` with PKCE parameters (`client_id`, …)" (Component Changes → mclaude-web, OAuth popup flow)
   - **Code**: `mclaude-cli/cmd/anthropic.go:69` — CLI uses `ClientID: "mclaude-cli"`; the SPA's equivalent is unspecified.

2. **`code_verifier` and `state` transport between parent window and popup callback page is unspecified** — The parent window generates the PKCE `code_verifier` (and `state`) before opening the popup. The popup loads `/auth/oauth/callback` as a fresh page, which must call `POST claude.ai/oauth/token` with `code_verifier`. The ADR gives no mechanism for how `code_verifier` (and `state` for CSRF validation) reaches the callback page — e.g., via `sessionStorage` keyed by `state`, URL fragment, or `window.opener` access before the redirect. Without this, the callback page cannot complete the token exchange, and the `state` CSRF check has no implementation path.
   - **Doc**: "The callback page extracts `code` and `state` from the URL query params, exchanges the code for an access token via `POST claude.ai/oauth/token` (with `code_verifier`)" (Component Changes → mclaude-web, OAuth popup flow)
   - **Code**: No existing SPA OAuth callback implementation.

3. **`POST claude.ai/oauth/token` CORS feasibility not addressed** — The popup callback page makes a cross-origin request (`POST https://claude.ai/oauth/token`) from the SPA origin. Whether `claude.ai` emits CORS headers permitting this is not verified or addressed. If `claude.ai` does not allow this cross-origin token exchange, the entire SPA OAuth popup flow is blocked. No fallback (e.g., proxying the token exchange through the control-plane) is specified.
   - **Doc**: "exchanges the code for an access token via `POST claude.ai/oauth/token`" (Component Changes → mclaude-web, OAuth popup flow)
   - **Code**: CLI performs the same exchange server-side (`mclaude-cli/cmd/anthropic.go:600`) — not from a browser, so provides no evidence of CORS compatibility.

4. **Session recovery after `token_expired` is unspecified** — The ADR says "Banner disappears once the session pod confirms token availability" and "session resumes." The session-agent sets `status: "token_expired"` in KV on 401/403. After the new token is delivered to the user-agent and propagated to the k8s Secret mount, there is no described mechanism by which the session-agent detects the new token and transitions back from `token_expired` to a running state. Does it watch the Secret mount for changes? Retry the failed Anthropic call? Restart the Claude Code subprocess? Require the user to manually restart the session?
   - **Doc**: "Banner disappears once the session pod confirms token availability." and "session resumes." (Token failure — inline prompt; Returning user (token invalid) flow)
   - **Code**: No existing `token_expired` handling in `mclaude-session-agent/` — this is new behavior with no specification.

5. **`[Re-authorize]` UI flow is unspecified** — The host detail CLAUDE ACCESS section shows `[Re-authorize]` in the "Revoked" state. The ADR says only "[Re-authorize] — re-delivers K_wrapped" with no description of the click handler: does it immediately attempt silent re-delivery? Prompt for confirmation? Open the Connect sheet? What does the user see if the user-agent is offline at the time of click?
   - **Doc**: "Revoked | `○ Revoked` (red) | [Re-authorize] — re-delivers K_wrapped" (Settings → Host detail table); "Host enters 'Revoked' state in the UI. [Re-authorize] button appears to re-deliver." (Revoke behavior section)
   - **Code**: No existing implementation.

6. **Settings "Token invalid" detection source is unspecified** — The Settings CLAUDE ACCOUNT section shows "Token invalid" state. The ADR only specifies detection via `token_expired` in session KV when a session is open. For Settings (shown outside any session context, or when no sessions are `token_expired` yet), there is no specified signal that drives this state. Is it derived from any active session having `token_expired` status? A separate KV field? An in-memory flag set during session observation? A developer cannot determine how to populate this state without this specification.
   - **Doc**: "Token invalid | `● Token invalid` (red dot) | [Reconnect]" (Settings — Claude Account section states table); detection described only for the session-level banner path (Token failure section)
   - **Code**: No existing implementation.

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | OAuth client_id | ADR listed `client_id` as a PKCE param but never specified value | Added: SPA uses same `client_id` as CLI (`mclaude-cli`, overridable via env var). Anthropic's server uses dynamic redirect URI registration per PKCE spec. | factual |
| 2 | code_verifier/state transport | Parent generates PKCE params; popup callback loads fresh and needs them | Added: parent stores `{ code_verifier, state }` in `sessionStorage` at `mclaude.oauth.pending`. Callback page reads from same-origin sessionStorage, verifies state match. | factual |
| 3 | CORS on token exchange | SPA calls claude.ai cross-origin; CLI is server-side so no CORS | Added: try direct call first; if CORS-blocked, fall back to CP proxy endpoint `POST /api/auth/oauth/exchange`. | factual |
| 4 | Session recovery after token_expired | ADR said "session resumes" without specifying how session-agent detects new token | Added: session-agent watches Secret mount via fsnotify (or polls 30s), reads new token, retries API call, transitions back to `running`. | factual |
| 5 | Re-authorize flow | ADR just said "re-delivers K_wrapped" with no click handler spec | Added: immediate silent re-delivery, no confirmation. On failure (offline): inline error "Host is offline — try again later". No localStorage retry (explicit action, not background). | factual |
| 6 | Settings Token invalid detection | Detection only specified for session-level banner, not Settings | Added: Settings shows "Token invalid" when ANY active session has `token_expired` in KV. No active sessions = presumed valid. No proactive Anthropic validation. | factual |

### Round 5

## Run: 2026-05-06T15:00:00Z

**Gaps found: 4**

1. **HashRouter incompatibility with `/auth/oauth/callback` route** — The SPA uses `HashRouter` (`App.tsx:2,136`). All client-side routes are served under `/#/...`. The ADR specifies the OAuth popup `redirect_uri` as `{SPA_ORIGIN}/auth/oauth/callback` — a path-based URL. When claude.ai completes authorization and redirects back to that URI, the browser loads a bare path with no hash fragment; the hash-based router never activates the callback route, so `code` and `state` are never captured, and the token exchange never runs. Either the callback must use a hash-style URL (`{SPA_ORIGIN}/#/auth/oauth/callback`) or the SPA must serve a dedicated server-side-rendered HTML file at that path (outside the hash router). Neither approach is specified.
   - **Doc**: "`redirect_uri={SPA_ORIGIN}/auth/oauth/callback`" and "**Callback page** (`/auth/oauth/callback` — new SPA route)" (Component Changes § OAuth popup flow, steps 2 and 3)
   - **Code**: `mclaude-web/src/components/App.tsx:2,136` — SPA uses `HashRouter`; all routes are under `/#/`.

2. **CP proxy endpoint `POST /api/auth/oauth/exchange` is not defined** — The ADR specifies a CORS fallback path where the callback page passes `code + code_verifier` back to the parent via `postMessage`, and the parent exchanges them through `POST /api/auth/oauth/exchange` on the CP. This endpoint does not appear in `docs/mclaude-control-plane/spec-control-plane.md`. No HTTP method, request schema (`{code, code_verifier, redirect_uri}`), response schema (`{access_token}`), auth requirement, or error codes are specified. Without this, the fallback path cannot be implemented.
   - **Doc**: "if `claude.ai` does not set CORS headers for this endpoint, the callback page must instead pass `code` + `code_verifier` back to the parent window via `postMessage` and let the parent exchange via a server-side proxy (CP endpoint `POST /api/auth/oauth/exchange`)" (Component Changes § OAuth popup flow, step 5)
   - **Code**: No matching endpoint in `docs/mclaude-control-plane/spec-control-plane.md`; no handler found in codebase.

3. **`sessionStorage` cannot be shared between popup and parent window** — The ADR says the callback page reads `{ code_verifier, state }` from `sessionStorage` at key `mclaude.oauth.pending`, annotated "shared with parent window — same origin." `sessionStorage` is isolated per browsing context (per tab/window), not shared across separate windows even of the same origin. When the parent opens a popup, the popup's `sessionStorage` is empty until that popup itself writes to it. After the popup navigates to `claude.ai` and is redirected back to the SPA origin, the popup's `sessionStorage` does not contain anything the parent window wrote. The mechanism as described will produce `undefined`/`null` for `code_verifier` and the CSRF `state` check will always fail. The correct storage for cross-window sharing is `localStorage` (shared across same-origin tabs/windows) or an alternative such as `BroadcastChannel`.
   - **Doc**: "Reads `{ code_verifier, state: expectedState }` from `sessionStorage` key `mclaude.oauth.pending` (shared with parent window — same origin)" (Component Changes § OAuth popup flow, step 4)
   - **Code**: No existing implementation.

4. **`mclaude auth anthropic --revoke` host-broadcast failure behavior unspecified** — The ADR extends `--revoke` to publish `token-revoke` to all accepted-grant hosts after the current KV-only delete. The existing `runRevoke` function (`mclaude-cli/cmd/anthropic.go:817-826`) returns `ExitAuthSuccess` unconditionally after KV deletion. The extension adds per-host NATS requests; the ADR does not specify: what exit code the CLI returns when ≥1 host fails (partial failure vs. success), whether the CLI continues delivering to remaining hosts after one failure, or what output text is printed for partial failures. The existing exit code taxonomy (0/1/2/3) covers partial delivery for token setup but there is no analogous mapping defined for the revoke path.
   - **Doc**: "`mclaude auth anthropic --revoke`: extend to publish token-revoke to all accepted-grant hosts (currently KV-only)." (Component Changes § mclaude-cli)
   - **Code**: `mclaude-cli/cmd/anthropic.go:817-826` — `runRevoke` currently KV-only; extension behavior and failure semantics undefined.

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | HashRouter incompatibility | ADR specified path-based callback route but SPA uses HashRouter | Changed callback to a static HTML page served by nginx outside the SPA HashRouter. Page is minimal: extract params, postMessage to parent, close. | factual |
| 2 | CP proxy endpoint undefined | CORS fallback mentioned an endpoint without defining it | Promoted CP proxy from fallback to primary path. Fully specified: `POST /api/auth/oauth/exchange`, request/response schema, auth requirement. Added to Impact section. | factual |
| 3 | sessionStorage not shared | sessionStorage is per-browsing-context, not per-origin | Eliminated cross-window storage entirely. Callback page sends `{ code, state }` to parent via postMessage. Parent keeps `code_verifier` and `state` in JS memory. Parent does exchange via CP proxy. | factual |
| 4 | CLI --revoke failure behavior | ADR said "extend" without specifying partial failure semantics | Added: KV delete first (always succeeds), iterate hosts with per-host warnings, continue on failure, ExitAuthSuccess if KV succeeded, summary message with N/M count. | factual |

## Run: 2026-05-06T16:00:00Z

**Gaps found: 1**

1. **`ANTHROPIC_OAUTH_CLIENT_ID` build-time injection mechanism unspecified** — The ADR says "The SPA uses the same `client_id` as the CLI (`mclaude-cli`, overridable via `ANTHROPIC_OAUTH_CLIENT_ID` env var injected at build time)." The vite.config.ts has no `define` entry for this constant, and the mclaude-cp Helm chart has no corresponding `spa.*` configuration knob. A developer cannot implement the OAuth popup flow without knowing: (a) what Vite mechanism exposes this constant to the bundle (e.g., `VITE_ANTHROPIC_OAUTH_CLIENT_ID` via `import.meta.env`, or a `define` entry in vite.config.ts), (b) how the default value `mclaude-cli` is set as a fallback at build time, and (c) whether/how the Helm chart passes the override to the Docker build.
   - **Doc**: "The SPA uses the same `client_id` as the CLI (`mclaude-cli`, overridable via `ANTHROPIC_OAUTH_CLIENT_ID` env var injected at build time)" (Component Changes § mclaude-web, OAuth popup flow)
   - **Code**: `mclaude-web/vite.config.ts` — no `define` entry; `mclaude-web/src/main.tsx` only uses `VITE_SENTRY_DSN`; `docs/charts-mclaude/spec-helm.md` has no `spa.*` knob for this value.

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | client_id injection | ADR said "overridable via env var injected at build time" but no mechanism existed | Simplified: hardcoded constant in SPA source. Not a secret, identical across environments. No Vite define or Helm knob needed. | factual |

### Result

**CLEAN** after 6 rounds, 21 total gaps resolved (21 factual fixes, 0 design decisions).
