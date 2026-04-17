## Run: 2026-04-16T00:00:00Z

Scope: SPA login error handling — docs/ui-spec.md lines 102-106, four error message rules.
Verify fix from commit 5539828 (Firefox `NetworkError` detection).

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Notes |
|-----------------|-----------|---------------|---------|-------|
| ui-spec.md:103 | Server returned non-2xx with body text: show verbatim | auth-client.ts:16-17 + AuthScreen.tsx:25-26 | IMPLEMENTED | body non-empty thrown as Error message; catch in AuthScreen passes msg through setLocalError |
| ui-spec.md:104 | Server returned non-2xx with no body: show `Login failed: {status}` | auth-client.ts:17 | IMPLEMENTED | Empty body falls to template string exactly matching spec |
| ui-spec.md:105 | Network error (Load failed / Failed to fetch / Firefox): show long HTTPS advisory | AuthScreen.tsx:23-24 | IMPLEMENTED | All three variants covered: `=== 'Load failed'`, `=== 'Failed to fetch'`, `startsWith('NetworkError')` |
| ui-spec.md:106 | Login succeeded but NATS failed: show `Login succeeded but could not connect to messaging ({natsUrl}): {error}` | App.tsx:372-374 | IMPLEMENTED | Exact format matches spec; error propagates to AuthScreen catch |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| AuthScreen.tsx:3-6 | INFRA | Props interface |
| AuthScreen.tsx:8-31 | INFRA | State, submit handler, error classification — all implement spec'd behaviors |
| AuthScreen.tsx:33 | INFRA | displayError merges localError and prop error; prop path is used by parent for NATS error display |
| AuthScreen.tsx:35-113 | INFRA | JSX render + inputStyle — required UI scaffold |
| auth-client.ts:9-23 | INFRA | login() — HTTP call and error throws feeding all four spec rules |

### Summary

- Implemented: 4
- Gap: 0
- Partial: 0
- Infra: 5
- Unspec'd: 0
- Dead: 0
