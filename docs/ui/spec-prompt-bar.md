# Spec: UI Prompt Bar

Interaction contract for the Claude Code `/ask` prompt bar (distinct from the AskUserQuestion tool event). Shared across all UI components.

## Interaction: Prompt Bar

Shown when session has a pending question (distinct from AskUserQuestion tool — this is a Claude Code `/ask` prompt):

```
┌─────────────────────────────────┐
│ What would you like to do?      │  question text
│ [1. Continue]  [2. Stop]        │  option buttons
└─────────────────────────────────┘
```

Option buttons: pill shape, `--surf2` background, tapping sends the option number as input.
