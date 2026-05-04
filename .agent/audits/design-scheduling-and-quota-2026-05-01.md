## Run: 2026-05-01T00:00:00Z

**Round 1 gaps (8) — all resolved.** The ADR now specifies U5 conversion formula, co-requisite relationship with ADR-0054, handleDelete branching logic, allowedTools breaking change with migration, two-strategy token counting, KV-based recovery (not stream replay), lifecycle subject pattern divergence note, and sessions.input message format with JSON examples.

**Gaps found: 8 → all fixed.**

---

## Run: 2026-05-01T19:30:00Z (Round 2)

Previous audit runs (docs/audits/) found 7 additional gaps across two evaluations. All 7 are now resolved:
- `jobId` removed from all lifecycle event payloads ✅
- `prompt` field added to QuotaMonitor struct ✅
- `pending → running` trigger added to select-loop ✅
- Intermediate turn-end clarification added (Stop hook suppresses `result` events) ✅
- `turnEndedCh`/`doneCh` priority ordering specified (nested select with default fallthrough) ✅
- `State` → `Status` rename coordinated via ADR-0054 clean cut-over (no rolling upgrade, no dual-read shim) ✅
- `handleTurnEnd()` premature completion — clarified by Stop hook suppression ✅

**Gaps found: 1**

1. **`outputTokensAtSoftMark` referenced in authoritative token counting formula but not defined in the QuotaMonitor struct** — The ADR's `QuotaMonitor` struct definition (§ QuotaMonitor Goroutine) lists `outputTokensSinceSoftMark int` but does NOT list `outputTokensAtSoftMark`. However, the authoritative (result event) counting strategy explicitly references it: `"outputTokensSinceSoftMark = resultUsage.OutputTokens - outputTokensAtSoftMark"`. This formula requires knowing the cumulative output token count at the moment the soft marker was injected. The soft-stop path says "resets `outputTokensSinceSoftMark = 0`" but does not say "captures `outputTokensAtSoftMark = <current cumulative output tokens>`." A developer implementing the struct verbatim would have `outputTokensSinceSoftMark` (for the byte estimate, starting at 0) but no field to store the authoritative snapshot needed for the correction formula at turn-end. They would need to add a field and infer when to populate it from context. The existing code (`quota_monitor.go:36`) has `outputTokensAtSoftMark int` on the struct — the ADR's struct definition dropped it.
   - **Doc**: QuotaMonitor struct (§ QuotaMonitor Goroutine) — `outputTokensSinceSoftMark int` is listed; no `outputTokensAtSoftMark` field.
   - **Doc**: Token counting, Authoritative strategy — `"outputTokensSinceSoftMark = resultUsage.OutputTokens - outputTokensAtSoftMark"` — references undefined field.
   - **Code**: `quota_monitor.go:36` — `outputTokensAtSoftMark int` exists in current code but was omitted from the ADR's struct definition.
