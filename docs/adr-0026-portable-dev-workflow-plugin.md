# ADR: Portable Dev Workflow Plugin

**Status**: draft
**Status history**:
- 2026-04-20: draft

## Overview

Extract the ADR/spec development workflow (skills, agents, docs MCP server, entrypoint script, and pre-tool-use hook) from the mclaude project into a standalone Claude Code plugin called `spec-driven-dev`. This makes the structured design → audit → implement → verify loop available in any repository.

## Motivation

The mclaude project evolved a powerful development workflow: plan-feature (ADR authoring with Q&A), design-audit (fresh-context evaluation loops), spec-evaluator (compliance checking), feature-change (orchestration), dev-harness (implementation), and file-bug (documentation). These are currently embedded in `.agent/skills/` and `.agent/agents/` inside the mclaude repo, but the patterns are project-agnostic — any repo with a `docs/` directory of ADRs and specs can use them.

The docs MCP server (`mclaude-docs-mcp/`) provides FTS5 search, section retrieval, and git-lineage tracking over markdown docs. It's also project-agnostic — it just needs a git repo with markdown files.

The master.sh entrypoint enforces a master/agent separation: the master session can only read code + edit docs + spawn agents, while dev-harness agents do the actual coding. This pattern is generic — it just needs a list of source directories.

