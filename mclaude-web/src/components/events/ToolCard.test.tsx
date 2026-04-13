// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'

// Re-export tokenizeBash for testing by testing the rendered output
// We test via the ToolCard component with a Bash block

import type { ToolUseBlock } from '@/types'
import { ToolCard } from './ToolCard'

function makeBashBlock(command: string): ToolUseBlock {
  return {
    type: 'tool_use',
    id: 'tool-1',
    name: 'Bash',
    inputSummary: command,
    fullInput: { command },
  }
}

describe('ToolCard bash syntax highlighting', () => {
  it('renders bash command without crashing', () => {
    const block = makeBashBlock('git status')
    const { container } = render(<ToolCard block={block} />)
    expect(container.textContent).toContain('git')
    expect(container.textContent).toContain('status')
  })

  it('renders command with flags', () => {
    const block = makeBashBlock('ls -la --color=auto')
    const { container } = render(<ToolCard block={block} />)
    expect(container.textContent).toContain('ls')
    expect(container.textContent).toContain('-la')
    expect(container.textContent).toContain('--color=auto')
  })

  it('renders command with pipe operator', () => {
    const block = makeBashBlock('cat file.txt | grep error')
    const { container } = render(<ToolCard block={block} />)
    expect(container.textContent).toContain('cat')
    expect(container.textContent).toContain('|')
    expect(container.textContent).toContain('grep')
  })

  it('renders command with variables', () => {
    const block = makeBashBlock('echo $HOME')
    const { container } = render(<ToolCard block={block} />)
    expect(container.textContent).toContain('echo')
    expect(container.textContent).toContain('$HOME')
  })

  it('renders command with string literals', () => {
    const block = makeBashBlock('echo "hello world"')
    const { container } = render(<ToolCard block={block} />)
    expect(container.textContent).toContain('echo')
    expect(container.textContent).toContain('"hello world"')
  })

  it('renders multiline bash without crashing', () => {
    const block = makeBashBlock('npm install && npm run build')
    const { container } = render(<ToolCard block={block} />)
    expect(container.textContent).toContain('npm')
    expect(container.textContent).toContain('&&')
  })

  it('renders empty command without crashing', () => {
    const block = makeBashBlock('')
    expect(() => render(<ToolCard block={block} />)).not.toThrow()
  })

  it('shows running indicator when no result', () => {
    const block = makeBashBlock('sleep 10')
    const { container } = render(<ToolCard block={block} />)
    expect(container.textContent).toContain('running')
  })

  it('shows result content when result present', () => {
    const block: ToolUseBlock = {
      ...makeBashBlock('echo hello'),
      result: {
        type: 'tool_result',
        toolUseId: 'tool-1',
        content: 'hello\n',
        isError: false,
      },
    }
    const { container } = render(<ToolCard block={block} />)
    expect(container.textContent).toContain('hello')
  })

  it('shows error indicator on error result', () => {
    const block: ToolUseBlock = {
      ...makeBashBlock('cat /nonexistent'),
      result: {
        type: 'tool_result',
        toolUseId: 'tool-1',
        content: 'cat: /nonexistent: No such file',
        isError: true,
      },
    }
    const { container } = render(<ToolCard block={block} />)
    expect(container.textContent).toContain('error')
  })
})
