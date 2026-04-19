# Scratch-to-Git Conversion

**Status**: accepted
**Status history**:
- 2026-04-14: accepted


## Overview

> **Current model note:** Today ALL projects (including scratch) get a bare repo at `/data/repo/` and use worktrees — see `entrypoint.sh` and `adr-2026-04-10-k8s-integration.md`. "Scratch" currently means "bare repo, no remote." This doc proposes changing to a regular-repo layout at `/data/` and introducing a true no-git scratch mode. This is a layout migration, not just adding git to projects that lack it.

Users can upgrade a scratch project (no git, files in `/data/`) to a git project (version-controlled, worktree isolation per session). Conversion happens in a dedicated session via a skill, where Claude walks the user through gitignore setup, initial commit, and optional remote connection. Once converted, a project cannot revert to scratch.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Trigger | User clicks "Enable Git" in project settings | Explicit user action, not auto-suggested. User knows when their project needs version control. |
| Conversion mechanism | Dedicated session with auto-triggered skill | Conversational — user controls what gets committed, gitignore patterns, remote setup. Open-ended and customizable. |
| Active sessions during conversion | Convert live, active sessions stay on `/data/` | `git init` in `/data/` doesn't move files. Active sessions continue unaware. New sessions get worktrees. |
| Conversion session cwd | Scratch sessions already use `/data/` | Under the true-scratch model, scratch projects have no `.git/` so sessions get `cwd=/data/`. No special flag needed — the conversion session is just a normal scratch session that runs `git init`. |
| Existing repo conflicts | Claude handles conversationally | When connecting a non-empty remote, Claude walks the user through conflict resolution in the conversion session. |
| Reversibility | One-way — no going back to scratch | Once git-enabled, stays git. Can disconnect remote (→ local-only git) but can't disable git. |
| Repo layout | Regular repo at `/data/` for all git projects | Unified model. `git clone <url> /data/` for remote projects, `git init /data/` for conversion. No bare repos. Worktrees branch off `/data/`. |
| `/data/` role post-conversion | Shared main branch, no session uses directly | `/data/` is the main branch checkout used for git operations. All sessions get `/data/worktrees/{branch}/`. |
| Who converts | Claude in-session via `/git-init` skill | Natural conversational flow. Skill notifies control-plane when done. |
| State model | `has_git` boolean + `git_url` column | `has_git=false, git_url=''` = scratch. `has_git=true, git_url=''` = local git. `has_git=true, git_url='url'` = remote. CHECK constraint prevents `has_git=false` with non-empty `git_url`. |
| CRD + ConfigMap | CRD keeps `gitURL` for provisioning, ConfigMap for runtime config | CRD triggers initial clone on first pod start. ConfigMap mounted as file for live updates without pod restart. Conversion doesn't update CRD — entrypoint detects `/data/.git/` from filesystem. |
| Pod restart on conversion | No restart needed — filesystem-first entrypoint | Entrypoint detects `/data/.git/` and checks `git remote` on disk. CRD `gitURL` only matters for initial clone. Conversion happens on disk in-session, no CRD change, no restart. |

## User Flow

### Converting to git (project settings)

1. User opens project settings in the SPA
2. Under "Version Control", sees: **None** — \[Enable Git\]
3. User clicks **Enable Git**
4. SPA creates a new session with the `/git-init` skill auto-triggered
5. Session appears in the session list, user is navigated to it
6. In the session, Claude:
   - Scans `/data/` for files
   - Creates `.gitignore` (asks user about patterns, suggests defaults for detected languages)
   - Runs `git init /data/`
   - Runs `git add . && git commit -m "Initial commit"`
   - Asks: "Want to push this to GitHub/GitLab?"
     - If yes: uses OAuth connection to create/select a repo, `git remote add origin <url>`, `git push`
     - If no: done — local-only git
   - Notifies control-plane: `projects.update { has_git: true, git_url: "" }` or `{ has_git: true, git_url: "<url>" }`
7. Control-plane updates DB, KV, ConfigMap
8. Active sessions on the project are notified via project update event
9. Future new sessions get worktrees at `/data/worktrees/{branch}/`
10. Conversion session ends (or user continues using it)

### Connecting a remote later

1. User in any session says "push this to GitHub" (or goes to project settings → Connect Remote)
2. Claude uses OAuth connection to create/select a repo
3. Runs `git remote add origin <url> && git push --all`
4. Notifies control-plane: `projects.update { git_url: "<url>" }`
5. Control-plane updates DB, KV, ConfigMap

### Disconnecting a remote

