# ADR: Fix missing scope parameter in OAuth PKCE authorize URL

**Status**: accepted
**Status history**:
- 2026-05-06: accepted

## Overview

The SPA's OAuth PKCE flow to `claude.ai/oauth/authorize` omits the required `scope` parameter. Claude.ai rejects the request with "Invalid OAuth Request — Missing scope parameter". Fix: add `scope=user:inference` to the authorize URL query parameters.

## Motivation

Bug — the OAuth popup fails immediately on every attempt. Users cannot connect their Claude account via OAuth, only via manual token paste.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Scope value | `user:inference` | Standard Anthropic OAuth scope for API access |
| Location | `ConnectSheet.tsx` line 149, add `authUrl.searchParams.set('scope', 'user:inference')` | Single line fix |

## Component Changes

### `mclaude-web`

- `src/components/ConnectSheet.tsx`: add `authUrl.searchParams.set('scope', 'user:inference')` after the `state` parameter (line 149).

## Impact

- No spec changes needed — ADR-0092 spec describes the OAuth flow; adding the scope parameter is an implementation fix.