Currently, starting a new project means either not having these workflows or manually copying files. A plugin solves this — install once, available everywhere.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Distribution | New public GitHub repo at `github.com/richardmsong/spec-driven-dev` registered via `claude plugin marketplace add <url>` | Cloneable from any machine; public so others can install; published under the `richardmsong` GitHub organization (not the user's personal account). |
| Plugin name | `spec-driven-dev` | Emphasizes the spec-first methodology. Clear what it does. |
| Docs MCP discovery | CLI arg `--root <path>` with walk-up-to-`.git` fallback | Explicit when configured via `.mcp.json`, still works standalone for debugging |
| Docs MCP packaging | Source in plugin repo at `docs-mcp/`; compiled binary at `${CLAUDE_PLUGIN_ROOT}/bin/docs-mcp` | No npm publish needed. Plugin `.mcp.json` references the binary via `${CLAUDE_PLUGIN_ROOT}`. Setup skill compiles it in place with `bun build --compile`. |
| Docs dashboard | Moves into plugin at `docs-dashboard/` | Dashboard imports 5 modules of `docs-mcp`. Rather than duplicating source, cross-repo-publishing, or fragile shim packages, co-locate both services in the plugin — they share the `.docs-index.db` and the boot/watcher lifecycle. |
| Which skills move | Core workflow: plan-feature, design-audit, feature-change, spec-evaluator, file-bug | Deploy-*, nats-send, job-queue, schedule-feature are mclaude-specific and stay |
| Which agents move | All three: design-evaluator, dev-harness, spec-evaluator | They implement the generic ADR/spec loop — descriptions generalized to remove "mclaude" references |
| Skill generalization | Generic patterns + conventions doc in plugin | Skills reference `docs/adr-*.md` and `docs/**/spec-*.md`. Plugin includes a conventions doc explaining expected repo structure. Project-specific context (component lists, doc index) goes in CLAUDE.md. |
| mclaude-docs-mcp spec | Deleted from mclaude; plugin owns its own spec at `spec-driven-dev/docs/spec-docs-mcp.md` | mclaude no longer owns the component, so the spec follows the source. |
| mclaude-docs-dashboard spec | Deleted from mclaude; plugin owns its own spec at `spec-driven-dev/docs/spec-docs-dashboard.md` | Same rationale — dashboard also moves to the plugin. |
| mclaude after extraction | CLAUDE.md only — no thin wrappers | Plugin skills are fully self-contained. mclaude-specific context goes in CLAUDE.md. |
| Hook design | Config-driven blocklist with ban/guard categories | Plugin ships a generic `pre-tool-use.sh` that reads `.agent/blocked-commands.json`. Two categories: `ban` (never overridable) and `guard` (overridable via `SDD_DEBUG=1`). |
| Hook installation | Plugin `hooks/hooks.json` with per-project opt-in | Plugin declares the hook in `hooks/hooks.json` (standard plugin hook format). The hook script checks for `.agent/blocked-commands.json` at runtime — if the file doesn't exist, the hook exits silently (no-op). Projects opt in by creating the config file via `/spec-driven-dev:setup`. |
| Debug override | Single `SDD_DEBUG=1` env var | Relaxes all `guard`-category blocks. Bans are never overridable. Simple — one flag to remember. |
| Master entrypoint | Plugin provides generic `sdd-master` script at `${CLAUDE_PLUGIN_ROOT}/bin/sdd-master` | Reads `.agent/master-config.json` for source directories, auto-generates `--disallowedTools` for Edit/Write on those dirs. Setup skill symlinks to `~/.local/bin/` for CLI convenience. Model probe cache at `$HOME/.cache/sdd/master-model`. |
| Workflow rules delivery | Setup skill writes a managed `<!-- sdd:workflow-rules BEGIN -->` … `<!-- sdd:workflow-rules END -->` block into the project's `./CLAUDE.md` | Plugin CLAUDE.md does not auto-load (confirmed). Managed markers make re-running setup idempotent — only the block between markers is ever touched; user content outside the markers is preserved. Block content is read verbatim from the plugin's `docs/conventions.md#workflow-rules` section. |
| Skill conventions | Plugin assumes two universal patterns: (1) ADR files at `docs/adr-NNNN-<slug>.md` (root only); (2) spec files at `docs/spec-*.md` (root, cross-cutting) and `docs/<component>/spec-*.md` (component-local). Plugin ships its own `spec-doc-layout.md` as a copy-into-project template. | The root-vs-subfolder partition for cross-cutting vs component-local specs is a universal pattern — every project with multiple components benefits from it. mclaude-specific file names like `feature-list.md`, `spec-state-schema.md`, `spec-tailscale-dns.md` are dropped from plugin skill references; those remain mclaude-local conventions. |
| Plugin CI | GitHub Actions running `bun test` on push for both `docs-mcp` and `docs-dashboard` packages, plus hook regex unit tests | Safe dependency for anyone who installs the plugin. Prevents regressions in the extracted code. |
| Setup idempotency | `/spec-driven-dev:setup` is always idempotent; no explicit first-time vs per-project mode | Checks each item independently (binary compiled? symlinks in `~/.local/bin`? `.agent/blocked-commands.json` exists? workflow rules block present in CLAUDE.md?). Running in a fully-configured project is a no-op. Simpler user model. |
| plugin.json contents | Minimal: `name`, `version`, `description` only | No Claude Code version pin for v1. Add compat fields if a specific Claude Code behavior proves required. |
| Extraction sequencing | Two phases: (1) create + populate plugin repo, verify both packages + CI pass; (2) mclaude cleanup PR that deletes extracted files and installs the plugin | During phase 1, mclaude still works from its own `mclaude-docs-mcp/` + `mclaude-docs-dashboard/`. Plugin is a parallel, verified-working copy. Phase 2 is a clean cutover; if anything is wrong, phase 2 can be reverted without touching the plugin repo. |

## User Flow

### Install (per machine, once)

1. Register plugin: `claude plugin marketplace add https://github.com/richardmsong/spec-driven-dev`
2. Install plugin: `claude plugin install spec-driven-dev`

### Setup (per project — also covers machine-level items on first run)

Run `/spec-driven-dev:setup` in the project. The skill is fully idempotent and checks each item independently:

- **Machine-level** (each item checked independently; skipped if already satisfied):
  - `bun` is on `PATH` — `command -v bun` exits 0. If not, abort with a clear message: "`bun` is required to compile docs-mcp. Install via: curl -fsSL https://bun.sh/install | bash".
  - Binary present: `[ -x "${CLAUDE_PLUGIN_ROOT}/bin/docs-mcp" ]`. If the file is missing or not executable, `mkdir -p "${CLAUDE_PLUGIN_ROOT}/bin"` then run the compile. (No version check in v1 — the setup skill re-compiles only when the file is absent. A plugin upgrade that changes source forces a recompile by deleting the binary; add proper version stamping later if needed.)
  - `sdd-master` symlink: `[ -L "$HOME/.local/bin/sdd-master" ]` pointing at `${CLAUDE_PLUGIN_ROOT}/bin/sdd-master`. `mkdir -p "$HOME/.local/bin"` and `ln -sfn "${CLAUDE_PLUGIN_ROOT}/bin/sdd-master" "$HOME/.local/bin/sdd-master"` if not.
  - `sdd-dashboard` symlink: same check + `ln -sfn` against `${CLAUDE_PLUGIN_ROOT}/bin/sdd-dashboard`.
  - `PATH` contains `~/.local/bin` — `case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) echo "warning: $HOME/.local/bin is not on PATH" ;; esac`. Warn only; do not modify the user's shell rc.
- **Project-level** (each item checked independently; created if missing):
  - `.agent/` directory: `[ -d .agent ]` — `mkdir -p .agent` if not.
  - `.agent/blocked-commands.json`: `[ -f .agent/blocked-commands.json ]` — write the default (bans for `gh run watch`, `git apply`) if not.
  - `.agent/master-config.json`: `[ -f .agent/master-config.json ]` — write `{"source_dirs": []}` if not.
  - `./CLAUDE.md` at the project root (not `.claude/CLAUDE.md`) contains the managed block. Detection: grep for the literal `<!-- sdd:workflow-rules BEGIN -->` marker. If the file is absent, create it. If the marker is absent, append the block to the end of the file. If the marker is present, replace every line between `BEGIN` and `END` (inclusive of both marker lines) with a freshly-rendered block. Block content is read from `${CLAUDE_PLUGIN_ROOT}/docs/conventions.md` — extract the lines between the `## workflow-rules` heading and the next `## ` heading, drop the heading line itself, and wrap the result in the BEGIN/END markers. User content outside the markers is never touched.
  - `.gitignore` contains `.agent/.docs-index.db*` — `grep -qF '.agent/.docs-index.db' .gitignore 2>/dev/null`. Append the pattern if not present. Create the file if it doesn't exist.

Then:
- Edit `.agent/master-config.json` to list source directories that only agents should modify.
- Optionally append project-specific guard rules to `.agent/blocked-commands.json`.
- Launch via `sdd-master` (master session with the configured `--disallowedTools`) instead of `claude`.

### Daily workflow

1. `/plan-feature <description>` → creates ADR in `docs/`, Q&A until all ambiguities resolved.
2. `/design-audit docs/adr-NNNN-slug.md` → fresh-context evaluation loop until CLEAN.
3. `/feature-change <description>` → checks spec → spawns dev-harness → implements + tests.
4. `/spec-evaluator [component]` → compliance audit.
5. `/file-bug <description>` → documents bug in `.agent/bugs/`.
6. `sdd-dashboard` (separate shell) → launches the docs dashboard for the current project.

## Component Changes

### Plugin repo: `spec-driven-dev/` (new, hosted on GitHub)

```
spec-driven-dev/
├── .claude-plugin/
│   └── plugin.json                    # name, version, description, compat
├── .mcp.json                          # flat keys, uses ${CLAUDE_PLUGIN_ROOT}
├── package.json                       # workspaces: docs-mcp, docs-dashboard
├── bun.lock
├── docs-mcp/                          # extracted from mclaude-docs-mcp/ — unchanged behavior except --root arg and db path
│   ├── src/
│   │   ├── index.ts                   # accepts --root <path>, fallback walk-up-to-.git
│   │   ├── db.ts
│   │   ├── content-indexer.ts
│   │   ├── lineage-scanner.ts
│   │   ├── parser.ts
│   │   ├── tools.ts
│   │   └── watcher.ts
│   ├── tests/                         # moved from mclaude-docs-mcp/tests/
│   └── package.json                   # name: "docs-mcp" (drop "mclaude-" prefix); keeps exports map
├── docs-dashboard/                    # extracted from mclaude-docs-dashboard/
│   ├── src/
│   │   ├── boot.ts                    # db path changes to <repo-root>/.agent/.docs-index.db
│   │   ├── server.ts
│   │   ├── routes.ts
│   │   └── graph-queries.ts
│   ├── ui/                            # Vite/React UI
│   ├── tests/
│   └── package.json                   # name: "docs-dashboard"; depends on "docs-mcp": "workspace:*"
├── bin/
│   ├── docs-mcp                       # compiled binary (built by setup skill via bun build --compile)
│   ├── sdd-master                     # generic master entrypoint script (see "sdd-master entrypoint" section)
│   └── sdd-dashboard                  # dashboard launcher (see "sdd-dashboard entrypoint" section — walks up from $PWD, sets SDD_REPO_ROOT, execs bun run "${CLAUDE_PLUGIN_ROOT}/docs-dashboard/src/server.ts" WITHOUT cd'ing)
├── skills/
│   ├── setup/SKILL.md                 # /spec-driven-dev:setup — compile binary + project init + install workflow block
│   ├── plan-feature/SKILL.md          # generalized (see "Skill generalization" below)
│   ├── design-audit/SKILL.md          # already generic — no changes needed
│   ├── feature-change/SKILL.md        # generalized (see below)
│   ├── spec-evaluator/SKILL.md        # generalized (see below)
│   └── file-bug/SKILL.md              # generalized (see below)
├── agents/                             # directory-per-agent layout (Claude Code scans `<plugin-root>/agents/` at load time)
│   ├── design-evaluator/
│   │   └── AGENT.md                   # already generic; just moved
│   ├── dev-harness/
│   │   └── AGENT.md                   # generalized (see below)
│   └── spec-evaluator/
│       └── AGENT.md                   # generalized (see below)
├── hooks/
│   ├── hooks.json                     # plugin hook registration (PreToolUse on Bash → points at pre-tool-use.sh)
│   └── pre-tool-use.sh                # reads $CLAUDE_PROJECT_DIR/.agent/blocked-commands.json
└── docs/
    ├── conventions.md                 # see "conventions.md content" below
    ├── spec-docs-mcp.md               # moved from mclaude (generalized: repo-root-relative, .agent/.docs-index.db)
    ├── spec-docs-dashboard.md         # moved from mclaude (if one exists; otherwise newly authored)
    └── spec-doc-layout.md             # moved from mclaude — root cross-cutting vs component-local partitioning rules
```

### `conventions.md` content

Ships at `spec-driven-dev/docs/conventions.md`. Three H2 sections. The `## workflow-rules` section's content (lines between the H2 heading and the next H2) is read verbatim by the setup skill and written between the managed markers in each project's `./CLAUDE.md`.

````markdown
# spec-driven-dev conventions

## Repo structure

ADRs live at `docs/adr-NNNN-<slug>.md` (root only, never nested). Specs live at:
- `docs/spec-*.md` — cross-cutting specs (affect multiple components).
- `docs/<component>/spec-*.md` — component-local.
- `docs/ui/spec-*.md` and `docs/ui/<ui-component>/spec-*.md` — UI shared contracts and component-local UI specs.

See `spec-doc-layout.md` (shipped in this directory) for the full rules.

## workflow-rules

### All app changes — /feature-change first

**Never write implementation code directly for any app change.**
Every change — feature, bug fix, refactor, config, UI tweak, backend change — goes through `/feature-change` first.

The loop: `/feature-change` checks the spec → updates spec if needed → commits spec → calls `/dev-harness <component>` → implements and tests.

For bug fixes where the spec is already correct, `/feature-change` skips the spec update and goes straight to `/dev-harness`.

### New feature detected → invoke /plan-feature immediately

**When the user describes anything that looks like a potential new feature, jump straight into `/plan-feature` — don't wait for the full picture, don't rely on keeping it in memory.**

Planning context is lost when you get compacted or switched out. The ADR on disk is the durable form. Start `/plan-feature` on the first mention, even mid-conversation, even if there are still open questions — drafts are first-class and can be paused, committed, and resumed.

Heuristic: if the user says something like "maybe we should…", "what if…", "could we add…", "I want to…", or describes a capability the app doesn't have yet → that's `/plan-feature`. Don't ask permission; just start the skill and let the Q&A surface the rest.

### Polling — keep checking until done

After triggering any async operation (CI run, build, long-running command), **keep polling** until it resolves. Do not summarize and hand back to the user mid-flight.

### Parallelism — use subagents for independent work

**When requests can be parallelized, use subagents extensively rather than handling them sequentially.**

Examples of work that should run in parallel via `Agent(run_in_background=true)`:
- Spec evaluator + CI polling while a build runs
- Multiple component audits or implementations at the same time
- Research tasks (reading spec docs, reading code, web searches) that don't depend on each other

Launch multiple agents in a single message when their work is independent. Don't serialize tasks that can overlap.

### Before debugging a config mismatch — read the source

Before guessing at a port, command, or env var, read the relevant Dockerfile / config file first. The answer is always there.

## Expected files in each project

Setup creates (and maintains) these files in the project:
- `.agent/blocked-commands.json` — PreToolUse blocklist (see main plugin README).
- `.agent/master-config.json` — source directories that only agents should edit.
- `./CLAUDE.md` — the `<!-- sdd:workflow-rules -->` block above is written here.

The project owns its `docs/` tree (ADRs and specs) and the content of `CLAUDE.md` outside the managed block.
````

The setup skill finds the `## workflow-rules` heading, collects every line until the next `## ` heading, trims the heading line itself, and writes the remaining text between `<!-- sdd:workflow-rules BEGIN -->` and `<!-- sdd:workflow-rules END -->` in the project's `./CLAUDE.md`.

### docs-mcp changes

- `src/index.ts`: replace the hardcoded `resolve(join(scriptDir, "..", ".."))` repo-root computation with `--root <path>` CLI arg parsing; if `--root` is absent, walk up from `process.cwd()` to find a directory containing `.git` and use that as the repo root.
- `package.json`: rename `"name"` to `"docs-mcp"` (drop the `mclaude-` prefix). Keep the existing `exports` map so the co-located dashboard can import `docs-mcp/{parser,db,content-indexer,lineage-scanner,watcher,tools}`. Add a `"bin"` field:
  ```json
  "bin": { "docs-mcp": "./src/index.ts" }
  ```
  Add a `"compile"` script that uses a **relative** outfile path — no env-var expansion is needed inside the script's shell, which avoids the subshell-env-loss pitfall:
  ```json
  "scripts": {
    "start": "bun run src/index.ts",
    "test": "bun test",
    "compile": "bun build --compile --target=bun ./src/index.ts --outfile ../bin/docs-mcp"
  }
  ```
  The setup skill runs this script from the plugin's `docs-mcp/` directory so the relative `../bin/docs-mcp` resolves to `${CLAUDE_PLUGIN_ROOT}/bin/docs-mcp`:
  ```bash
  cd "${CLAUDE_PLUGIN_ROOT}/docs-mcp" && bun run compile
  ```
  The setup skill is responsible for ensuring `${CLAUDE_PLUGIN_ROOT}/bin/` exists (`mkdir -p`) before invoking the compile.
- `.docs-index.db` path: changes from `<repo-root>/mclaude-docs-mcp/.docs-index.db` to `<repo-root>/.agent/.docs-index.db`. Update the default in both `docs-mcp/src/index.ts` and `docs-dashboard/src/boot.ts`. The setup skill's project-level step ensures the `.agent/` directory exists before first boot.

### docs-dashboard changes

- `package.json`: rename to `"docs-dashboard"`; update dep from `"mclaude-docs-mcp": "workspace:*"` to `"docs-mcp": "workspace:*"`
- `src/boot.ts`: update import paths from `"mclaude-docs-mcp/..."` to `"docs-mcp/..."`; update default db path (as above)
- `src/routes.ts`: same import rename
- Existing behavior unchanged — the dashboard still discovers the repo root via walk-up from CWD, so running `sdd-dashboard` inside any project works identically.

### `.mcp.json` in plugin

Uses flat-key format (no `"mcpServers"` wrapper) per the plugin `.mcp.json` convention:

```json
{
  "docs": {
    "command": "${CLAUDE_PLUGIN_ROOT}/bin/docs-mcp",
    "args": ["--root", "${CLAUDE_PROJECT_DIR}"]
  }
}
```

`${CLAUDE_PLUGIN_ROOT}` resolves to the plugin's install directory. `${CLAUDE_PROJECT_DIR}` resolves to the current project root. If `CLAUDE_PROJECT_DIR` is empty/unset, docs-mcp falls back to walking up from CWD to find `.git`.

### Pre-tool-use hook

`hooks/pre-tool-use.sh` — generic script invoked by Claude Code as a `PreToolUse` hook matched on the `Bash` tool. Behavior:

1. Read the stdin JSON envelope that Claude Code sends. The envelope shape is:
   ```json
   {
     "session_id": "...",
     "transcript_path": "...",
     "cwd": "...",
     "permission_mode": "default",
     "hook_event_name": "PreToolUse",
     "tool_name": "Bash",
     "tool_use_id": "...",
     "tool_input": { "command": "...", "description": "...", "timeout": 120000, "run_in_background": false }
   }
   ```
   Extract the command string from `tool_input.command`.
2. Read `$CLAUDE_PROJECT_DIR/.agent/blocked-commands.json`. If the file does not exist, exit 0 silently (no blocks, no-op).
3. Iterate each rule. Match the command against the rule's `pattern` (POSIX ERE). If the pattern matches:
   - If `category == "ban"`: emit a `deny` decision and exit 0.
   - If `category == "guard"`: if `SDD_DEBUG=1` is set in the environment, continue (the rule is overridden); otherwise emit a `deny` decision and exit 0.
4. If no rule matches, exit 0 (allow).

The `deny` decision is written to stdout as the standard Claude Code hook JSON response:
```json
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "deny",
    "permissionDecisionReason": "<rule message>"
  }
}
```

### `hooks/hooks.json`

Registers the hook with Claude Code's plugin runtime:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "${CLAUDE_PLUGIN_ROOT}/hooks/pre-tool-use.sh"
          }
        ]
      }
    ]
  }
}
```

`${CLAUDE_PLUGIN_ROOT}` is substituted by Claude Code at hook-invocation time to the plugin's install directory. `$CLAUDE_PROJECT_DIR` is an env var available to the hook script that resolves to the project root (used inside `pre-tool-use.sh` to locate the project's `.agent/blocked-commands.json`).

Default `.agent/blocked-commands.json` created by setup:
```json
{
  "rules": [
    {
      "pattern": "gh\\s+run\\s+watch",
      "message": "Blocks until timeout. Use 'gh run view {id}' to poll.",
      "category": "ban"
    },
    {
      "pattern": "git\\s+apply",
      "message": "Bypasses the spec→dev-harness→evaluator loop. Use /feature-change.",
      "category": "ban"
    }
  ]
}
```

mclaude adds project-specific guards:
```json
{
  "rules": [
    {"pattern": "gh\\s+run\\s+watch", "message": "...", "category": "ban"},
    {"pattern": "git\\s+apply", "message": "...", "category": "ban"},
    {"pattern": "helm\\s+(upgrade|install)", "message": "Must run via CI. Set SDD_DEBUG=1 to override.", "category": "guard"},
    {"pattern": "docker\\s+build", "message": "Must run via CI. Set SDD_DEBUG=1 to override.", "category": "guard"},
    {"pattern": "k3d\\s+image\\s+import", "message": "Bypasses CI. Set SDD_DEBUG=1 to override.", "category": "guard"},
    {"pattern": "kubectl\\s+(create|apply|patch|delete|replace|edit|scale|rollout\\s+restart|exec)", "message": "Cluster state managed by Helm. Set SDD_DEBUG=1 to override.", "category": "guard"}
  ]
}
```

### `sdd-master` entrypoint

Reads `.agent/master-config.json` from the current working directory (which must be inside a git repo):
```json
{
  "source_dirs": [
    "mclaude-control-plane/**/*.go",
    "mclaude-session-agent/**/*.go",
    "mclaude-web/src/**"
  ]
}
```

Each entry is a repo-root-relative glob. For each one, the script appends two `--disallowedTools` args to the `claude` invocation: `Edit(<glob>)` and `Write(<glob>)`. The format matches what `scripts/master.sh` currently emits.

Includes the Opus model probing logic from current `master.sh`. Model probe cache moves from `$HOME/.cache/mclaude/master-model` to `$HOME/.cache/sdd/master-model`.

If `.agent/master-config.json` is missing or `source_dirs` is empty, the script still runs `claude` with only the model arg (no `--disallowedTools`) — master can then edit anything.

### `sdd-dashboard` entrypoint

Single-purpose launcher:

1. Walk up from `$PWD` until it finds a `.git` directory. Abort with a clear error if none is found.
2. Export `SDD_REPO_ROOT=<resolved-repo-root>` into the launched process's environment.
3. Exec `bun run "${CLAUDE_PLUGIN_ROOT}/docs-dashboard/src/server.ts"` **without** `cd`-ing into the plugin dir (so Bun's cwd-relative behavior doesn't interact with walk-up logic).

`docs-dashboard/src/boot.ts` is updated to read the repo root in this order:

1. `process.env.SDD_REPO_ROOT` if non-empty (preferred path when launched via `sdd-dashboard`).
2. Otherwise `findRepoRoot(process.cwd())` — preserves the existing walk-up behavior for direct `bun run src/server.ts` invocations during development.

Dashboard port, UI build path, and HTTP routes are unchanged from the current `mclaude-docs-dashboard` behavior — this is a behavior-preserving move.

### mclaude project cleanup

After plugin extraction:
- **Remove** from `.agent/skills/`: plan-feature, design-audit, feature-change, spec-evaluator, file-bug (and their symlinks under `.claude/skills/`)
- **Remove** from `.agent/agents/`: design-evaluator, dev-harness, spec-evaluator (and their symlinks under `.claude/agents/`)
- **Remove** `.agent/hooks/pre-tool-use.sh` (plugin hook replaces it)
- **Keep** in `.agent/skills/`: deploy-connector, deploy-local-preview, deploy-preview, deploy-relay, deploy-server, job-queue, nats-send, schedule-feature
- **Remove** `mclaude-docs-mcp/` (source + tests move to plugin repo; the on-disk `mclaude-docs-mcp/.docs-index.db*` WAL/SHM files are deleted as a byproduct of the directory removal — a fresh index is rebuilt at `.agent/.docs-index.db` on first MCP boot)
- **Remove** `mclaude-docs-dashboard/` (source + tests + UI move to plugin repo)
- **Remove** `docs/mclaude-docs-mcp/spec-docs-mcp.md` (plugin owns its spec)
- **Remove** `docs/ui/mclaude-docs-dashboard/` spec files if any (plugin owns them)
- **Remove** `.mcp.json` docs entry (plugin provides it)
- **Remove** workspaces entries in root `package.json` for docs-mcp + docs-dashboard
- **Remove** `scripts/master.sh` entirely (user invokes `sdd-master` directly); remove references in CLAUDE.md + any READMEs
- **Update** `CLAUDE.md` → trim workflow rules that the plugin now handles; keep mclaude-specific rules (CI, DNS, deploy); insert the plugin's `<!-- sdd:workflow-rules -->` managed block (populated by the setup skill from `spec-driven-dev/docs/conventions.md`)
- **Update** `.claude/settings.json` → remove the project's `PreToolUse` hook entry (plugin's `hooks/hooks.json` supplies the equivalent)
- **Update** `.gitignore` → remove `mclaude-docs-mcp/.docs-index.db*` lines; add `.agent/.docs-index.db*` (setup skill also does this, but mclaude can land it explicitly in the cleanup PR)
- **Create** `.agent/master-config.json` with mclaude source directories (the same globs currently in `scripts/master.sh`)
- **Create** `.agent/blocked-commands.json` with mclaude-specific guard rules added to defaults
- **Update** `feature-list.md` (if it references any moved components)

## Data Model

No new persistent state beyond existing patterns:
- `.agent/.docs-index.db` — SQLite FTS5 index (runtime, gitignored)
- `.agent/blocked-commands.json` — committed to repo
- `.agent/master-config.json` — committed to repo
- `.agent/bugs/*.md` — committed to repo
- `.agent/audits/*.md` — committed or gitignored per preference

## Error Handling

- No `docs/` directory → docs MCP logs warning, returns empty results (no crash)
- No `.git` → lineage scanning skipped; search and section retrieval still work
- No `.agent/blocked-commands.json` → hook silently allows all commands (no default blocks without setup)
- No `.agent/master-config.json` → `sdd-master` runs without `--disallowedTools` (master can edit anything)
- `docs-mcp` not on PATH → Claude Code logs MCP start failure; skills still work but without docs MCP tools
- `bun` not installed → setup skill fails with clear message ("bun required to compile docs-mcp")

## Security

No secrets, tokens, or auth. The docs MCP reads the local filesystem only. The hook reads project-local config only. The master entrypoint reads project-local config only.

## Impact

**New:**
- `spec-driven-dev` plugin repository (GitHub) with skills, agents, docs MCP, docs dashboard, hook, conventions doc, and entrypoint scripts

**Specs updated (committed with this ADR):**
- None in mclaude — all the specs that describe the moving components (`docs/mclaude-docs-mcp/spec-docs-mcp.md`, any dashboard specs under `docs/ui/mclaude-docs-dashboard/`) are deleted as part of the extraction, not updated.
- The plugin repo is seeded with its own `spec-docs-mcp.md` and `spec-docs-dashboard.md` at the extraction commit, derived from the mclaude specs.

**Modified in mclaude (deletion commit, after plugin repo is live):**
- `.agent/skills/` — 5 skills removed
- `.claude/skills/` — 5 symlinks removed
- `.agent/agents/` — 3 agents removed
- `.claude/agents/` — 3 symlinks removed (if present)
- `.agent/hooks/pre-tool-use.sh` — removed
- `mclaude-docs-mcp/` — removed (source + tests)
- `mclaude-docs-dashboard/` — removed (source + tests + UI)
- `docs/mclaude-docs-mcp/` — removed
- `docs/ui/mclaude-docs-dashboard/` — removed (if present)
- `.mcp.json` — `docs` entry removed (only entry today → file may be deleted)
- `.claude/settings.json` — `PreToolUse` hook entry removed
- `package.json` — workspaces list pruned
- `scripts/master.sh` — removed
- `CLAUDE.md` — workflow rules trimmed; `<!-- sdd:workflow-rules -->` managed block added via setup skill

**New in mclaude:**
- `.agent/blocked-commands.json` — mclaude-specific guard rules
- `.agent/master-config.json` — mclaude source directory list

## Scope

**v1:**
- Plugin repo (public GitHub) with all skills, agents, docs MCP, docs dashboard, hook, and entrypoint scripts
- Setup skill for one-time project configuration (compile binary, symlink `sdd-master` + `sdd-dashboard`, seed `.agent/` configs, insert workflow rules into project CLAUDE.md)
- Docs MCP accepts `--root` with walk-up fallback
- Docs dashboard launches via `sdd-dashboard` from any directory under a git repo
- Conventions doc explaining expected repo structure + workflow rules block to paste into project CLAUDE.md
- mclaude project cleaned up to use the plugin

**Deferred:**
- Publishing to a marketplace
- Plugin configuration (custom doc directories, custom ADR templates)
- Per-rule override env vars (v1 has only `SDD_DEBUG=1`)
- Auto-detection of source directories for master config
- Migrating the dashboard database format or adding new dashboard features during extraction (behavior-preserving move only)

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| Plugin scaffolding | ~120 | 40k | plugin.json, .mcp.json, root package.json (workspaces), conventions.md skeleton |
| Skill extraction + generalization | ~300 (edits) | 120k | Remove mclaude refs, generic patterns, 5 skills |
| Agent extraction + generalization | ~150 (edits) | 60k | Remove "mclaude" coupling, 3 agents |
| Docs MCP portability | ~100 | 60k | --root arg, walk-up fallback, bun build --compile, db path change, package rename |
| Docs dashboard portability | ~80 | 50k | Package rename, import path rewrites, default db path change, launcher script |
| Pre-tool-use hook | ~80 | 40k | Config-driven blocklist, ban/guard, SDD_DEBUG |
| sdd-master + sdd-dashboard entrypoints | ~100 | 40k | Config-driven --disallowedTools, model probing; dashboard launcher |
| Setup skill | ~180 | 80k | Compile, symlink master + dashboard, insert workflow block in CLAUDE.md, create configs |
| mclaude cleanup | ~80 (deletions) | 40k | Remove extracted files, docs folders, workspace entries, master.sh, update CLAUDE.md, create configs |

**Total estimated tokens:** ~530k
**Estimated wall-clock:** 1.0h of 5h budget (20%)
