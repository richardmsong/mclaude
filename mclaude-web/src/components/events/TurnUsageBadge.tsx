import { useState } from 'react'
import type { UsageStats } from '@/types'
import { formatTokens, formatCost, computeCost, loadCalibration } from '@/lib/pricing'
import { TurnUsageSheet } from './TurnUsageSheet'

interface TurnUsageBadgeProps {
  usage: UsageStats
  model?: string
  sessionUsage?: UsageStats
}

export function TurnUsageBadge({ usage, model, sessionUsage }: TurnUsageBadgeProps) {
  const [sheetOpen, setSheetOpen] = useState(false)

  const calibration = loadCalibration()
  const totalTokens =
    usage.inputTokens + usage.outputTokens + usage.cacheReadTokens + usage.cacheWriteTokens
  const cost = computeCost(usage, calibration)

  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 4 }}>
        <button
          onClick={() => setSheetOpen(true)}
          style={{
            background: 'var(--surf2)',
            borderRadius: 8,
            padding: '4px 8px',
            fontFamily: "'Menlo','Courier New',monospace",
            fontSize: 11,
            color: 'var(--text3)',
            cursor: 'pointer',
          }}
        >
          {formatTokens(totalTokens)} tokens &middot; {formatCost(cost)}
        </button>
      </div>

      {sheetOpen && (
        <TurnUsageSheet
          usage={usage}
          model={model}
          sessionUsage={sessionUsage}
          onClose={() => setSheetOpen(false)}
        />
      )}
    </>
  )
}
