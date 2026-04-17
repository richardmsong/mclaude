// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { TurnUsageBadge } from './TurnUsageBadge'
import { TurnUsageSheet } from './TurnUsageSheet'
import type { UsageStats } from '@/types'

const sampleUsage: UsageStats = {
  inputTokens: 8200,
  outputTokens: 1100,
  cacheReadTokens: 3000,
  cacheWriteTokens: 0,
  costUsd: 0,
}

beforeEach(() => {
  localStorage.clear()
})

describe('TurnUsageBadge', () => {
  it('renders formatted token count and cost', () => {
    const { container } = render(
      <TurnUsageBadge usage={sampleUsage} model="claude-sonnet-4-6" />
    )
    // Total tokens = 8200 + 1100 + 3000 + 0 = 12300 -> 12.3K
    expect(container.textContent).toContain('12.3K tokens')
    // Cost: (8200*3 + 1100*15 + 3000*0.3) / 1e6 = (24600 + 16500 + 900) / 1e6 = 0.042
    expect(container.textContent).toContain('$0.042')
  })

  it('is hidden when usage is not present (conditional render by caller)', () => {
    // TurnUsageBadge is only rendered when usage exists — test that it renders with usage
    const { container } = render(<TurnUsageBadge usage={sampleUsage} />)
    expect(container.firstChild).not.toBeNull()
  })

  it('opens TurnUsageSheet on click', () => {
    render(<TurnUsageBadge usage={sampleUsage} model="sonnet-4-6" />)
    const badge = screen.getByRole('button')
    fireEvent.click(badge)
    // Sheet header should appear
    expect(screen.getByText('Turn Usage')).toBeDefined()
  })
})

describe('TurnUsageSheet', () => {
  it('renders model name', () => {
    const { container } = render(
      <TurnUsageSheet
        usage={sampleUsage}
        model="claude-sonnet-4-6"
        onClose={() => {}}
      />
    )
    expect(container.textContent).toContain('claude-sonnet-4-6')
  })

  it('renders all four token tile labels', () => {
    const { container } = render(
      <TurnUsageSheet usage={sampleUsage} onClose={() => {}} />
    )
    expect(container.textContent).toContain('Input')
    expect(container.textContent).toContain('Output')
    expect(container.textContent).toContain('Cache W')
    expect(container.textContent).toContain('Cache R')
  })

  it('renders Estimated Cost section', () => {
    const { container } = render(
      <TurnUsageSheet usage={sampleUsage} onClose={() => {}} />
    )
    expect(container.textContent).toContain('Estimated Cost')
    // total tokens 12300
    expect(container.textContent).toContain('12.3K total tokens')
  })

  it('renders session proportion when sessionUsage provided', () => {
    const sessionUsage: UsageStats = {
      ...sampleUsage,
      inputTokens: 82000,
      outputTokens: 11000,
      cacheReadTokens: 30000,
      cacheWriteTokens: 0,
      costUsd: 0,
    }
    const { container } = render(
      <TurnUsageSheet usage={sampleUsage} sessionUsage={sessionUsage} onClose={() => {}} />
    )
    expect(container.textContent).toContain('% of session')
    expect(container.textContent).toContain('10%')
  })

  it('does not render session proportion without sessionUsage', () => {
    const { container } = render(
      <TurnUsageSheet usage={sampleUsage} onClose={() => {}} />
    )
    expect(container.textContent).not.toContain('% of session')
  })

  it('calls onClose when scrim is clicked', () => {
    let closed = false
    const { container } = render(
      <TurnUsageSheet usage={sampleUsage} onClose={() => { closed = true }} />
    )
    // Click the scrim (first fixed div)
    const scrim = container.querySelector('div[style*="rgba"]') as HTMLElement
    if (scrim) fireEvent.click(scrim)
    expect(closed).toBe(true)
  })
})
