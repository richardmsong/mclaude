## Run 1 (round 2): 2026-05-01T00:00:00Z

**Gaps found: 3**

1. **Dual config update path — `InputConfig` on `.input` subject vs dedicated `.config` subject** — ADR-0005 defines `InputConfig` as an `InputType` (`type: "config"`) that arrives on `sessions.{sslug}.input`, AND separately defines a dedicated `sessions.{sslug}.config` subject in the NATS Subject Structure. Both carry the same patch-semantics fields (`model`, `permissionMode`, `systemPrompt`). The ADR's own note says "Interrupt and restart are NOT InputTypes. They arrive on separate NATS subjects" — by the same logic, config should NOT be an InputType since it also has its own dedicated subject. Furthermore, the Session Agent Changes pseudocode (`runSession` function) handles `message`, `permission_response`, and `interrupt` in the input dispatcher but does NOT handle `config`, reinforcing the confusion about which path carries config updates. A developer implementing the session agent would stop and ask: "Which subject carries config updates — `.input` with `type: config`, or `.config`? Or both?"
   - **Doc**: Session Input Schema defines `InputConfig InputType = "config"` (line ~1021) with config fields in `SessionInput` (lines ~1010-1013). NATS Subject Structure lists `sessions.{sslug}.config` as a separate subject (line ~525). Session Agent Changes pseudocode (lines ~553-566) omits config handling.
   - **Code**: `spec-nats-payload-schema.md` defines `sessions.{sslug}.config` as an independent subject with its own payload schema (lines ~389-413), confirming the dedicated subject. The current code has no `.config` per-session subject (it uses the multiplexed `.api.sessions.input` for everything).

2. **`CLIBackend` enum values inconsistent with `spec-nats-payload-schema.md`** — ADR-0005 defines `BackendDevin CLIBackend = "devin"` and `BackendGeneric CLIBackend = "generic"`. `spec-nats-payload-schema.md` lists the backend values as `claude_code`, `droid`, `devin_acp`, `generic_terminal`. The spec declares ADR-0005 as an authoritative dependency (`Depends on: adr-0005-pluggable-cli.md (canonical event schema, driver interface)`), but contradicts it on two of the five enum values. A developer implementing the SPA session list (which reads `backend` from KV) would use `"devin_acp"` and `"generic_terminal"` per the payload spec, while a developer implementing the driver would use `"devin"` and `"generic"` per ADR-0005. These mismatched wire values would cause broken UI rendering and incorrect backend detection.
   - **Doc**: ADR-0005 `CLIBackend` enum (line ~459): `BackendDevin = "devin"`, `BackendGeneric = "generic"`.
   - **Code**: `spec-nats-payload-schema.md` line 763: `backend  CLI backend (claude_code, droid, devin_acp, generic_terminal)`. Two values differ: `devin` vs `devin_acp`, `generic` vs `generic_terminal`.

3. **`spec-nats-payload-schema.md` `sessions.create` still uses nested `quotaMonitor` — contradicts ADR-0005 and ADR-0044** — ADR-0005 explicitly states: "all quota fields are **top-level** in the JSON payload — not nested under a `quotaMonitor` object. This matches ADR-0044 which explicitly supersedes the nested `quotaMonitor: {...}` shape in `spec-nats-payload-schema.md`." ADR-0044 also explicitly states: "all quota fields are **top-level**, not nested under a `quotaMonitor` object — this ADR supersedes the nested shape in `spec-nats-payload-schema.md`." Yet `spec-nats-payload-schema.md` still shows the nested shape with different field names (`softThreshold`/`hardHeadroomTokens` in the ADRs vs `Threshold`/`Priority` in the current code's `QuotaMonitorConfig`). A developer implementing the `sessions.create` publisher or consumer who reads `spec-nats-payload-schema.md` — the document explicitly designed to be the single source of truth for NATS payloads — would produce the wrong JSON shape.
   - **Doc**: ADR-0005 "Driver registration" section (lines ~610-613): "all quota fields are **top-level** in the JSON payload". ADR-0044 "Creating a Scheduled Session" section: flat fields listed explicitly.
   - **Code**: `spec-nats-payload-schema.md` `sessions.create` scheduled payload (lines ~34-43) shows `"quotaMonitor": { "softThreshold": 75, "hardHeadroomTokens": 50000, "jobId": "job-001", "autoContinue": true }` — nested, not flat.

## Run 2 (round 3): 2026-05-01T07:25:22Z

**Round 2 fix verification:**

1. ✅ **Dual config path — FIXED.** `InputConfig` type removed from `InputType` enum. Only `message`, `skill_invoke`, `permission_response` remain. Config updates arrive exclusively on the dedicated `sessions.{sslug}.config` subject. The note at line ~1025 explicitly states "Interrupt, restart, and config are NOT InputTypes." The `runSession` pseudocode now has a separate goroutine for config handling. No ambiguity remains.

2. ✅ **CLIBackend enum mismatch — FIXED.** ADR-0005 now uses `BackendDevinACP CLIBackend = "devin_acp"` (line 657) and `BackendGenericTerminal CLIBackend = "generic_terminal"` (line 659), matching `spec-nats-payload-schema.md` exactly.

3. ✅ **Nested `quotaMonitor` in spec — FIXED.** `spec-nats-payload-schema.md` now shows flat top-level quota fields on `sessions.create` (lines ~65-82), matching ADR-0005 and ADR-0044. The `quotaMonitor` nesting is gone.

**New gap check:** Full re-read of ADR-0005 (1126 lines), cross-referenced against `spec-state-schema.md`, `spec-nats-payload-schema.md`, `spec-nats-data-taxonomy.md`, `adr-0044-scheduling-and-quota.md`, `adr-0054-nats-jetstream-permission-tightening.md`, and codebase files (`state.go`, `events.go`, `mclaude-cli/events/types.go`).

CLEAN — no blocking gaps found.
