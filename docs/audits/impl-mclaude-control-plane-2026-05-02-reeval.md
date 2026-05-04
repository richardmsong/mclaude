## Run: 2026-05-02T17:30:00Z (re-evaluation)

Component: mclaude-control-plane
Spec files: docs/mclaude-control-plane/spec-control-plane.md, docs/spec-state-schema.md, docs/charts-mclaude/spec-helm.md
ADRs evaluated: adr-0073 (accepted — gen-host-nkey fix), plus cross-cutting ADRs
Context: Re-evaluation after previous pass found 3 PARTIALs (all SPEC→FIX). Focusing on confirming those 3 items are now IMPLEMENTED.

Previous audit summary (impl-mclaude-control-plane-2026-05-02.md):
- Implemented: 82, Gap: 0, Partial: 3, Infra: 27, Unspec'd: 1, Dead: 0
- Tested: 1, Unit only: 8, E2E only: 1, Untested: 3
- Bugs fixed: 0, Bugs open: 0

The 3 PARTIALs were all SPEC→FIX:
1. spec-control-plane.md:213 — Startup step 3: OPERATOR_KEYS_PATH failure said "fatal exit" but code is non-fatal warning. Spec needed to match code.
2. spec-control-plane.md:300 — Error handling: OPERATOR_KEYS_PATH missing said "fatal exit" but code is non-fatal warning. Spec needed to match code.
3. spec-control-plane.md:248 — Auth middleware: said "Known gap: not implemented" but code implemented it. Spec needed to acknowledge implementation.

### Phase 1 — Spec → Code (targeted re-evaluation of previously PARTIAL items)

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| spec-control-plane.md:213 | "Also loads the operator seed and system account seed from OPERATOR_KEYS_PATH ... Caches the current account JWT in memory for revocation modifications." (no mention of fatal exit for OPERATOR_KEYS_PATH failure in startup step 3) | main.go:116-125 | IMPLEMENTED | — | Spec no longer says "fatal exit" for OPERATOR_KEYS_PATH in startup step 3. Code logs warning and continues. Spec and code now match. |
| spec-control-plane.md:300 | "OPERATOR_KEYS_PATH mount missing or unreadable: warning logged at startup; JWT revocation is disabled (TTL-only fallback). Control-plane can still mint and validate JWTs without this path — only the $SYS.REQ.CLAIMS.UPDATE revocation path is unavailable. This is the expected behavior in dev environments that lack the operator-keys Secret mount." | main.go:119-125 | IMPLEMENTED | — | Spec now correctly says "warning logged at startup" not "fatal exit". Code at main.go:120-125: logs Warn().Err(loadErr).Msg("operator-keys not loaded — JWT revocation disabled (TTL-only fallback)") then continues. Perfect match. |
| spec-control-plane.md:248 | "The auth middleware on protected routes decodes and validates the JWT against the account public key, extracts the user UUID into the request context, and enforces access boundaries: returns 403 Forbidden when the JWT subject (user slug) does not match the URL {uslug} parameter. Admin users bypass this check. Admin routes use a separate static ADMIN_TOKEN middleware on the loopback port (not per-user is_admin checks)." | auth.go:366-411 | IMPLEMENTED | — | Spec no longer says "Known gap". Code at auth.go:391-408 checks /api/users/{uslug}/... paths, extracts urlUSlug, fetches user from DB, returns 403 if user.Slug != urlUSlug AND !user.IsAdmin. Admin bypass implemented. Spec and code now match. |

### Phase 1 — gen-host-nkey subcommand (ADR-0073, confirming full implementation)

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| ADR-0073:44-50 | main.go: dispatch gen-host-nkey immediately after init-keys check | main.go:37-40 | IMPLEMENTED | — | `if len(os.Args) > 1 && os.Args[1] == "gen-host-nkey" { runGenHostNkey(); return }` present. |
| ADR-0073:57 | Read env NAMESPACE (default mclaude-system), HOST_CREDS_SECRET (required, fatal if empty) | gen_host_nkey.go:39-43 | IMPLEMENTED | — | envOr("NAMESPACE", "mclaude-system") and os.Getenv("HOST_CREDS_SECRET") with Fatal if empty. |
| ADR-0073:58 | Build in-cluster K8s client via rest.InClusterConfig()+kubernetes.NewForConfig() | gen_host_nkey.go:49-56 | IMPLEMENTED | — | Both calls present; fatal on error. |
| ADR-0073:59 | GET Secret; if exists → log "already exists — skipping" and exit 0 | gen_host_nkey.go:67-74 | IMPLEMENTED | — | Returns nil (leading to exit 0) with "already exists — skipping" log. |
| ADR-0073:60 | nkeys.CreateUser() | gen_host_nkey.go:80-83 | IMPLEMENTED | — | Correct U-prefix key type. |
| ADR-0073:61-62 | Extract seed and public key | gen_host_nkey.go:85-93 | IMPLEMENTED | — | kp.Seed() and kp.PublicKey() both called. |
| ADR-0073:63 | Create Secret with Data: {"nkey_seed": seed} | gen_host_nkey.go:96-109 | IMPLEMENTED | — | Secret created with correct field name. |
| ADR-0073:64 | Log public key with nkeyPublic field and "generated host NKey" message | gen_host_nkey.go:119-123 | IMPLEMENTED | — | Exact log message matches ADR. |
| ADR-0073:67-72 | Error handling: InClusterConfig failure, HOST_CREDS_SECRET empty, GET non-404, CreateUser/Seed failure, AlreadyExists race, other CREATE error | gen_host_nkey.go:49-52,41-43,75-77,80-88,111-117 | IMPLEMENTED | — | All error paths covered per ADR spec. |

### Summary of Changes Since Previous Audit

All 3 previously PARTIAL items are now IMPLEMENTED:

1. **OPERATOR_KEYS_PATH startup error handling (spec-control-plane.md:213)**: Spec was updated to remove the erroneous "fatal exit" language. Now describes the correct non-fatal warning behavior. Code unchanged (was already correct). PARTIAL → IMPLEMENTED.

2. **OPERATOR_KEYS_PATH error handling section (spec-control-plane.md:300)**: Spec was updated to say "warning logged at startup; JWT revocation is disabled (TTL-only fallback)" instead of "fatal exit on startup". Code unchanged (was already correct). PARTIAL → IMPLEMENTED.

3. **Auth middleware cross-user 403 enforcement (spec-control-plane.md:248)**: Spec was updated to remove "Known gap" language and now correctly describes the implemented behavior: 403 Forbidden when JWT subject doesn't match URL {uslug}, with admin bypass. Code unchanged (was already implemented). PARTIAL → IMPLEMENTED.

### Summary

- Phase 1 targeted re-evaluation: 3 previously PARTIAL items → all now IMPLEMENTED
- gen-host-nkey subcommand (ADR-0073): all 8 spec lines confirmed IMPLEMENTED
- CODE→FIX gaps remaining: 0
- SPEC→FIX gaps remaining: 0
- Overall component verdict: All spec lines IMPLEMENTED (82 + 3 reclassified = 85 IMPLEMENTED, 0 GAP, 0 PARTIAL)

No CODE→FIX gaps were found. No dev-harness pass is needed.
