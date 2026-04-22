## Audit: 2026-04-22T00:00:00Z

**Document:** docs/adr-0026-portable-dev-workflow-plugin.md

### Round 1

**Gaps found: 6**

1. **`sdd-dashboard` env-var handoff to `boot.ts` is unspecified** — The doc says `sdd-dashboard` "walks up from $PWD to find .git, then execs `bun run src/server.ts` inside `${CLAUDE_PLUGIN_ROOT}/docs-dashboard/`, passing the repo root via an env var." The env var name is never stated. `boot.ts` currently calls `process.cwd()` to start its own walk-up — it does not accept an env var. If `sdd-dashboard` changes `cwd` to `${CLAUDE_PLUGIN_ROOT}/docs-dashboard/` before launching `bun run src/server.ts`, the walk-up from the new cwd will find the plugin's own `.git`, not the project's. A developer must know: (a) which env var to set in `sdd-dashboard`, and (b) how `boot.ts` reads it. Neither is specified.
   - **Doc**: "walks up from $PWD until it finds a .git directory, then execs `bun run src/server.ts` inside `${CLAUDE_PLUGIN_ROOT}/docs-dashboard/`, passing the repo root via an env var"
   - **Code**: `mclaude-docs-dashboard/src/boot.ts:49` — `const cwd = process.cwd(); const repoRoot = findRepoRoot(cwd);` — no env-var reading path exists today.

2. **`pre-tool-use.sh` stdin parsing contract is unspecified** — The hook must extract the Bash command from "stdin JSON," but the exact JSON structure is not defined. The current `mclaude` hook uses `tool_input.command`. Claude Code's actual hook stdin format (the full JSON envelope, whether it's `tool_input` or `input` or something else) is not referenced or quoted. A developer writing the generic `pre-tool-use.sh` must know the exact field path to extract the Bash command.
   - **Doc**: "Extracts the Bash command from stdin JSON"
   - **Code**: `mclaude/.agent/hooks/pre-tool-use.sh` uses `d.get('tool_input', {}).get('command', '')` — but this format is not specified anywhere in the ADR so a developer could not derive it from the doc alone.

3. **`hooks/hooks.json` format is unspecified** — The doc says the plugin declares the hook in `hooks/hooks.json` using "standard plugin hook format," but no schema or example of that format is provided. It is not defined in any referenced ADR. A developer cannot write the file without knowing what fields it accepts (event name, matcher, command path).
   - **Doc**: "Plugin declares the hook in `hooks/hooks.json` (standard plugin hook format). ... PreToolUse on Bash → points at pre-tool-use.sh"
   - **Code**: no `hooks.json` file exists anywhere in the codebase to reference.

4. **`bun build --compile` invocation for `docs-mcp` binary is underspecified** — The setup skill must compile `docs-mcp` into a single binary at `${CLAUDE_PLUGIN_ROOT}/bin/docs-mcp`. The exact `bun build --compile` command is not given: which entrypoint file (`docs-mcp/src/index.ts`?), which `--outfile` path, and whether any `--external` or `--target` flags are needed. The existing `mclaude-docs-mcp/package.json` has no `build` or `compile` script; the ADR says to "add" one but does not specify its content. A developer writing the setup skill or the `package.json` `compile` script cannot produce it from the doc alone.
   - **Doc**: "Compile `docs-mcp` binary via `bun build --compile` into `${CLAUDE_PLUGIN_ROOT}/bin/docs-mcp`" and "add `bun build --compile` script" to `package.json`
   - **Code**: `mclaude-docs-mcp/package.json` — no `compile` script or `bin` field exists today; the doc says to add both but gives no value.

5. **`conventions.md` workflow-rules block content is not defined** — The setup skill reads the block "verbatim from the plugin's `docs/conventions.md#workflow-rules` section." The ADR describes that the block is "derived from mclaude's current CLAUDE.md by stripping mclaude-specific rules and keeping the universal ones," then lists six rule categories, but provides no exact text. The setup skill must write a literal string into each project's CLAUDE.md. Without the verbatim content of the `workflow-rules` section, the skill cannot be implemented and, critically, the conventions.md file itself cannot be authored.
   - **Doc**: "Block content is read verbatim from the plugin's `docs/conventions.md#workflow-rules` section" and "Content derived from mclaude's current CLAUDE.md by stripping mclaude-specific rules"
   - **Code**: `conventions.md` does not exist yet — the doc describes what it should contain but gives no literal text.

