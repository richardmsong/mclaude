## Audit: 2026-04-14T00:00:00Z
**Document:** docs/plan-scratch-to-git.md

---

### Round 1 — Initial Evaluation

#### Cross-reference files examined
- `docs/plan-k8s-integration.md` — session lifecycle, PVC layout, MCProject CRD, NATS subjects
- `docs/plan-github-oauth.md` — OAuth connection flow, token storage
- `mclaude-session-agent/entrypoint.sh` — current git init logic (bare repo for ALL projects)
- `mclaude-control-plane/projects.go` — current project handlers (create only, no update)
- `mclaude-control-plane/mcproject_types.go` — MCProject CRD spec
- `mclaude-control-plane/reconciler.go` — reconciler deployment logic
- `mclaude-session-agent/agent.go` — session create/delete handlers (universal worktree model)
- `mclaude-session-agent/worktree_git_test.go` — tests confirm universal bare repo model

---

### Factual Inconsistencies (fixable)

#### F1: Current model is NOT "bare repo for git, no git for scratch" — it is "bare repo for ALL"
**Status: BLOCKING**

The design doc's foundational assumption is wrong. It states:

> Users can upgrade a scratch project (no git, files in `/data/`) to a git project

But the current codebase uses a **universal bare repo** model:
- `entrypoint.sh` lines 34-60: ALL projects get a bare repo at `/data/repo/`. Scratch projects (no `GIT_URL`) get `git init --bare /data/repo` with an empty init commit.
- `agent.go` line 305: session create ALWAYS computes `repoPath := filepath.Join(dataDir, "repo")` and creates worktrees.
- `worktree_git_test.go` line 160: test comment explicitly says "there is no scratch path. Every project has a bare repo."
- `plan-k8s-integration.md` line 442: "Every project has a bare repo at `/data/repo` — the entrypoint initializes one via `git init --bare` for scratch projects"

The doc proposes changing from bare→regular repo layout (section "Change from current model"), which is valid. But the premise that scratch projects currently have "no git" is factually wrong — they DO have git (a bare repo + worktrees), they just have no remote.

**Fix:** Update the Overview and decisions to accurately describe what "scratch" means today (local bare repo, no remote, worktrees) vs. what the doc proposes.

#### F2: The `projects.update` handler does not exist
**Status: BLOCKING**

The doc says the `/git-init` skill "Notifies control-plane: `projects.update { git_url: 'local' }`" (line 38). But `projects.go` only has a `projects.create` subscriber (`mclaude.*.api.projects.create`). There is no `projects.update` handler. The doc should explicitly list this as a new handler to implement, not reference it as if it exists.

**Fix:** In "Component Changes > Control-plane", make it explicit this is a NEW NATS subscription to be created.

#### F3: NATS subject pattern has `{location}` token that doesn't match existing conventions
**Status: MINOR**

Line 86: `mclaude.{userId}.{location}.{projectId}.project.updated`

Existing NATS subjects in `plan-k8s-integration.md` use:
- `mclaude.{userId}.{projectId}.api.sessions.*` (no location token for K8s)
- `mclaude.{userId}.laptop.{hostname}.{projectId}.api.>` (laptop mode only)

The `{location}` token is only used in laptop mode. For K8s (where this feature runs), the pattern should be `mclaude.{userId}.{projectId}.project.updated` or follow the existing event pattern.

**Fix:** Remove `{location}` from the subject pattern.

#### F4: MCProject CRD `spec.gitURL` semantics mismatch
**Status: MINOR**

Line 88 says: "`spec.gitURL` field already exists, no schema change needed."

This is true — `mcproject_types.go` has `GitURL string` in the spec. However, the doc proposes storing `"local"` in `git_url` for local-only git projects. Currently `GitURL` is either empty (scratch) or a URL. Storing the sentinel value `"local"` in a field documented as "optional git remote" is a semantic change that affects:
- The reconciler's `GIT_URL` env var logic (reconciler.go line 332-333): it checks `if gitURL != ""` to decide whether to set the env var. `"local"` would now be set as `GIT_URL=local`, which the entrypoint would try to `git clone "local"`.

**Fix:** Document that the entrypoint must be updated to handle `GIT_URL=local` (skip clone, treat as local-only), or that the reconciler should NOT pass `GIT_URL` for the `"local"` value.

