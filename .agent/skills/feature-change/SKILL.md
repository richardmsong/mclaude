---
name: feature-change
description: Universal entry point for any change to the mclaude app — new features, bug fixes, refactors, config changes, anything. Reads design docs as the spec, updates the spec first (writing a new design doc for new features), then implements directly and validates.
user_invocable: true
---

# Feature Change

The entry point for **any change to any part of the mclaude app**. Features, bug fixes, refactors, config changes, UI tweaks, backend changes, helm values — everything goes through this loop.

## Usage

```
/feature-change <description of the change>
```

Examples:
- `/feature-change add project creation to control-plane`
- `/feature-change login returns wrong natsUrl to browser clients`
- `/feature-change refactor auth middleware to use context helper`
- `/feature-change remove server URL field from login screen`
- `/feature-change increase JWT expiry to 24h`
- `/feature-change helm chart missing resource limits on session-agent`

---

## Design docs are the spec

Design docs (`docs/plan-*.md`) are the canonical spec for mclaude. There is no separate spec layer.

| What's changing | Read these design docs |
|----------------|-----------|
| Control-plane (auth, provisioning, NATS subjects, HTTP endpoints) | `docs/plan-k8s-integration.md` |
| Session-agent (session lifecycle, Claude process, KV, failure modes) | `docs/plan-k8s-integration.md` |
| SPA / client (screens, stores, viewmodels, NATS pub/sub) | `docs/ui-spec.md`, `docs/plan-client-architecture.md` |
| Cross-cutting (new NATS subject, new KV bucket, new shared API) | `docs/plan-k8s-integration.md` + `docs/plan-client-architecture.md` |
| Feature-specific subsystem | `docs/plan-<feature>.md` |
| **iOS app** (`MClaude/`, SwiftUI) | `docs/design-claude-uplift.md` first: theme, fonts, drawer nav, and the Components section. It **overrides** `docs/ui-spec.md` where they conflict (e.g. follow-system light/dark, not enforced dark). Use `docs/ui-spec.md` only for cross-platform behavior the uplift doc doesn't cover. → **iOS/server lane** |
| **mclaude-server** (`mclaude-server/`, headless Swift runner on the Mac, launchd `com.mclaude.server`) | No dedicated design doc. Record app-facing behavior in `docs/design-claude-uplift.md`, or in the `docs/plan-*.md` for that feature. → **iOS/server lane** |

Also check `docs/feature-list.md` for feature IDs and platform support matrix.

**Which app?** The iOS app and the web SPA both have a transcript and a composer. Most recent work (Nov 2026) has been on the iOS app. Find the code that matches the reported symptom (for example, `.autocorrectionDisabled()` on the iOS composer). If the code doesn't settle it, ask the user.

---

## The Loop

```
1. Read the relevant design docs
2. Classify the change:
   A. Design doc correct, code wrong (bug)         → skip to step 4
   B. No design doc covers this (new feature)       → write the design doc (step 3)
   C. Design doc needs updating (change or gap)     → update design doc (step 3)
   D. Refactor (behavior unchanged)                 → skip to step 4
3. Update the design doc, commit spec separately
4. Implement directly, then review your own diff against the spec
5. Validate in the running app
```

**For iOS app (`MClaude/`) or `mclaude-server/` changes:** steps 3–5 follow the **iOS / mclaude-server lane** below. The main differences: the work happens in a git worktree on its own branch, validation is in the simulator against a :8399 test server, and a validated change is merged and shipped automatically.

This order is mandatory. The design doc always reflects intended behavior. If the code doesn't match the design doc, the code is wrong — fix the code. If the design doc doesn't describe the desired behavior, fix the design doc first, then fix the code.

---

## Step 1 — Read the relevant design docs

Read the full section of every design doc that covers the area being changed. Context matters — don't just search for a keyword.

---

## Step 2 — Classify the change

**A — Bug (design doc correct, code wrong):**
The design doc describes the desired behavior *in enough detail that someone reading only the spec could implement the fix*. The code simply diverges from what's written. Skip to step 4. Example: design doc says `natsUrl` is omitted when empty, code was returning the internal cluster URL.

**Litmus test for A:** Can you point to a specific sentence in the spec that the fix restores compliance with? If yes → A. If the fix requires adding behavior, configuration, setup steps, or environmental prerequisites that the spec doesn't mention → C, even if the spec describes the desired *outcome*.

"Spec says X should work" is not the same as "spec describes how X works." If the spec says `gh auth setup-git` registers credential helpers but doesn't describe the config prerequisites that make it succeed in headless containers, and the fix is to add those prerequisites — that's a spec gap (C), not a bug (A).

**B — New feature (no design doc):**
No design doc covers this feature. Write one in step 3 before any code. Do not write code without a spec.
- Research what exists first (related docs, code, patterns), so questions are informed.
- Resolve every real design decision with the user via AskUserQuestion: batch by theme, concrete options with trade-offs, recommended option first. Don't ask what the code can answer.
- Put it in the right home: a new `docs/plan-<slug>.md` for a standalone feature (Overview, Decisions, User Flow, Component Changes, Data Model, Error Handling, Scope), or a section in the existing doc for a small addition (see the table above).

