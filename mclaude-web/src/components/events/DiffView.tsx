interface DiffViewProps {
  diff: string
  filename?: string
}

interface DiffLine {
  type: 'add' | 'remove' | 'context' | 'header'
  content: string
}

function parseDiff(raw: string): DiffLine[] {
  return raw.split('\n').map(line => {
    if (line.startsWith('+')) return { type: 'add' as const, content: line.slice(1) }
    if (line.startsWith('-')) return { type: 'remove' as const, content: line.slice(1) }
    if (line.startsWith('@@') || line.startsWith('diff ') || line.startsWith('index ') || line.startsWith('---') || line.startsWith('+++')) {
      return { type: 'header' as const, content: line }
    }
    return { type: 'context' as const, content: line.startsWith(' ') ? line.slice(1) : line }
  })
}

const LINE_STYLES: Record<DiffLine['type'], React.CSSProperties> = {
  add: { background: 'rgba(48,209,88,0.12)', color: 'var(--text)' },
  remove: { background: 'rgba(255,69,58,0.12)', color: 'var(--text)' },
  context: { background: 'transparent', color: 'var(--text3)' },
  header: { background: 'var(--surf3)', color: 'var(--text2)', fontStyle: 'italic' },
}

const GUTTER: Record<DiffLine['type'], string> = {
  add: '+',
  remove: '−',
  context: ' ',
  header: ' ',
}

// Compute char-level diff between two strings using LCS
// Returns segments: { text, highlighted }[]
type CharSegment = { text: string; highlighted: boolean }

function charDiff(a: string, b: string): { removed: CharSegment[]; added: CharSegment[] } {
  // LCS-based character diff (Myers algorithm simplified)
  const m = a.length
  const n = b.length
  const dp: number[][] = Array.from({ length: m + 1 }, () => new Array(n + 1).fill(0))
  for (let i = m - 1; i >= 0; i--) {
    for (let j = n - 1; j >= 0; j--) {
      if (a[i] === b[j]) {
        dp[i]![j] = (dp[i + 1]?.[j + 1] ?? 0) + 1
      } else {
        dp[i]![j] = Math.max(dp[i + 1]?.[j] ?? 0, dp[i]?.[j + 1] ?? 0)
      }
    }
  }

  const removed: CharSegment[] = []
  const added: CharSegment[] = []
  let i = 0, j = 0

  while (i < m || j < n) {
    if (i < m && j < n && a[i] === b[j]) {
      removed.push({ text: a[i]!, highlighted: false })
      added.push({ text: b[j]!, highlighted: false })
      i++; j++
    } else if (j < n && (i >= m || (dp[i]?.[j + 1] ?? 0) >= (dp[i + 1]?.[j] ?? 0))) {
      added.push({ text: b[j]!, highlighted: true })
      j++
    } else {
      removed.push({ text: a[i]!, highlighted: true })
      i++
    }
  }

  return { removed, added }
}

// Group consecutive segments of same highlight status
function groupSegments(segs: CharSegment[]): { text: string; highlighted: boolean }[] {
  if (segs.length === 0) return []
  const result: { text: string; highlighted: boolean }[] = []
  let current = { text: segs[0]!.text, highlighted: segs[0]!.highlighted }
  for (let i = 1; i < segs.length; i++) {
    const seg = segs[i]!
    if (seg.highlighted === current.highlighted) {
      current.text += seg.text
    } else {
      result.push(current)
      current = { text: seg.text, highlighted: seg.highlighted }
    }
  }
  result.push(current)
  return result
}

// Find matching lines between removed and added to compute char diffs
function pairLines(lines: DiffLine[]): Map<number, number> {
  const pairs = new Map<number, number>()
  const removedIdx: number[] = []
  const addedIdx: number[] = []
  lines.forEach((l, i) => {
    if (l.type === 'remove') removedIdx.push(i)
    if (l.type === 'add') addedIdx.push(i)
  })
  const count = Math.min(removedIdx.length, addedIdx.length)
  for (let k = 0; k < count; k++) {
    pairs.set(removedIdx[k]!, addedIdx[k]!)
  }
  return pairs
}

