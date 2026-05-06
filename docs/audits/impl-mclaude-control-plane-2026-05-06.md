## Run: 2026-05-06T00:00:00Z

Component: mclaude-control-plane
Scope: ADR-0092 — POST /api/auth/oauth/exchange proxy endpoint

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0092:104 / spec-control-plane:104 | `POST /api/auth/oauth/exchange` — endpoint exists on main port, protected by valid mclaude user JWT | server/server.go:29 | IMPLEMENTED | — | Route registered with `s.authMiddleware` wrapping `handleOAuthExchange` |
| adr-0092:279 | Request body: `{ "code": "...", "code_verifier": "...", "redirect_uri": "...", "client_id": "..." }` | server/oauth_exchange.go:28-33 | IMPLEMENTED | — | `OAuthExchangeRequest` struct matches exactly |
| adr-0092:279 | CP adds `grant_type=authorization_code` and forwards as form-encoded POST to Anthropic token endpoint | server/oauth_exchange.go:72-86 | IMPLEMENTED | — | `form.Set("grant_type", "authorization_code")` plus all four caller-supplied fields; Content-Type set to `application/x-www-form-urlencoded` |
| adr-0092:279 | Forwards to `claude.ai/oauth/token` | server/oauth_exchange.go:16 | IMPLEMENTED | — | `const anthropicTokenEndpoint = "https://claude.ai/oauth/token"` |
| adr-0092:279 | Returns `{ "access_token": "..." }` on success | server/oauth_exchange.go:109-131 | IMPLEMENTED | — | On HTTP 200 from upstream, extracts `access_token` from JSON body and returns `OAuthExchangeResponse{AccessToken: accessToken}` |
| adr-0092:279 | Returns `{ "error": "...", "error_description": "..." }` on failure | server/oauth_exchange.go:134-161 | IMPLEMENTED | — | Non-200 upstream responses parsed into `OAuthExchangeErrorResponse`; unknown errors synthesized with `"token_exchange_failed"` key |
| adr-0092:279 | Auth: requires valid mclaude user JWT | server/server.go:29 | IMPLEMENTED | — | `s.authMiddleware(http.HandlerFunc(s.handleOAuthExchange))` |
| adr-0092:279 | CP does not store the token — it passes through | server/oauth_exchange.go:54-162 | IMPLEMENTED | — | Handler has no DB or NATS calls; verified by `TestHandleOAuthExchange_DoesNotStoreToken` (nil DB/NATS — would panic on access) |


### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| server/oauth_exchange.go:18-25 | INFRA | `oauthExchangeHTTPClient` package-level var + `httpDoer` interface — test-seam pattern for injecting a fake HTTP client in unit tests; necessary plumbing for testability |
| server/oauth_exchange.go:28-44 | INFRA | `OAuthExchangeRequest`, `OAuthExchangeResponse`, `OAuthExchangeErrorResponse` struct definitions — required to satisfy spec-defined request/response shapes |
| server/oauth_exchange.go:86-88 | INFRA | `Accept: application/json` header on upstream request — not explicitly required by spec but standard HTTP practice for JSON APIs; no spec says to omit it |
| server/oauth_exchange.go:149-160 | INFRA | HTTP status code mapping (400→400, 401→401, 5xx→502) — error response forwarding logic for non-200 upstream; spec says `{error, error_description}` on failure but does not enumerate output status codes; mapping is reasonable and not contradicted by spec |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | Integration test | Verdict | Notes |
|-----------------|-----------|-----------|------------------|---------|-------|
| adr-0092:104 / spec-control-plane:104 | POST /api/auth/oauth/exchange exists, protected | TestHandleOAuthExchange_RequiresAuth, TestHandleOAuthExchange_InvalidToken | none | UNIT_ONLY | Endpoint is an API handler that proxies to external service; no integration test covers real auth middleware + real JWT + real handler wiring against a live server |
| adr-0092:279 | Request body shape `{code, code_verifier, redirect_uri, client_id}` | TestHandleOAuthExchange_MissingFields, TestHandleOAuthExchange_InvalidJSON | none | UNIT_ONLY | Missing fields covered; integration test absent |
| adr-0092:279 | CP adds `grant_type=authorization_code` and form-encodes POST | TestHandleOAuthExchange_UpstreamRequestFields | none | UNIT_ONLY | Form fields fully verified in unit test via capturing doer |
| adr-0092:279 | Returns `{access_token}` on success | TestHandleOAuthExchange_Success | none | UNIT_ONLY | Happy path covered by unit test |
| adr-0092:279 | Returns `{error, error_description}` on failure | TestHandleOAuthExchange_UpstreamError, TestHandleOAuthExchange_UpstreamServerError, TestHandleOAuthExchange_UpstreamNetworkError | none | UNIT_ONLY | 400/5xx/network-error cases covered |
| adr-0092:279 | CP does not store the token | TestHandleOAuthExchange_DoesNotStoreToken | none | UNIT_ONLY | Covered by nil-DB/NATS panic-if-touched pattern; no integration test |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| 002-droid-shield-false-positives.md | Droid-Shield False Positives on Non-Secret Patterns | OPEN | Component is "Droid-Shield pre-commit secret scanner" — not mclaude-control-plane. Out of scope for this audit. |

### Summary

- Implemented: 8
- Gap: 0
- Partial: 0
- Infra: 4
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 6
- E2E only: 0
- Untested: 0
- Bugs fixed: 0
- Bugs open: 0 (1 open bug found but it is out of scope — Component is Droid-Shield, not mclaude-control-plane)
