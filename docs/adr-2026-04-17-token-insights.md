# Per-Turn Token Insights

**Status**: accepted
**Status history**:
- 2026-04-17: accepted


Feature **M5** — per-turn token breakdown visible on every assistant message.

## Problem

The SPA shows session-level token aggregates (feature M3) on a dedicated screen, but there's no way to understand which turns consumed the most tokens. Users need per-turn visibility to:
- Identify expensive turns (cache misses, large tool results)
- Understand cost per interaction without leaving the conversation
- Diagnose context window growth patterns

## Data Foundation

The data already exists:

- `Turn.usage?: UsageStats` is populated from `assistant` stream-json events (see `event-store.ts`)
- `UsageStats` has: `inputTokens`, `outputTokens`, `cacheReadTokens`, `cacheWriteTokens`, `costUsd`
- **Bug**: `costUsd` is hardcoded to `0` — must be computed from token counts

## Design

### 1. Fix cost computation in EventStore

When accumulating `turn.usage` from an `assistant` event, compute `costUsd` using the same pricing constants as `TokenUsage.tsx`:

```
PRICE_PER_M = { input: 3.0, output: 15.0, cacheRead: 0.3, cacheWrite: 3.75 }

costUsd = (inputTokens * input + outputTokens * output
         + cacheReadTokens * cacheRead + cacheWriteTokens * cacheWrite) / 1_000_000
```

Apply the calibration multiplier from `localStorage` (`mclaude.costCalibration`) if present.

Extract `PRICE_PER_M` into a shared module (`src/lib/pricing.ts`) so both `TokenUsage.tsx` and `event-store.ts` use the same constants. Also export `computeCost(usage: UsageStats, calibration?: number): number`.

### 2. Inline usage badge on assistant turns

Every assistant turn with `usage` data shows a small, tappable badge below the last block:

```
                                        ┌───────────────────────┐
  Let me fix that bug in the auth...    │ 12.3K tokens · $0.041 │
                                        └───────────────────────┘
                                                         ▼
```

Badge spec:
- Position: right-aligned, below the last block in the assistant turn, 4px top margin
- Background: `--surf2`, border-radius 8px, padding 4px 8px
- Text: `--text3`, 11px, monospace
- Format: `{totalTokens} tokens · ${cost}` where totalTokens = input + output + cacheRead + cacheWrite
- Tap: opens the **Turn Usage Sheet** (see below)
- Hidden when `usage` is absent (older sessions, user turns)

### 3. Turn Usage Sheet (bottom sheet)

Tapping the usage badge opens a bottom sheet with full per-turn breakdown. Reuses the existing bottom-sheet pattern from `EventDetailModal`.

```
┌─────────────────────────────────┐
│  📊 Turn Usage              ✕   │  header
├─────────────────────────────────┤
│  sonnet-4-6                     │  model name (from turn.model)
│                                 │
│  ┌────────┐  ┌────────┐        │
│  │ Input  │  │ Output │        │  2×2 grid (same style as TokenUsage)
│  │  8.2K  │  │  1.1K  │        │
│  │ $0.025 │  │ $0.017 │        │
│  └────────┘  └────────┘        │
│  ┌────────┐  ┌────────┐        │
│  │Cache W │  │Cache R │        │
│  │  0     │  │  3.0K  │        │
│  │ $0.000 │  │ $0.001 │        │
│  └────────┘  └────────┘        │
│                                 │
│  ┌─────────────────────────┐    │  stacked bar (proportional)
│  │████████░░░░░░░░░░░░░██░│    │
│  └─────────────────────────┘    │
│  ■ Input ■ Output ■ Cache R ■ W│  legend
│                                 │
│  Estimated Cost                 │
│  $0.043                         │  large, bold
│  12.3K total tokens             │
│                                 │
│  % of session                   │  context vs session
│  [███░░░░░░░░░] 18%             │  thin bar, --text3
│  $0.043 of $0.238 total         │
└─────────────────────────────────┘
```

Components:
- **Model badge**: turn's model name, `--text2`, 13px
- **2x2 token tiles**: identical layout/style to `TokenUsage.tsx` tiles (same colors: input=blue, output=green, cache-write=orange, cache-read=purple)
- **Stacked bar**: same as `TokenUsage.tsx` bar, but for this turn only
- **Estimated cost card**: total cost for this turn, bold
- **Session proportion**: thin progress bar showing what fraction of total session cost this turn represents. Only shown when session-level usage is available.

### 4. Integration with EventDetailModal

The existing `EventDetailModal` already shows `turn.usage` as a one-line footer:
```
{turn.model} · {tokens} tokens
```

Enhance this to be tappable — tapping it opens the Turn Usage Sheet. Also change the display to include cost:
```
{turn.model} · {totalTokens} tokens · ${costUsd}
```

### 5. Component structure

```
src/lib/pricing.ts                    — PRICE_PER_M, computeCost(), formatTokens(), formatCost()
src/components/events/TurnUsageBadge.tsx  — inline badge below assistant turns
src/components/events/TurnUsageSheet.tsx  — bottom sheet with full breakdown
```

`TurnUsageBadge` is rendered by the conversation event list for each assistant turn that has `usage`. `TurnUsageSheet` is rendered conditionally when the badge (or the EventDetailModal footer) is tapped.

### 6. Feature list update

Add to `docs/feature-list.md` under Model & Cost:

| M5 | Per-turn token insights | Inline token/cost badge on each assistant message, tap for full breakdown |

Platform support: Web SPA only initially. CLI shows nothing (text-only). iOS future.

## Out of Scope

- Historical per-turn data for sessions started before this feature (usage data must be present in the stream events)
- Per-tool-call token attribution (Claude API doesn't break down tokens per tool call within a turn)
- Budget alerts or limits per turn
- Token usage timeline chart across turns (possible future enhancement)
