// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import type { ToolUseBlock, Turn } from '@/types'
import { EventDetailModal } from './EventDetailModal'

function makeTurn(): Turn {
  return {
    id: 'turn-1',
    type: 'assistant',
    blocks: [],
  }
}

function makeBlock(overrides: Partial<ToolUseBlock>): ToolUseBlock {
  return {
    type: 'tool_use',
    id: 'tool-1',
    name: 'Bash',
    inputSummary: '',
    ...overrides,
  }
}

describe('EventDetailModal', () => {
  it('Bash tool: renders command and does NOT render raw JSON Input section', () => {
    const block = makeBlock({
      name: 'Bash',
      fullInput: { command: 'git status' },
    })
    const { container, queryByText } = render(
      <EventDetailModal block={block} turn={makeTurn()} onClose={() => {}} />
    )
    // Should show the command
    expect(container.textContent).toContain('git status')
    // Should NOT have a label "Input" from JsonSection
    expect(queryByText('Input')).toBeNull()
    // Should NOT render raw JSON for Bash (no JSON.stringify output of the full object)
    expect(container.textContent).not.toContain('"command"')
  })

  it('Edit tool: renders file path and does NOT render JsonSection raw JSON', () => {
    const block = makeBlock({
      name: 'Edit',
      fullInput: {
        file_path: 'src/main.ts',
        old_string: 'foo',
        new_string: 'bar',
      },
    })
    const { container, queryByText } = render(
      <EventDetailModal block={block} turn={makeTurn()} onClose={() => {}} />
    )
    // Should show the file path
    expect(container.textContent).toContain('src/main.ts')
    // Should NOT have a "Input" section label
    expect(queryByText('Input')).toBeNull()
    // Should NOT render the raw key names from JSON.stringify of the full input
    expect(container.textContent).not.toContain('"old_string"')
  })

  it('Write tool: renders file path and content, no Input section', () => {
    const block = makeBlock({
      name: 'Write',
      fullInput: {
        file_path: 'out.txt',
        content: 'hello world',
      },
    })
    const { container, queryByText } = render(
      <EventDetailModal block={block} turn={makeTurn()} onClose={() => {}} />
    )
    expect(container.textContent).toContain('out.txt')
    expect(container.textContent).toContain('hello world')
    expect(queryByText('Input')).toBeNull()
  })

  it('Read tool: renders file path, no Input section', () => {
    const block = makeBlock({
      name: 'Read',
      fullInput: { file_path: '/etc/hosts', offset: 10, limit: 50 },
    })
    const { container, queryByText } = render(
      <EventDetailModal block={block} turn={makeTurn()} onClose={() => {}} />
    )
    expect(container.textContent).toContain('/etc/hosts')
    expect(queryByText('Input')).toBeNull()
  })

  it('Grep tool: renders pattern and path, no Input section', () => {
    const block = makeBlock({
      name: 'Grep',
      fullInput: { pattern: 'TODO', path: 'src/' },
    })
    const { container, queryByText } = render(
      <EventDetailModal block={block} turn={makeTurn()} onClose={() => {}} />
    )
    expect(container.textContent).toContain('TODO')
    expect(container.textContent).toContain('src/')
    expect(queryByText('Input')).toBeNull()
  })

  it('Unknown tool: renders syntax-highlighted JSON (not raw JsonSection)', () => {
    const block = makeBlock({
      name: 'UnknownTool',
      fullInput: { someKey: 'someValue', count: 42, flag: true },
    })
    const { container, queryByText } = render(
      <EventDetailModal block={block} turn={makeTurn()} onClose={() => {}} />
    )
    // Should render the JSON content (keys and values)
    expect(container.textContent).toContain('someKey')
    expect(container.textContent).toContain('someValue')
    expect(container.textContent).toContain('42')
    expect(container.textContent).toContain('true')
    // Should NOT have the "Input" label from JsonSection
    expect(queryByText('Input')).toBeNull()
    // The fallback renders a <pre>, not a JsonSection with a label
    // Verify there's a <pre> element present for the JSON display
    expect(container.querySelector('pre')).not.toBeNull()
  })

  it('Unknown tool: JSON keys are wrapped in colored spans (syntax highlighting)', () => {
    const block = makeBlock({
      name: 'AgentTool',
      fullInput: { task: 'do something' },
    })
    const { container } = render(
      <EventDetailModal block={block} turn={makeTurn()} onClose={() => {}} />
    )
    // Keys should be rendered as spans with color: var(--blue)
    const blueSpans = Array.from(container.querySelectorAll('span')).filter(
      el => (el as HTMLElement).style?.color === 'var(--blue)'
    )
    expect(blueSpans.length).toBeGreaterThan(0)
  })
})
