# ADR: Fix OAuth PKCE parameters to match Anthropic's OAuth provider

**Status**: draft
**Status history**:
- 2026-05-06: draft

## Overview

The SPA's OAuth PKCE flow uses incorrect parameters — wrong `client_id`, wrong authorize URL, wrong token URL, and incomplete scopes. These were hardcoded during ADR-0092 implementation without access to Anthropic's actual OAuth configuration. This ADR corrects all parameters to match the production OAuth flow used by Claude Code (the official CLI).

## Motivation

The OAuth popup fails because:
1. `client_id` is `mclaude-cli` (invented) — the real one is `9d1c250a-e61b-44d9-88ed-5944d1962f5e`
2. Authorize URL is `claude.ai/oauth/authorize` — the real one is `claude.com/cai/oauth/authorize` (bounces through claude.com for attribution, then 307s to claude.ai)
3. Token URL is `claude.ai/oauth/token` — the real one is `platform.claude.com/v1/oauth/token`
4. Scopes are `user:inference` only — the real set is `user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload`
5. The redirect URI must match what's registered with the OAuth provider — our SPA uses `{SPA_ORIGIN}/auth/oauth/callback` which may not be registered

Source: [Claude Code OAuth constants](https://github.com/yasasbanukaofficial/claude-code/blob/main/src/constants/oauth.ts)

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| `client_id` | `9d1c250a-e61b-44d9-88ed-5944d1962f5e` | Anthropic's registered OAuth client ID for Claude Code |
| Authorize URL | `https://claude.com/cai/oauth/authorize` | Production URL with attribution bounce; 307s to claude.ai/oauth/authorize |
| Token URL | `https://platform.claude.com/v1/oauth/token` | Anthropic's production token endpoint |
| Scopes | `user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload` | Full Claude Code scope set — `user:inference` alone is insufficient |
| Redirect URI | `https://platform.claude.com/oauth/code/callback` | Anthropic's registered callback URL. The SPA cannot use its own origin as redirect — it won't be registered. Instead, use the manual redirect flow: Anthropic redirects to their success page which displays the auth code, and the user or the SPA's postMessage flow captures it. |
| Token exchange | Via CP proxy `POST /api/auth/oauth/exchange` | The SPA cannot call `platform.claude.com/v1/oauth/token` directly (CORS). CP proxies the exchange. The token URL in CP must also be updated to `platform.claude.com/v1/oauth/token`. |
| OAuth beta header | `anthropic-beta: oauth-2025-04-20` | Required header on token exchange requests per Claude Code source |

## Component Changes

### `mclaude-web`

**`src/components/ConnectSheet.tsx`:**
- `OAUTH_CLIENT_ID`: `'mclaude-cli'` → `'9d1c250a-e61b-44d9-88ed-5944d1962f5e'`
- `CLAUDE_OAUTH_URL`: `'https://claude.ai/oauth/authorize'` → `'https://claude.com/cai/oauth/authorize'`
- Scopes: `'user:inference'` → `'user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload'`
- Redirect URI: investigate whether `{SPA_ORIGIN}/auth/oauth/callback` works with this client_id, or if we need to use Anthropic's registered callback URL (`platform.claude.com/oauth/code/callback`) with a different flow

### `mclaude-control-plane`

**`server/oauth_exchange.go` (or wherever `POST /api/auth/oauth/exchange` is):**
- Token URL: update to `https://platform.claude.com/v1/oauth/token`
- Add `anthropic-beta: oauth-2025-04-20` header to the token exchange request

## Open Questions

1. **Redirect URI registration**: Does Anthropic's OAuth provider allow arbitrary redirect URIs for this client_id, or only pre-registered ones? If only pre-registered, we may need to use `platform.claude.com/oauth/code/callback` and a different flow to capture the auth code (e.g., polling or manual paste). Claude Code uses this URL as `MANUAL_REDIRECT_URL`.

2. **Client ID reuse**: Is it acceptable to reuse Claude Code's `client_id` for mclaude, or should we register a separate OAuth application with Anthropic? Reusing avoids registration overhead but means mclaude and Claude Code share token scopes and rate limits.

3. **Scope minimization**: Do we need all 5 scopes? `user:sessions:claude_code`, `user:mcp_servers`, and `user:file_upload` may not be needed for mclaude's use case (we only need API inference). But requesting fewer scopes than Claude Code may cause issues if the provider expects the full set.

## Impact

- `docs/mclaude-web/spec-web.md`: update OAuth popup flow description with correct URLs, client_id, scopes
- `docs/mclaude-control-plane/spec-control-plane.md`: update `POST /api/auth/oauth/exchange` token URL
