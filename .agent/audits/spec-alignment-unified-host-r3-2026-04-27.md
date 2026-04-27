## Run: 2026-04-27T00:00:00Z

### Round 3 Spec Alignment Audit — ADR-0035 Unified Host Architecture

Focus: verify Round 2 G1 auth.json fix landed correctly; check for any new gaps.
ADR: `docs/adr-0035-unified-host-architecture.md`
Specs checked: `docs/spec-state-schema.md`, `docs/mclaude-control-plane/spec-control-plane.md`,
`docs/mclaude-session-agent/spec-session-agent.md`, `docs/charts-mclaude/spec-helm.md`,
`docs/mclaude-cli/spec-cli.md`, `docs/ui/mclaude-web/spec-host-picker.md`,
`docs/mclaude-controller/spec-controller.md`.

---

### Phase 1 — Round 2 G1 Fix Verification

| Fix | What was fixed | Where to look | Verdict | Notes |
|-----|---------------|---------------|---------|-------|
| G1a | spec-state-schema.md: `auth.json` moved out of host credentials directory into a separate "User-level credentials" subsection at `~/.mclaude/auth.json` | spec-state-schema.md lines 609–616 | VERIFIED | New subsection "### User-level credentials" at line 609. Path: `~/.mclaude/auth.json` (mode `0600`). Explicitly states "The token is user-scoped, not host-scoped — it lives outside the per-host directory because the same admin token is valid across all of the user's hosts." Writers: `mclaude login`. Readers: CLI (admin and cluster subcommands). Host credentials directory section (lines 594–607) no longer contains `auth.json`. |
| G1b | spec-cli.md: `mclaude login` command added as its own documented entry | spec-cli.md lines 38–44 | VERIFIED | Full `#### mclaude login` section present. Documents email+password or OAuth flow, writes to `~/.mclaude/auth.json` at mode `0600`, token is user-scoped, re-running overwrites. `--server` and `--email` flags documented with defaults. Writer of `auth.json` is now consistently documented in both spec-state-schema.md (line 615: "Writers: `mclaude login`.") and spec-cli.md. |

---

### Phase 2 — New Gap Check (Spot Pass on Changed Sections)

Scanning for any second-order inconsistencies introduced by the auth.json relocation.

| Check | Spec text | Location | Verdict | Notes |
|-------|-----------|----------|---------|-------|
| ADR-0035:50 "Token persisted to `~/.mclaude/auth.json` at 0600" | spec-state-schema.md:611: `Path: ~/.mclaude/auth.json (mode 0600)` | Both consistent | IMPLEMENTED | Exact path and mode match. |
| ADR-0035:50 "mclaude login ... bearer token" | spec-cli.md:38–44 `mclaude login` command | Consistent | IMPLEMENTED | Command documented with correct behavior. |
| spec-state-schema.md host credentials directory (lines 594–607): must NOT contain auth.json | Lines 597–600: `nkey.seed`, `nats.creds`, `config.json` only | Clean | VERIFIED | No `auth.json` entry in the host credentials directory section. |
| spec-cli.md Dependencies section: references `~/.mclaude/context.json` and `mclaude-common` | Lines 127–130 | No auth.json reference needed | N/A | Dependencies section lists unix socket and context.json. No explicit auth.json dependency entry, but auth.json is documented inline at the mclaude login command (lines 38–44). No gap — the login command's own description is the canonical reference. |
| Cross-check: spec-control-plane.md admin bearer token description vs. new auth.json section | spec-control-plane.md:10–11, 68 describes admin Bearer auth; spec-state-schema.md:613 says "CLI sends `Authorization: Bearer <token>`" | Consistent | IMPLEMENTED | Control-plane spec does not need to reference the client-side file path. |

---

### Phase 3 — No-Regression Spot Check

Verifying that the host credentials directory section is intact and well-formed after the auth.json extraction.

| Item | Expected | Actual (spec-state-schema.md) | Verdict |
|------|----------|-------------------------------|---------|
| Host credentials directory header | `### Host credentials directory (BYOH machines)` | Line 593 present | PASS |
| Path | `~/.mclaude/hosts/{hslug}/` | Line 595 | PASS |
| Contents: nkey.seed | Present | Line 598 | PASS |
| Contents: nats.creds | Present | Line 599 | PASS |
| Contents: config.json | Present | Line 600 | PASS |
| active-host symlink | Present | Line 605 | PASS |
| User-level credentials section following the host dir section | New `### User-level credentials` subsection | Lines 609–616 | PASS |
| auth.json NOT in host dir contents | Absent | Confirmed absent | PASS |

---

### Phase 4 — Bug Triage

No new bugs opened against ADR-0035 since Round 2. BUG-004 remains in `.agent/bugs/fixed/` (moved in Round 2). No open bugs to triage.

---

### Summary

**Round 2 G1 Fix**: VERIFIED — both sub-fixes present and correctly implemented:
- (a) spec-state-schema.md: `auth.json` in a new "User-level credentials" subsection at `~/.mclaude/auth.json`, separated from the host credentials directory.
- (b) spec-cli.md: `mclaude login` documented as its own command with correct path, mode, token-scope note, and flags.

**New gaps found**: 0

**All other ADR-0035 decision lines**: carry forward as IMPLEMENTED from Round 2 (46 lines verified, all removed concepts confirmed absent).

**Bugs**: 0 OPEN, 1 FIXED (carry-forward from Round 2).