**C — Behavior change or spec gap (design doc needs updating):**
Either the design doc describes old behavior you're changing, OR the design doc is silent on behavior that should be specified. In both cases: update the design doc first to describe the intended behavior, commit separately, then proceed to step 4. For non-trivial changes, ask the user to confirm before committing.

Spec gaps include but are not limited to: error handling, edge cases, loading states, environmental prerequisites, configuration setup, platform-specific behavior, failure modes, and any mechanism the code needs but the spec doesn't describe.

**Default to C.** A missing spec is NOT the same as "spec correct, code wrong" (A). If the spec doesn't say what should happen, classify as C and fill in the spec — don't skip to step 4 and leave the gap undocumented. When in doubt between A and C, choose C — the cost of an unnecessary spec update is near zero, the cost of an undocumented behavior is cumulative.

**D — Refactor (behavior unchanged):**
The design doc already describes the correct behavior. The code is restructured but externally identical. Skip to step 4. If the refactor reveals a spec gap (something undocumented), update the design doc first.

---

## Step 3 — Update the design doc

Design doc editing rules:

- Add to the right section with full payload/schema/behavior
- Remove entirely — no stale text
- Change in place — old text out, new text in
- Never leave design doc and code out of sync
- Describe only what will be built now — no future/intended behavior, no function names or file paths
- UI: wireframes match what renders; every interactive element gets a behavior bullet

Commit spec changes separately from code:
```bash
git add docs/
git commit -m "spec(<area>): <what changed and why>"
```

---

## iOS / mclaude-server lane

The iOS app and mclaude-server are built and run **locally**, not by CI. Steps 1–2 work the same as for the other components. Steps 3–5 change as follows.

**Work in a git worktree, commit there, merge to ship** (Richard, 2026-11-09: use worktrees and auto-ship when done; git resumed 2026-11-08 with baseline `07a96ca`, local only). Several sessions share `~/work/mclaude-v1`, so a change gets its own branch and worktree instead of editing the shared tree:

```bash
git -C ~/work/mclaude-v1 worktree add /tmp/fc/<slug> -b fc/<slug> main
```

- The session itself stays in `~/work/mclaude-v1` (projects are keyed by folder, so never start a session in a worktree). Read, edit and build in `/tmp/fc/<slug>` using absolute paths.
- The worktree has only *committed* code. If `git -C ~/work/mclaude-v1 status` shows another session's uncommitted edits to the files you need, base the change on `main` as committed and say so in your report. If you need their edits, message that session (SendMessage) and ask them to commit first.
- Commit on `fc/<slug>` once the change is validated: first the spec, then the code. Example messages: `spec(ios): …` and `feat(ios): …`. Use `git add <paths>`, never `git add -A`.
- **Don't push** unless Richard asks. Never rewrite history on `main`.
- Never stash, revert, reset or reformat anything in the shared tree that you didn't write.

**Step 3 (spec):** edit the design doc in the worktree. Commit it on the branch once the change is validated, just before the code commit.

**Step 4 (implement):** see Step 4 below. Build details for this lane:
- iOS: Xcode project `MClaude/MClaude.xcodeproj`, scheme `MClaude`, generated by xcodegen from `MClaude/project.yml`, deployment target iOS 17. Run `xcodegen generate` after adding files. Then build for the simulator with the command in `/deploy-ios` step 2: `-target MClaude -sdk iphonesimulator SUPPORTED_PLATFORMS="iphoneos iphonesimulator" CODE_SIGNING_ALLOWED=NO`. `project.yml` pins `iphoneos`, so `-destination 'generic/platform=iOS Simulator'` fails.
- iOS in a worktree: pass its own `SYMROOT`/`-derivedDataPath` under `/tmp/fc/<slug>` so you don't share build output with other sessions.
- Server: `swift build` and `swift test` in `/tmp/fc/<slug>/mclaude-server/`. The first build in a fresh worktree is a full build, so it takes a few minutes.
- Editor errors on single files (SourceKit checking a file in isolation) don't count; only the build result does.
- spec-evaluator is built for the k8s and SPA components and isn't used here. Your own review of the diff against the spec is the check.

**Step 5 (validate):** a successful build doesn't validate anything. Before reporting, smoke-test in the simulator (`/deploy-ios` step 2). If the change touches the server, point the simulator at a throwaway test server on port 8399 (`/deploy-server` step 2). **Never test against the live server** on :8377; you're probably running inside it. For visual changes, take screenshots in light **and** dark. Report what was verified: build only, simulator, or device.

**Ship automatically when done (user guidance, 2026-11-04, extended 2026-11-09):** once validation passes, ship without asking. Only skip shipping if validation failed or the user says not to.