1. User goes to project settings → Disconnect Remote
2. Control-plane updates `git_url` from URL to `''` (has_git stays true)
3. Local git repo and history preserved, just no remote push/pull
4. Sessions continue working in worktrees — nothing changes on disk

## Component Changes

### SPA (`mclaude-web/`)

**Project settings screen** — new "Version Control" section:

| State | Display | Actions |
|-------|---------|---------|
| `has_git=false` | Version Control: **None** | \[Enable Git\] |
| `has_git=true, git_url=''` | Version Control: **Local Git** | \[Connect Remote\] |
| `has_git=true, git_url='<url>'` | Version Control: **`<url>`** | \[Disconnect Remote\] |

**Enable Git button behavior:**
1. POST to session create API with `{ skill: "/git-init" }` (or equivalent field that auto-triggers the skill)
2. Navigate to the new session

**Project update notification:**
When control-plane publishes a project update event (has_git changed), active sessions in the SPA show a toast: "Version control enabled for this project."

### Control-plane (`mclaude-control-plane/`)

**`projects.update` handler** (`mclaude.{userId}.api.projects.update`) — accept `has_git` and `git_url` field updates:
- Validates state transitions:
  - `(false,'')` → `(true,'')` — enable local git
  - `(false,'')` → `(true,'<url>')` — enable git with remote
  - `(true,'')` → `(true,'<url>')` — connect remote
  - `(true,'<url>')` → `(true,'')` — disconnect remote
- Rejects: any transition where `has_git` goes from true to false (one-way)
- Updates Postgres `projects.has_git` and `projects.git_url`
- Updates `mclaude-projects` KV entry
- Updates ConfigMap `project-{id}-config` with `GIT_URL` value
- Publishes project update event: `mclaude.{userId}.api.projects.updated` (global, not location-scoped — projects span all locations)
- Does NOT update MCProject CRD — git config lives in ConfigMap, not CRD

**MCProject CRD** — keeps `spec.gitURL` for initial provisioning (clone-on-first-start). Reconciler creates a ConfigMap `project-{id}-config` alongside the Deployment. The Deployment mounts the ConfigMap as a file at `/etc/mclaude/config`.

**ConfigMap** — `project-{id}-config`:
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: project-{projectId}-config
data:
  GIT_URL: ""          # updated by control-plane on projects.update
```

Mounted as a volume in the session-agent pod. ConfigMap volume mounts auto-update without pod restart (kubelet sync period, typically ~60s).

### Session-agent (`mclaude-session-agent/`)

**Entrypoint changes** — filesystem-first repo detection:

```bash
# Read git URL from ConfigMap mount (preferred, auto-updates without restart)
# Falls back to GIT_URL env var during migration (before ConfigMap is added)
GIT_URL=$(cat /etc/mclaude/config/GIT_URL 2>/dev/null || echo "${GIT_URL:-}")

if [ -d "/data/.git" ]; then
    # Repo already exists — fetch if remote configured
    if git -C /data remote | grep -q .; then
        git -C /data fetch --all --prune
    fi
elif [ -n "$GIT_URL" ]; then
    # First start with a remote URL — clone
    git clone "$GIT_URL" /data/
fi
# Otherwise: scratch project, /data/ used directly
```

No magic strings. No `GIT_URL=local` sentinel. The entrypoint trusts the filesystem:
- `/data/.git/` exists → git project (check remotes for fetch)
- `/data/.git/` doesn't exist + `GIT_URL` set → clone
- Neither → scratch

**Session create changes** — worktree logic based on filesystem:

```
if /data/.git/ exists:
    # Git project (remote or local-only)
    create worktree: git -C /data worktree add /data/worktrees/{branchSlug} -b {branch}
    cwd = /data/worktrees/{branchSlug}
else:
    # Scratch project
    cwd = /data/
fi
```

**`/git-init` skill support** — the session running the git-init skill operates on `/data/` directly (it's a scratch session at the point of conversion, no worktree). After the skill commits, `/data/.git/` exists. The next session created will see `.git/` and get a worktree.

### Helm (`charts/mclaude/`)

**ConfigMap template** — new template for project config ConfigMap. Created by the reconciler alongside the Deployment. Mounted as a volume at `/etc/mclaude/config`.

**Deployment template** — add volume mount for the ConfigMap:
```yaml
volumes:
  - name: project-config
    configMap:
      name: project-{{ .projectId }}-config
containers:
  - name: session-agent
    volumeMounts:
      - name: project-config
        mountPath: /etc/mclaude/config
        readOnly: true
```

## Data Model

### Schema

```sql
-- Migration: add has_git column
ALTER TABLE projects ADD COLUMN has_git BOOLEAN NOT NULL DEFAULT false;