6. **DB path migration gap: `boot.ts` default path changes but the transition for existing mclaude DB is not addressed** — The doc says the db path changes from `<repo-root>/mclaude-docs-mcp/.docs-index.db` to `<repo-root>/.agent/.docs-index.db`. `boot.ts` currently defaults to `join(repoRoot, "mclaude-docs-mcp", ".docs-index.db")`. The plugin version of `boot.ts` will default to `join(repoRoot, ".agent", ".docs-index.db")`. In the mclaude cleanup phase, the existing DB at the old path is abandoned and a new one is built at the new path. The doc does not specify whether the old DB file is deleted, whether `gitignore` patterns for the old location are cleaned up, or whether there is any risk of two DB files coexisting. The mclaude cleanup section mentions updating `.gitignore` to remove the old pattern and add the new one, but does not say what happens to the existing on-disk DB file at `mclaude-docs-mcp/.docs-index.db` (currently gitignored via `*.db` in the root `.gitignore`).
   - **Doc**: "`.docs-index.db` path: changes from `<repo-root>/mclaude-docs-mcp/.docs-index.db` to `<repo-root>/.agent/.docs-index.db`" and mclaude cleanup section updates `.gitignore` but does not mention deleting or moving the old DB.
   - **Code**: `mclaude-docs-dashboard/src/boot.ts:58` — `dbPath ?? join(repoRoot, "mclaude-docs-mcp", ".docs-index.db")` — old path is still the live default.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|------------|------|
| 1 | `sdd-dashboard` env-var handoff unspecified | Added explicit launcher behavior: walks up from `$PWD`, exports `SDD_REPO_ROOT`, execs `bun run <abs>/src/server.ts` without `cd`. `boot.ts` reads `process.env.SDD_REPO_ROOT` before falling back to cwd walk-up. | factual |
| 2 | `pre-tool-use.sh` stdin JSON field path unspecified | Added the full stdin envelope shape with `tool_input.command` as the documented field path. | factual |
| 3 | `hooks/hooks.json` format unspecified | Added a full `hooks/hooks.json` example with the plugin hook format (`PreToolUse` → `matcher: "Bash"` → `type: "command"`, `command: "${CLAUDE_PLUGIN_ROOT}/hooks/pre-tool-use.sh"`). | factual |
| 4 | `bun build --compile` invocation underspecified | Added exact `package.json` `"bin"` + `"compile"` script contents and the setup skill's invocation (`cd "${CLAUDE_PLUGIN_ROOT}/docs-mcp" && bun run compile`). | factual |
| 5 | `conventions.md` workflow-rules block content undefined | Embedded verbatim `conventions.md` content in the ADR (three H2 sections; `## workflow-rules` section enumerates the 5 universal rules that setup writes into project CLAUDE.md). | factual |
| 6 | Old DB file disposition unspecified | Clarified that `mclaude-docs-mcp/.docs-index.db*` WAL/SHM files are deleted as a byproduct of the `mclaude-docs-mcp/` directory removal; a fresh index is rebuilt at `.agent/.docs-index.db` on next boot. | factual |

---

## Run: 2026-04-22T16:00:00Z

**Gaps found: 1**

1. **`compile` npm script uses `${CLAUDE_PLUGIN_ROOT}` but the variable's availability in the script shell is unspecified** — The ADR adds this `compile` entry to `docs-mcp/package.json`:

   ```json
   "compile": "bun build --compile --target=bun ./src/index.ts --outfile ${CLAUDE_PLUGIN_ROOT}/bin/docs-mcp"
   ```

   And says the setup skill runs it as: `cd "${CLAUDE_PLUGIN_ROOT}/docs-mcp" && bun run compile`.

   When `bun run compile` executes the package.json script, the shell that runs it is a new subprocess. `${CLAUDE_PLUGIN_ROOT}` will expand to an empty string (and the `--outfile` path becomes `/bin/docs-mcp`, which is a root-owned system path) unless the variable was exported into the environment before the invocation. The ADR does not specify that the setup skill must `export CLAUDE_PLUGIN_ROOT` before calling `bun run compile`, nor does it offer an alternative approach (e.g., passing `--outfile` directly on the command line instead of reading it from an env var inside the script).

   A developer writing the setup skill cannot determine whether to `export` the variable or use a different mechanism. If `CLAUDE_PLUGIN_ROOT` is only set as a local shell variable in the setup skill (e.g., `CLAUDE_PLUGIN_ROOT=$(claude plugin root spec-driven-dev)` without `export`), the compile will silently produce a binary at the wrong path.

   - **Doc**: `"compile": "bun build --compile --target=bun ./src/index.ts --outfile ${CLAUDE_PLUGIN_ROOT}/bin/docs-mcp"` and "The setup skill runs this script from the `docs-mcp/` working directory: `cd "${CLAUDE_PLUGIN_ROOT}/docs-mcp" && bun run compile`."
   - **Code**: `mclaude-docs-mcp/package.json` — no `compile` script exists today; the doc adds one that depends on env-var availability that isn't guaranteed.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|------------|------|