#### F5: Entrypoint initial commit uses plumbing commands, doc shows porcelain
**Status: MINOR (in proposed pseudocode only)**

The doc's proposed entrypoint pseudocode (lines 94-103) doesn't show the actual implementation details. The current entrypoint (line 49-54) uses plumbing commands (`hash-object`, `commit-tree`, `update-ref`) because a bare repo has no working tree. The doc's proposed regular-repo flow uses `git init /data/` which DOES have a working tree, so porcelain commands work. This is consistent within the proposal but should be called out as part of the migration.

No fix needed — just noting for completeness.

#### F6: Migration script has a bug with dotfiles
**Status: MINOR**

Line 209: `mv /data/checkout/{.,}* /data/ 2>/dev/null`

The glob `{.,}*` expands to `.*` and `*`. The `.*` glob includes `.` and `..`, which would attempt `mv /data/checkout/. /data/` and `mv /data/checkout/.. /data/`. While `mv` typically refuses to move `.` and `..`, this is fragile. Better to use:
```bash
shopt -s dotglob && mv /data/checkout/* /data/ && shopt -u dotglob
```

**Fix:** Update the migration script.

---

### Design Decisions (require user input)

#### D1: Breaking the universal worktree model — is this the right direction?
**Current model:** ALL projects (scratch + git) have a bare repo at `/data/repo/` and use worktrees. This was a deliberate design choice — `plan-k8s-integration.md` line 442 says "This means the session agent's worktree machinery works uniformly for all projects."

**Proposed model:** Scratch projects have NO git at `/data/`, git projects have a regular repo at `/data/`. This creates two code paths in the session-agent (worktree mode vs. direct `/data/` mode).

DECISION NEEDED: Repo layout model
Options:
A. **Keep universal worktrees (current)** — scratch stays as bare-repo+worktrees. "Enable Git" just adds a remote and changes `git_url` from `''` to URL. No layout migration needed. The entrypoint already handles everything. Simpler implementation, no two-code-path problem.
B. **Switch to regular repo (proposed in doc)** — all projects migrate from `/data/repo/` bare to `/data/` regular. Scratch projects lose their bare repo and become plain files. More natural for user-initiated `git init`. Requires migration of ALL existing projects.
C. **Hybrid** — keep bare repo for existing projects, use regular repo only for NEW scratch projects that get converted. Two layouts coexist permanently. Most complex to maintain.

#### D2: Conversion session cwd — what directory does the `/git-init` skill's session run in?

The doc says (line 120): "the session running the git-init skill operates on `/data/` directly (it's a scratch session at the point of conversion, no worktree)."

But under the current universal model, this session WOULD have a worktree at `/data/worktrees/{branch}/`. The skill would need to operate on `/data/` (the bare repo parent or the shared data root), not its own worktree.

If option A from D1 is chosen, the conversion skill needs special handling to operate outside its worktree.

DECISION NEEDED: Conversion session working directory
Options:
A. Skill overrides cwd to `/data/` — session-agent creates the session with a special flag (e.g., `{ skill: "/git-init", cwd: "/data/" }`) that bypasses worktree creation.
B. Conversion runs as a non-session operation — control-plane or session-agent handles it directly without spawning a Claude session.

#### D3: Pod restart required after `spec.gitURL` change

Line 88: "Reconciler uses it to set `GIT_URL` env var on session-agent pods."

The reconciler sets env vars at Deployment spec time. Changing `spec.gitURL` triggers a Deployment spec change which causes a pod restart (rolling update). This means:
- All active sessions in the project are interrupted
- The pod restarts, entrypoint re-runs with new `GIT_URL`
- Sessions resume via `--resume`

The doc doesn't mention this disruption. Is this acceptable?

DECISION NEEDED: Pod restart on git conversion
Options:
A. **Accept the restart** — conversion is a one-time event, sessions resume. Document the disruption.
B. **Defer CRD update** — don't update `spec.gitURL` immediately. Let the entrypoint detect `/data/.git/` on next natural restart. The env var becomes informational only.
C. **Session-agent watches for project updates** — add a NATS subscription for project update events so the session-agent can adapt without restart.

#### D4: `git_url = ''` vs `git_url = 'local'` — is the sentinel value the right approach?