-- Constraint: git_url requires has_git
ALTER TABLE projects ADD CONSTRAINT git_url_requires_git
    CHECK (has_git OR git_url = '');
```

| `has_git` | `git_url` | State |
|-----------|-----------|-------|
| false | '' | scratch |
| true | '' | local git |
| true | 'git@...' | remote git |
| false | 'git@...' | **rejected by CHECK** |

### State transitions

```
scratch (false,'') ──→ local git (true,'') ──→ remote (true,'<url>')
scratch (false,'') ──→ remote (true,'<url>')                  ↓
                                               local git (true,'')  ← disconnect
```

Cannot transition `has_git` from true to false.

### KV entry

`mclaude-projects` KV — `ProjectKVState` gains `HasGit bool` field. `GitURL` field unchanged.

### MCProject CRD

`spec.gitURL` — used only for initial clone on first pod start. NOT updated on conversion. The reconciler creates a ConfigMap with the current git URL; the ConfigMap is the live source of truth for the session-agent.

### ConfigMap

`project-{id}-config` — created by the reconciler. Updated by control-plane on `projects.update`. Mounted as a file volume. Auto-updates without pod restart.

## Error Handling

| Scenario | Behavior |
|----------|----------|
| `git init` fails (disk full, permissions) | Skill reports error to user. Project stays scratch. User can retry. |
| `git add` fails (path too long, encoding issues) | Skill reports error, suggests fixing the file, retries. |
| `git push` fails (auth, network) | Skill reports error. Project is local-only git. User can retry push later. |
| Remote repo has existing content | Claude walks user through: merge, rebase, or force push. User decides. |
| Two sessions try to convert simultaneously | `git init` is idempotent — second init is a no-op. First `git add && commit` wins. Second session sees repo already exists. |
| Control-plane rejects state transition | Skill reports: "This project already has version control." |
| Active session writes to `/data/` after conversion | The write lands in the main branch working tree. Not in any worktree. Next session to work on main would see it. Low risk — active sessions end eventually. |
| ConfigMap update delay | kubelet syncs ConfigMap volumes every ~60s. Between update and sync, entrypoint reads stale value. Acceptable — only affects fetch-on-restart timing. |

## Security

- The `/git-init` skill runs with the same permissions as any Claude session — no elevated access needed.
- OAuth tokens for GitHub/GitLab are managed by the existing OAuth connection flow (see `adr-2026-04-14-github-oauth.md`). The skill uses the connection's `gh`/`glab` CLI credentials to create repos and push.
- `has_git` and `git_url` updates go through the control-plane API, which validates the requesting user owns the project.
- ConfigMap is in the project's namespace, scoped by RBAC. Only the control-plane ServiceAccount can update it.

## PVC Layout

### Before conversion (scratch)

```
/data/
  file1.py
  file2.py
  ...
```

### After conversion (local-only or remote)

```
/data/                      ← main branch checkout (no session uses directly)
  .git/
  file1.py
  file2.py
  ...
/data/worktrees/
  session-abc/              ← session 1's working tree
  session-def/              ← session 2's working tree
```

### Change from current model

The current git project model uses a bare repo at `/data/repo/` with worktrees at `/data/worktrees/`. This changes to a regular repo at `/data/` with worktrees at `/data/worktrees/`. This unifies the layout for both cloned-from-remote and converted-from-scratch projects.

**Migration**: existing git projects with `/data/repo/` (bare) need migration to `/data/` (regular). This can be handled in the entrypoint: if `/data/repo/` exists and `/data/.git/` does not, convert the bare repo to a regular checkout:

```bash
if [ -d "/data/repo" ] && [ ! -d "/data/.git" ]; then
    git clone /data/repo /data/checkout
    rm -rf /data/repo
    (shopt -s dotglob && mv /data/checkout/* /data/)
    rmdir /data/checkout
fi
```

## Scope

### v1

- "Enable Git" in project settings → spawns conversion session with `/git-init` skill
- `/git-init` skill: git init, gitignore setup, initial commit, optional remote connection
- Control-plane: `projects.update` with `has_git` + `git_url` state transitions, ConfigMap updates
- Session-agent: filesystem-first entrypoint, detect `/data/.git/` for worktree vs scratch mode
- SPA: version control section in project settings, session creation with skill trigger
- Unified regular-repo layout (migration from bare if needed)
- ConfigMap for runtime git config (no pod restart on conversion)

### Deferred

- Auto-suggestion of git conversion based on project complexity
- Git branch management UI in the SPA (create/switch/merge branches)
- Pull request creation from the SPA
- Git history viewer in the SPA
- Periodic auto-commit for local-only git projects
