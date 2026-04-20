# Spec: UI Token Usage

Token usage data contracts and visualizations. Shared across all UI components — calibration, budget bar semantics, and the session/global breakdown formats are cross-platform.

## Overlay: Token Usage (Session)

Full-screen overlay with back button. Shows breakdown for a single session:

```
┌─────────────────────────────────┐
│ ‹ Back   Token Usage            │
├─────────────────────────────────┤
│  sonnet-4-6 · 5 turns           │  model + turn count
│                                 │
│  ┌────────┐  ┌────────┐         │
│  │ Input  │  │ Output │         │  2-column grid
│  │ 12.3K  │  │ 4.1K   │         │
│  │ $0.012 │  │ $0.041 │         │
│  └────────┘  └────────┘         │
│  ┌────────┐  ┌────────┐         │
│  │Cache W │  │Cache R │         │
│  │ 2.1K   │  │ 45.2K  │         │
│  │ $0.003 │  │ $0.005 │         │
│  └────────┘  └────────┘         │
│                                 │
│  ┌─────────────────────────┐    │
│  │  Estimated Cost         │    │
│  │  $0.061                 │    │
│  │  63.7K total tokens     │    │
│  └─────────────────────────┘    │
│  Prices: input $3/M · output …  │
└─────────────────────────────────┘
```

Token tiles: 2×2 grid, each showing label, token count (formatted as K/M), cost estimate. Colors match the design palette (input=blue, output=green, cache-write=orange, cache-read=purple).

---

## Screen: Token Usage (Global)

```
┌─────────────────────────────────┐
│ ‹ Back   Token Usage            │
├─────────────────────────────────┤
│ 1H  6H  24H  7D  30D            │  time range chips
├─────────────────────────────────┤
│ $4.23 / $140 this month  Calib  │  monthly budget bar
│ [████░░░░░░░░░░░░░░░░░░░] $140  │
│ 30% used · 12/30 days           │
│ Projected month-end: $9.20      │
├─────────────────────────────────┤
│ ┌──────┐ ┌──────┐ ┌──────┐     │
│ │Tokens│ │ Cost │ │Tok/m │     │  stat tiles
│ │ 1.2M │ │$4.23 │ │ 845  │     │
│ └──────┘ └──────┘ └──────┘     │
├─────────────────────────────────┤
│ [stacked bar chart SVG]         │  tokens over time
│  ■ Input ■ Output ■ Cache R ■ W │
├─────────────────────────────────┤
│ ● Input      1.2M    $3.60      │  token breakdown list
│ ● Output     89K     $1.34      │
│ ● Cache Read 320K    $0.10      │
│ ● Cache Write 12K    $0.015     │
├─────────────────────────────────┤
│ sonnet-4-6 ×12 · 89 API calls   │  footer
└─────────────────────────────────┘
```

Budget bar: two-layer progress bar — solid for actual spend (color: green/orange/red based on %, threshold 60%/85%), semi-transparent for projected overage.

Chart: stacked bar chart, time-bucketed. Buckets: 5min (1H), 30min (6H), 2h (24H), 6h (7D), 1d (30D). X-axis labels at 4 evenly-spaced positions.

Calibration: link to adjust cost estimates against Anthropic Console actuals. When calibrated, shows badge with multiplier.
