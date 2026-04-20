# Spec: UI Inline Diff View

GitHub-style unified diff with char-level highlighting. Shared contract — every UI component must implement the full diff view including char-level highlighting.

## Component: Inline Diff View (`.diff-view`)

GitHub-style unified diff with char-level highlighting.

```
┌─────────────────────────────────┐
│ 📄 src/main.go                  │  filename header (optional)
├─────────────────────────────────┤
│   package main                  │  context line (--text3 background)
│ − import "fmt"                  │  removed line (--red bg: rgba(255,69,58,.12))
│ + import "log"                  │  added line (--green bg: rgba(48,209,88,.12))
│   func main() {                 │  context line
└─────────────────────────────────┘
```

- Gutter column: `−` for removed, `+` for added, space for context
- Char-level highlights: `<span class="diff-hl">` — darker background within the line (rgba(255,255,255,.25) for additions, rgba(255,69,58,.35) for removals)
- Monospace font, 12px, `--surf2` base background
- Horizontally scrollable for long lines
- Every element (container, line div, gutter span, content span) must explicitly set `fontSize: 12` and `fontFamily: monospace` — never rely on inheritance; add `-webkit-text-size-adjust: 100%` to prevent iOS scaling
