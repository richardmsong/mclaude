# Spec: UI First-Run Flow

Onboarding flow triggered on first login when no sessions exist. Shared across all UI components.

## First-Run Flow

Triggered when the user logs in and no sessions exist in any project in the KV store (checked after ~1s watch-settle delay). This handles the case where the server has seeded a project (e.g. "Default Project") with no sessions yet.

1. If no projects exist, client calls `projects.create` with `{ name: "Default" }` to create one first.
2. Client calls `sessions.create` in the first available project with `{ name: "Getting Started" }`
3. Client navigates directly to the new session
4. Client sends the pre-seeded onboarding message as the first user turn:

> Hi! I'm Claude. You're in MClaude — a real-time coding environment powered by Claude Code.
>
> Here's what you can do here:
> - Write and edit files across your project
> - Run shell commands (git, npm, make, etc.)
> - Search and read your codebase
> - Create more sessions for different tasks or branches
>
> Ask me anything to get started — like "what's in this project?" or "help me fix this bug". What would you like to work on?

The flow runs once. After the default project is created it appears in the KV store on subsequent logins.