1. **Merge** into `main` from the shared tree: `git -C ~/work/mclaude-v1 merge --no-ff fc/<slug>`. Git refuses the merge if another session has uncommitted edits to a file the branch touches. Don't stash or overwrite them: message that session (SendMessage) to commit or step aside, wait, and retry. Before archiving the app or restarting the server, give peers a heads-up (shared-tree rule).
2. **Deploy from the shared tree**, because the release scripts and the launch agent use `~/work/mclaude-v1`. The release build includes **every uncommitted edit** in that tree. So first run `git -C ~/work/mclaude-v1 status -- MClaude mclaude-server`. If another session has uncommitted edits there, ask the owner (ListAgents, then SendMessage) whether they're ready to ship before you build, and say in your report if you shipped with them.
   - iOS change: `/deploy-ios`. It bumps the build number, uploads to TestFlight, polls App Store Connect until the build is `VALID`, and appends to the release log. Keep polling until `VALID`; don't hand back mid-flight.
   - Server change (alone or with iOS): `/deploy-server` after any TestFlight upload. Rebuild the release binary in the shared tree, sign it, run the tests, and tag `server-<date>`. Restart it **last**, after your final message, using the delayed detached restart. Restarting it earlier kills the current session.
3. **Clean up:** `git worktree remove /tmp/fc/<slug> && git -C ~/work/mclaude-v1 branch -d fc/<slug>`.

Keep the final report short: what changed, what was verified, the TestFlight build number, and the commit(s).

---

## Step 4 — Implement and review

**Implement directly in this session.** The dev-harness agent was removed on 2026-11-04 at the user's request. Edit only the files the change needs.

**Subagents only for independent, parallel chunks.** Example: the server queue and the app's queue UI were built at the same time. Use `general-purpose` agents with a complete brief: paths, spec section, exact behavior, constraints (work in the given worktree; commit on its branch, never merge or ship), and the build command. Each agent reports its build result and diff. Small or sequential changes stay in this session.

**Review the whole diff against the spec** before validating: required behavior, edge cases, accessibility, and regressions in nearby behavior. Example: a long-press handler that swallowed the next normal tap. Fix what you find and rebuild until it's clean.

**k8s / SPA components** (`mclaude-control-plane/`, `mclaude-web/`, `mclaude-session-agent/`, `mclaude-cli/`, `charts/mclaude/`): for non-trivial changes, also run `/spec-evaluator <component>` as a fresh-context check. Fix code gaps yourself and re-run until CLEAN. CI still owns builds and deploys for these components (see CLAUDE.md).

### Handling backpressure

When you or spec-evaluator find a spec problem (ambiguity, missing detail, contradiction), handle it as backpressure:

1. **Classify**: factual error → fix directly. Missing detail with obvious answer → fill in. Design decision needed → ask the user via AskUserQuestion.
2. **Edit** the design doc following the step 3 editing rules.
3. **Commit** the spec change separately from code (in the iOS/server lane, on the worktree branch).
4. Continue implementing against the updated spec.

### Rules

- Never report a change complete while a known spec gap or bug remains. Fix it, or update the spec to reflect reality and say so.
- If a gap can't be implemented because of environment constraints, update the design doc to match reality.

---

## Step 5 — Validate (SPA changes; iOS/server: see lane above)

After CI deploys the preview, use the **Playwright MCP** to validate the golden path directly in the browser. Do not stop at "build passes" — drive the browser through the actual user flow.

```
Validation checklist for spa changes:
1. Navigate to the preview URL
2. Log in as dev@mclaude.local / dev
3. Assert the changed screen/behavior matches the spec
4. Assert the previous state (before the fix/feature) is gone
5. Test the specific acceptance criteria stated in the original request
```

**Tools**: `mcp__playwright__browser_navigate`, `mcp__playwright__browser_snapshot`,
`mcp__playwright__browser_fill_form`, `mcp__playwright__browser_click`,
`mcp__playwright__browser_wait_for`, `mcp__playwright__browser_evaluate`,
`mcp__playwright__browser_console_messages`

**Diagnostic tips** when something looks wrong:
- `browser_console_messages` — check for JS errors
- `browser_evaluate` — inspect live state (e.g. `() => window._captured`)
- Check NATS JetStream consumer state via port-forwarded NATS monitoring (`kubectl port-forward ... 8222:8222`)
- Check pod logs (`kubectl logs`) to confirm control-plane or session-agent received the request
- Check KV bucket contents via `curl localhost:8222/jsz?streams=1`

Do not report the task complete until Playwright confirms the acceptance criteria are met in the running preview.

---

## Reference

- `docs/plan-k8s-integration.md` — backend architecture, NATS subjects, KV
- `docs/plan-client-architecture.md` — client architecture, stores, viewmodels
- `docs/ui-spec.md` — UI wireframes and behavior
- `docs/plan-*.md` — feature-specific design docs (each is a spec)
- `docs/feature-list.md` — feature IDs
- `docs/design-claude-uplift.md` — iOS app visual/interaction spec (overrides ui-spec.md for iOS)
- `/deploy-ios` (TestFlight), `/deploy-server` (local server + :8399 test server)
- Component roots: `mclaude-control-plane/`, `mclaude-web/`, `mclaude-session-agent/`, `mclaude-cli/`, `charts/mclaude/`, `MClaude/`, `mclaude-server/`
