# Repository Guidelines

## Project Structure

```
mclaude/
├── mclaude-cli/            # Go — CLI binary (plugin host)
├── mclaude-common/         # Go — shared types and utilities
├── mclaude-connector/      # Go — Tailscale connector daemon
├── mclaude-control-plane/  # Go — Kubernetes controller
├── mclaude-relay/          # Go — NATS relay
├── mclaude-session-agent/  # Go — per-session Claude agent runner
├── mclaude-server/         # Swift (Hummingbird) — WebSocket/TLS server
├── mclaude-web/            # TypeScript/React — browser SPA
├── mclaude-docs-mcp/       # TypeScript/Bun — docs MCP server
├── mclaude-docs-dashboard/ # TypeScript/Bun — local docs dashboard
├── charts/                 # Helm charts for Kubernetes deployment
├── docs/                   # ADRs (adr-XXXX-*.md) + per-component specs
└── scripts/                # Developer helper scripts
```

All Go modules share a `go.work` workspace at the repo root. The two Bun/TypeScript packages are linked via a root `package.json` workspace.

## Build, Test, and Development Commands

**Go modules** (run inside the relevant module directory):
```bash
go test -race ./...     # unit tests with race detector
go build ./...          # compile check
```

**mclaude-web** (Vite + Vitest):
```bash
npm test                # run unit tests (vitest)
npm run lint            # type-check (tsc --noEmit)
npm run build           # production build
npm run dev             # local dev server
```

**mclaude-docs-mcp / mclaude-docs-dashboard** (Bun):
```bash
bun test                # unit tests
bun run start           # start server locally
```

**Deployments — CI owns builds, never run locally:**
- Never run `helm upgrade`, `docker build`, or `k3d image import` yourself.
- Trigger and monitor CI via `gh run view {id}` (do not use `gh run watch`).

## Coding Style & Naming Conventions

- **Go**: standard `gofmt` formatting; idiomatic Go error handling; no external linters enforced beyond `go vet`.
- **TypeScript**: strict mode enabled in all `tsconfig.json` files; use the existing library choices (React, Vite, Bun) — do not introduce new frameworks without an ADR.
- **Swift**: Swift 5.10+, macOS 14 target; follow standard Swift API design guidelines.
- **File names**: kebab-case for docs (`spec-session-agent.md`), package-style for Go (`snake_case` for vars, `CamelCase` for types).

## Testing Guidelines

| Component | Framework | Command |
|-----------|-----------|---------|
| Go modules | `testing` + `testify` | `go test -race ./...` |
| mclaude-web | Vitest + Testing Library | `npm test` |
| mclaude-docs-* | Bun test runner | `bun test` |
| mclaude-web e2e | Playwright | `npm run e2e` |

Run tests for the affected component before opening a PR. CI runs tests per-component based on changed paths.

## ADR-Driven Development

Every non-trivial change (feature, bug fix, refactor) requires an ADR before implementation:

1. **New feature** → invoke `/plan-feature` skill immediately to draft an ADR.
2. **Any app change** → run `/feature-change` first; it checks/updates the spec, commits, then calls `/dev-harness`.
3. ADRs live in `docs/adr-XXXX-<slug>.md`. Status lifecycle: `draft → accepted → implemented`.
4. Component living specs live in `docs/<component>/spec-<topic>.md` and describe current code behavior.

## Commit & Pull Request Guidelines

Commit message format:
```
type(scope): short description (ADR-XXXX)
```

Common types: `feat`, `fix`, `spec`, `refactor`, `test`, `chore`.

Examples:
```
feat(mclaude-docs-dashboard): bind to 0.0.0.0 + multi-URL startup banner (ADR-0028)
spec(component-specs): review pass — fix 4 spec inaccuracies, co-commit with ADR-0025
fix(mclaude-docs-dashboard): edge tooltip last_commit + lineage error 500 fix
```

- PRs should reference the relevant ADR in description.
- Spec commits and implementation commits are often co-committed in a single message.
- Always run `git diff --cached` before committing to verify no secrets or credentials are staged.