The doc proposes three states in one column: `''` (scratch), `'local'` (local git), URL (remote git). The sentinel `'local'` has implications:
- The reconciler currently treats any non-empty `gitURL` as a clone URL (passes it as `GIT_URL` env var)
- The entrypoint currently treats any non-empty `GIT_URL` as a clone target
- Both need special-case logic for `'local'`

DECISION NEEDED: State encoding for local-only git
Options:
A. **Sentinel `'local'` in `git_url`** (as proposed) — simple schema, one column. Requires reconciler and entrypoint to special-case `'local'`.
B. **Separate boolean `git_enabled`** — add a new column. `git_enabled=true, git_url=''` means local-only. `git_enabled=true, git_url='https://...'` means remote. No sentinel values, clearer semantics, but requires schema migration.
C. **Enum column `git_mode`** — values: `none`, `local`, `remote`. `git_url` stays URL-only. Two columns but clean semantics.

---

### Fixes Applied (Round 1)

1. **F1** — Added callout block in Overview noting the current universal bare-repo model and that this doc proposes a layout migration, not just "adding git."
2. **F2** — Changed `projects.update` handler label from implicit to explicit: `**NEW: projects.update handler**` with NATS subject.
3. **F3** — Removed `{location}` token from NATS subject: `mclaude.{userId}.{projectId}.project.updated`.
4. **F4** — Updated entrypoint pseudocode to explicitly handle `GIT_URL=local` (skip clone). Added note about reconciler passing sentinel value.
5. **F6** — Replaced fragile `{.,}*` glob with `shopt -s dotglob` in migration script.

### Not Fixed (requires design decisions)

- **F1 (full)** — The Overview still describes "scratch = no git" which contradicts reality. A complete rewrite depends on D1 (whether to keep or break the universal worktree model).
- **D1, D2, D3, D4** — Design decisions returned to user for resolution.

---

### Summary

| # | Type | Severity | Status |
|---|------|----------|--------|
| F1 | Factual | BLOCKING | Current model is universal bare repo, not "no git for scratch" |
| F2 | Factual | BLOCKING | `projects.update` handler doesn't exist, needs to be new |
| F3 | Factual | MINOR | NATS subject has spurious `{location}` token |
| F4 | Factual | MINOR | `GIT_URL=local` would break entrypoint clone logic |
| F5 | Factual | INFO | Entrypoint pseudocode OK for proposed model |
| F6 | Factual | MINOR | Migration script dotfile glob is fragile |
| D1 | Design | BLOCKING | Universal worktrees vs. regular repo — fundamental architecture choice |
| D2 | Design | BLOCKING | Conversion session cwd when current model uses worktrees |
| D3 | Design | MODERATE | Pod restart disruption on git_url change not documented |
| D4 | Design | MODERATE | Sentinel value `'local'` vs. separate column for git state |

---

### Round 2 — Design Decision Resolution Audit

#### Decisions resolved in updated doc

| Decision | Resolution | Consistent? |
|----------|-----------|-------------|
| D1: Repo layout | **Option B — regular repo at `/data/`**. Scratch = no `.git/`, git = regular repo at `/data/`, worktrees at `/data/worktrees/`. Migration from bare repo documented. | YES — Overview callout, Decisions table, entrypoint pseudocode, PVC layout, and migration script all align. |
| D2: Conversion session cwd | **Scratch sessions already use `/data/`**. Under new model, scratch has no `.git/`, so sessions get `cwd=/data/`. Conversion session is just a normal scratch session that runs `git init`. No special flag needed. | YES — follows naturally from D1. Decision table row "Conversion session cwd" is consistent with session-create pseudocode (no `.git/` → `cwd = /data/`). |
| D3: Pod restart | **No restart — filesystem-first entrypoint + ConfigMap**. CRD `gitURL` only for initial clone. Entrypoint reads `/etc/mclaude/config/GIT_URL` from ConfigMap mount. ConfigMap updated by control-plane, auto-synced by kubelet. Conversion changes disk only, no CRD update, no restart. | YES — clean solution. Avoids D3's disruption problem entirely. |
| D4: State encoding | **Option B — `has_git` boolean + `git_url` column**. No sentinel values. CHECK constraint enforces `has_git OR git_url = ''`. Three states: scratch `(false,'')`, local git `(true,'')`, remote `(true,'url')`. | YES — schema, state transition diagram, KV entry, and API handler all use this model consistently. |

**All four design decisions are resolved and internally consistent.**

#### Remaining issues

