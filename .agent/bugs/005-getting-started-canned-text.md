# BUG-005: Getting Started onboarding should prompt Claude, not show canned text

**Severity**: Low — feature enhancement, not a crash  
**Component**: spa (App.tsx)  
**Reported**: 2026-04-16  

## Symptoms

- User opens MClaude for the first time
- "Getting Started" session is created automatically
- A hardcoded self-introduction message is sent as a user message:
  > "Hi! I'm Claude. You're in MClaude — a real-time coding environment powered by Claude Code..."
- This appears as if the USER typed it, which is confusing
- Claude doesn't actually respond to explain itself — the canned text IS the entire onboarding

## Current Behavior

`App.tsx:183-191` sends a hardcoded multi-line string via `conversationVM.sendMessage()`. This goes through the normal user message flow — it appears as a blue right-aligned user bubble, not as a system message or Claude response.

The message content:
```
Hi! I'm Claude. You're in MClaude — a real-time coding environment powered by Claude Code.

Here's what you can do here:
- Write and edit files across your project
- Run shell commands (git, npm, make, etc.)
- Search and read your codebase
- Create more sessions for different tasks or branches

Ask me anything to get started — like "what's in this project?" or "help me fix this bug". What would you like to work on?
```

## Expected Behavior

The Getting Started session should **prompt Claude** to introduce itself and the environment. Options:

1. **Specialized prompt**: Send a prompt like "Explain what MClaude is and what I can do here" that triggers Claude to respond naturally with contextual information about the project/environment
2. **User-level skill**: Create a `/getting-started` skill that Claude invokes, which provides structured onboarding based on the project's CLAUDE.md, available tools, and environment
3. **System message + prompt**: Show a system-style message (not user bubble) explaining the environment, then send a prompt that makes Claude respond with project-specific context ("What's in this repo?")

The key insight: Claude should be **responding**, not the user message impersonating Claude.

## Files

- `mclaude-web/src/components/App.tsx:171-198` — first-run flow with hardcoded message
- `mclaude-web/src/viewmodels/conversation-vm.ts:49-62` — sendMessage flow