function renderLineContent(
  line: DiffLine,
  charDiffs: Map<number, { removed: CharSegment[]; added: CharSegment[] }>,
  removedLineIndex: number | null,
  pairIndex: number | null,
): React.ReactNode {
  if (line.type === 'remove' && removedLineIndex !== null) {
    const diff = charDiffs.get(removedLineIndex)
    if (diff) {
      const groups = groupSegments(diff.removed)
      return groups.map((g, i) =>
        g.highlighted
          ? <span key={i} className="diff-hl" style={{ background: 'rgba(255,69,58,0.35)', borderRadius: 2, fontSize: 12, fontFamily: "'Menlo','Courier New',monospace" }}>{g.text}</span>
          : <span key={i} style={{ fontSize: 12, fontFamily: "'Menlo','Courier New',monospace" }}>{g.text}</span>
      )
    }
  }
  if (line.type === 'add' && pairIndex !== null) {
    const diff = charDiffs.get(pairIndex)
    if (diff) {
      const groups = groupSegments(diff.added)
      return groups.map((g, i) =>
        g.highlighted
          ? <span key={i} className="diff-hl" style={{ background: 'rgba(255,255,255,0.25)', borderRadius: 2, fontSize: 12, fontFamily: "'Menlo','Courier New',monospace" }}>{g.text}</span>
          : <span key={i} style={{ fontSize: 12, fontFamily: "'Menlo','Courier New',monospace" }}>{g.text}</span>
      )
    }
  }
  return line.content
}

export function DiffView({ diff, filename }: DiffViewProps) {
  const lines = parseDiff(diff)

  // Pair remove/add lines for char-level diff
  const pairs = pairLines(lines)
  // Build reverse map: added line idx → removed line idx
  const addedToRemoved = new Map<number, number>()
  pairs.forEach((addedIdx, removedIdx) => addedToRemoved.set(addedIdx, removedIdx))

  // Compute char diffs for paired lines (keyed by added line index)
  const charDiffs = new Map<number, { removed: CharSegment[]; added: CharSegment[] }>()
  pairs.forEach((addedIdx, removedIdx) => {
    const removedLine = lines[removedIdx]
    const addedLine = lines[addedIdx]
    if (removedLine && addedLine && removedLine.content.length < 500 && addedLine.content.length < 500) {
      charDiffs.set(addedIdx, charDiff(removedLine.content, addedLine.content))
    }
  })

  return (
    <div style={{
      background: 'var(--surf2)',
      borderRadius: 8,
      overflow: 'hidden',
      fontSize: 12,
      fontFamily: "'Menlo','Courier New',monospace",
      WebkitTextSizeAdjust: '100%',
      lineHeight: '1.5',
    }}>
      {filename && (
        <div style={{
          padding: '6px 12px',
          background: 'var(--surf3)',
          color: 'var(--text2)',
          borderBottom: '1px solid var(--border)',
          display: 'flex',
          alignItems: 'center',
          gap: 6,
          fontSize: 12,
          fontFamily: "'Menlo','Courier New',monospace",
        }}>
          <span>📄</span>
          <span>{filename}</span>
        </div>
      )}
      <div style={{ overflowX: 'auto' }}>
        {lines.map((line, i) => {
          // For remove lines: the charDiff is keyed by the paired added-line index
          const pairedAddedIdx = (line.type === 'remove') ? (pairs.get(i) ?? null) : null
          // For add lines: the charDiff is keyed by this line's own index (which is the added-line key)
          const pairIndex = (line.type === 'remove')
            ? pairedAddedIdx  // key to charDiffs for removed lines is the addedIdx
            : (line.type === 'add' ? i : null)
          // removedLineIndex: for remove lines, maps to the charDiff's .removed segments
          const removedLineIndex = (line.type === 'remove') ? (pairedAddedIdx ?? null) : null

          return (
            <div key={i} style={{ display: 'flex', fontSize: 12, fontFamily: "'Menlo','Courier New',monospace", ...LINE_STYLES[line.type] }}>
              <span style={{
                width: 20,
                textAlign: 'center',
                flexShrink: 0,
                fontSize: 12,
                fontFamily: "'Menlo','Courier New',monospace",
                color: line.type === 'add' ? 'var(--green)' : line.type === 'remove' ? 'var(--red)' : 'var(--text3)',
                userSelect: 'none',
              }}>
                {GUTTER[line.type]}
              </span>
              <span style={{ whiteSpace: 'pre', padding: '1px 8px 1px 4px', fontSize: 12, fontFamily: "'Menlo','Courier New',monospace" }}>
                {renderLineContent(line, charDiffs, removedLineIndex, pairIndex)}
              </span>
            </div>
          )
        })}
      </div>
    </div>
  )
}