##### F7: `{location}` segment in `project.updated` event subject — correct but needs sourcing
**Status: MINOR**

Line 95: `mclaude.{userId}.{location}.{projectId}.project.updated`

Per the latest `plan-k8s-integration.md` (commit `b5b5f6d`), all session-scoped subjects now include `{location}`. The event subject correctly uses it. However:

1. The **control-plane** publishes this event, not a session-agent. The control-plane must resolve the project's location to construct the subject. This requires either a location field in the projects table/KV or a lookup to the `mclaude-locations` KV bucket. The doc doesn't specify how the control-plane knows the location.
2. The **API subject** on line 85 (`mclaude.{userId}.api.projects.update`) correctly omits `{location}`, matching existing project API patterns (`projects.create`, `projects.delete`, `projects.list` are all global, not location-scoped).

**Fix:** Add a note specifying how the control-plane resolves the project's location when publishing the event. Options: (a) store location in the projects table, (b) look up from MCProject CR's cluster context, (c) the session-agent that triggers the update includes location in the request payload.

##### F8: `project-{id}-config` ConfigMap is a new resource type not in plan-k8s-integration.md
**Status: MINOR**

The k8s integration plan defines one ConfigMap per user namespace: `user-config`. The scratch-to-git doc introduces a per-project ConfigMap (`project-{projectId}-config`) that doesn't exist in plan-k8s-integration.md. This requires:

1. Reconciler update: `reconcileDeployment` must create and own the project ConfigMap alongside PVCs and Deployment.
2. Deployment template update: mount the project ConfigMap as a volume at `/etc/mclaude/config`.
3. The doc's Helm section (lines 152-168) describes the mount correctly, but should note this is a new resource the reconciler must create (not just a Helm template).

**Fix:** Either (a) update plan-k8s-integration.md to include `project-{id}-config` in the reconcile loop, or (b) add an explicit callout in the scratch-to-git doc that the reconciler needs a new step (between current steps 8 and 9): "Ensure project ConfigMap `project-{projectId}-config` in user namespace."

##### F9: `projects.update` NATS subject not registered in plan-k8s-integration.md API table
**Status: MINOR**

Line 85 introduces `mclaude.{userId}.api.projects.update` as a new NATS subscription. The k8s integration plan's API section (lines 148-156) lists `projects.create`, `projects.delete`, `projects.list` but not `projects.update`. Similarly, `project.updated` (the event) is a new subject type not in the Events section.

**Fix:** When implementing, update plan-k8s-integration.md's API and Events sections to include:
- API: `mclaude.{userId}.api.projects.update → control-plane`
- Events: `mclaude.{userId}.{location}.{projectId}.project.updated → project state change events`

##### F10: Entrypoint transition from env var to ConfigMap mount
**Status: INFO**

The current entrypoint reads `$GIT_URL` as an environment variable (set by the reconciler in the Deployment spec, line 332-333 of reconciler.go). The proposed entrypoint reads from `/etc/mclaude/config/GIT_URL` (ConfigMap file mount). This is a breaking change to the entrypoint contract. The doc's entrypoint pseudocode (lines 117-129) correctly shows the new approach. On implementation, the entrypoint should support BOTH sources during migration (check ConfigMap mount first, fall back to env var).

No fix needed in the doc — just an implementation note.

---

### Round 2 Summary

| # | Type | Severity | Status |
|---|------|----------|--------|
| D1 | Design | RESOLVED | Regular repo at `/data/` — consistent throughout doc |
| D2 | Design | RESOLVED | Scratch sessions use `/data/` — no special cwd needed |
| D3 | Design | RESOLVED | Filesystem-first entrypoint + ConfigMap — no pod restart |
| D4 | Design | RESOLVED | `has_git` boolean + `git_url` column — clean state model |
| F7 | Factual | MINOR | Control-plane needs project location to publish event subject |
| F8 | Factual | MINOR | `project-{id}-config` ConfigMap is new, needs reconciler step |
| F9 | Factual | MINOR | `projects.update` and `project.updated` not in k8s plan's subject tables |
| F10 | Factual | INFO | Entrypoint env-var-to-ConfigMap migration needs backward compat |

**Verdict: CLEAN (no blocking gaps).** All four design decisions from Round 1 are resolved and internally consistent. Remaining issues are minor cross-doc sync items (F7-F9) and an implementation note (F10) — none block design approval.
