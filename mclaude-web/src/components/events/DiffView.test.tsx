// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { DiffView } from './DiffView'

describe('DiffView', () => {
  it('renders without crashing for empty diff', () => {
    expect(() => render(<DiffView diff="" />)).not.toThrow()
  })

  it('renders added lines with + gutter', () => {
    const diff = '+new line\n context line'
    const { container } = render(<DiffView diff={diff} />)
    const gutters = container.querySelectorAll('span')
    const hasPlus = Array.from(gutters).some(el => el.textContent === '+')
    expect(hasPlus).toBe(true)
  })

  it('renders removed lines with − gutter', () => {
    const diff = '-old line\n context'
    const { container } = render(<DiffView diff={diff} />)
    const gutters = container.querySelectorAll('span')
    const hasMinus = Array.from(gutters).some(el => el.textContent === '−')
    expect(hasMinus).toBe(true)
  })

  it('shows filename header when provided', () => {
    const { container } = render(<DiffView diff="+line" filename="src/main.go" />)
    expect(container.textContent).toContain('src/main.go')
  })

  it('renders char-level highlights for paired add/remove lines', () => {
    // A remove/add pair with one character difference
    const diff = '-const foo = 1\n+const foo = 2'
    const { container } = render(<DiffView diff={diff} />)
    // char highlights are spans with class diff-hl
    const hlSpans = container.querySelectorAll('.diff-hl')
    // Should have at least 1 highlighted span (the changed character)
    expect(hlSpans.length).toBeGreaterThan(0)
  })

  it('renders context lines without gutter marker', () => {
    const diff = ' context line'
    const { container } = render(<DiffView diff={diff} />)
    // Context lines get a space gutter
    const gutters = container.querySelectorAll('span')
    const hasSpace = Array.from(gutters).some(el => el.textContent === ' ')
    expect(hasSpace).toBe(true)
  })

  it('handles unified diff format with @@ header', () => {
    const diff = `@@ -1,3 +1,3 @@\n context\n-removed\n+added`
    const { container } = render(<DiffView diff={diff} />)
    expect(container.textContent).toContain('context')
    expect(container.textContent).toContain('removed')
    expect(container.textContent).toContain('added')
  })

  it('does not crash on very long lines', () => {
    const longLine = '+' + 'x'.repeat(600)
    expect(() => render(<DiffView diff={longLine} />)).not.toThrow()
  })
})
