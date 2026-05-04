## Run: 2026-04-26 (round 1)

All gaps SPEC->FIX. Specs need updating to reflect ADR-0036 decisions.

1. spec-cli.md: missing `clear` event in wire protocol
2. spec-cli.md: missing `parent_tool_use_id` nesting
3. spec-cli.md: missing explicit accumulator omission list
4. spec-cli.md: missing exit-on-socket-drop behavior
5. spec-conversation-events.md: missing SystemMessageBlock type + isSynthetic rule
6. spec-conversation-events.md: missing `[SYSTEM NOTIFICATION` discard rule + dedup algorithm
7. (PARTIAL) spec-conversation-events.md: System Event doesn't distinguish CompactionBlock vs SystemMessageBlock
8. (PARTIAL) spec-conversation-events.md: missing `clear` event rendering

All fixed by master session in spec edits below.
