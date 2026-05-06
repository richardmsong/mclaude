# Spec: UI First-Run Flow

Onboarding flow triggered on first login when no sessions exist. Shared across all UI components.

## Token Gate (ADR-0092)

Before the first-run flow, the SPA checks for a Claude token. On login, the SPA hydrates from `mclaude-user-info-{uslug}` KV. If `tokens.anthropic` is absent, the Connect sheet (bottom sheet) auto-opens over an empty/dimmed background with [✕] disabled (token is mandatory). The user connects via OAuth PKCE popup or manual paste. After the token is written to KV, the SPA auto-delivers K_wrapped to the default platform. Session creation is blocked until delivery succeeds (30s timeout with [Try again]).

If the token already exists in KV (returning user), this gate is skipped and the dashboard renders normally.

**Delivery timeout behavior:** If the default platform user-agent is offline, the SPA shows a spinner ("Setting up your environment…") with a 30s timeout. After timeout: spinner stops, error message with [Try again] button appears. [Try again] retries delivery immediately and resets the 30s timer. The Connect sheet remains visible behind the error. If the user dismisses the Connect sheet (navigates away), the pending delivery is written to `localStorage` at `mclaude.pendingDeliveries` and retried on next login. The first-run flow (project + session creation) does not proceed until delivery succeeds.

## First-Run Flow

Triggered when the user logs in, has a valid token (gate passed), and no sessions exist in any project in the KV store (checked after ~1s watch-settle delay). This handles the case where the server has seeded a project (e.g. "Default Project") with no sessions yet.

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