| 1 | `${CLAUDE_PLUGIN_ROOT}` availability in `bun run compile` subshell | Changed the `compile` script to use a relative outfile path (`../bin/docs-mcp`) that resolves correctly when the setup skill `cd`s into `docs-mcp/` first. No env var expansion needed inside the script. Also added an explicit `mkdir -p "${CLAUDE_PLUGIN_ROOT}/bin/"` step for the setup skill. | factual |

## Run: 2026-04-22T17:00:00Z

**Gaps found: 3**

1. **`sdd-dashboard` bin/ file-tree comment contradicts the entrypoint section** — The `bin/` directory tree at line 118 annotates `sdd-dashboard` as: `cd "${CLAUDE_PLUGIN_ROOT}/docs-dashboard" && bun run src/server.ts`. The dedicated `sdd-dashboard entrypoint` section at lines 362–366 explicitly states the opposite: it execs `bun run "${CLAUDE_PLUGIN_ROOT}/docs-dashboard/src/server.ts"` **without** `cd`-ing into the plugin dir. These two descriptions are mutually exclusive: the first changes cwd into the plugin directory (which would break Bun's cwd-based walk-up by landing in the plugin repo instead of the project), while the second preserves the user's cwd. A developer cannot know which behavior to implement.
   - **Doc**: Line 118: `# launcher: cd "${CLAUDE_PLUGIN_ROOT}/docs-dashboard" && bun run src/server.ts`; Lines 362–366: `Exec bun run "${CLAUDE_PLUGIN_ROOT}/docs-dashboard/src/server.ts" **without** cd-ing into the plugin dir`.
   - **Code**: `mclaude-docs-dashboard/src/boot.ts:49` — `const cwd = process.cwd(); const repoRoot = findRepoRoot(cwd);` — cwd must be inside the project for walk-up to work.

2. **Agent file format "single-file convention for plugins" is undefined** — The directory tree lists agent entries as bare `.md` files (e.g., `agents/design-evaluator.md`) and annotates them "single-file convention for plugins." The existing codebase uses a directory layout (`design-evaluator/AGENT.md`). The doc never defines what format Claude Code accepts for plugin agents, whether `agents/<name>.md` is a supported format, or how it differs from the existing `agents/<name>/AGENT.md` convention. A developer authoring the `agents/` directory in the plugin repo cannot determine which format to use.
   - **Doc**: Line 127: `│   ├── design-evaluator.md            # single-file convention for plugins; already generic`
   - **Code**: `/Users/rsong/work/mclaude/.agent/agents/design-evaluator/AGENT.md` — directory-per-agent format is what the codebase currently uses and what `.claude/agents/` symlinks point to.

3. **Setup idempotency check for compiled binary is unspecified** — The Decisions table states setup "checks each item independently (binary compiled?...)" but never defines what that check is. No test command, file-existence check, or version stamp is described. A developer writing the setup skill cannot implement "is the binary already compiled?" without knowing the mechanism (e.g., `[ -x "${CLAUDE_PLUGIN_ROOT}/bin/docs-mcp" ]`, or running `docs-mcp --version`, or checking a stamp file). The absence of this definition leaves the idempotency of the most expensive step (the compile) undefined.
   - **Doc**: "Checks each item independently (binary compiled?...)" — Decisions table, line 43.
   - **Code**: No precedent for this check exists in the codebase; `scripts/master.sh` uses `[ -r "$CACHE_FILE" ]` for the model probe cache, but the doc does not define an analogous mechanism for the binary.

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|------------|------|
| 1 | `sdd-dashboard` tree comment contradicted dedicated section | Updated the `bin/` tree comment for `sdd-dashboard` to describe the launcher as "walks up from $PWD, sets SDD_REPO_ROOT, execs bun run …/docs-dashboard/src/server.ts WITHOUT cd'ing" — matches the dedicated "sdd-dashboard entrypoint" section exactly. | factual |
| 2 | Agent file format "single-file" was wrong | Switched the `agents/` tree in the ADR to the documented directory-per-agent layout (`agents/<name>/AGENT.md`) — matches the Claude Code plugins doc. | factual |
| 3 | Setup idempotency check undefined | Enumerated concrete shell checks for every idempotency condition: `command -v bun`, `[ -x "${CLAUDE_PLUGIN_ROOT}/bin/docs-mcp" ]` for the binary, `[ -L "$HOME/.local/bin/sdd-master" ]` for symlinks, `[ -d .agent ]`, `[ -f .agent/blocked-commands.json ]`, grep for the `<!-- sdd:workflow-rules BEGIN -->` marker, `grep -qF '.agent/.docs-index.db' .gitignore`. | factual |
