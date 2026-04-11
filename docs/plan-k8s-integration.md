# K8s Integration Plan

## Context

mclaude already has a `K8sSessionManager` that creates pods and sends input via `kubectl exec`. This plan completes the integration using a **pod-per-project** model: one pod per git repo runs tmux, and each Claude Code session is a tmux window inside it. Multiple branches are handled via git worktrees within the same pod. Sessions share a filesystem naturally — enabling monorepo multi-session, agent-agent via filesystem, and shared Docker. On pod restart, a session manifest on the PVC lets the entrypoint relaunch all windows with `claude --resume`.

Each user gets their own namespace (`mclaude-{userId}`) with a **per-namespace mclaude-server** Deployment that connects to the relay via WebSocket tunnel. A **mclaude-controller** in the system namespace handles provisioning (namespaces, RBAC, server Deployments). The relay is a network proxy only — no cluster management.

### Anthropic API Access

In environments where cluster egress to `api.anthropic.com` is blocked (corporate firewalls), an HTTP CONNECT proxy (e.g., Squid) is required. All project pods set `HTTPS_PROXY` to route Anthropic API calls through the proxy.

```
HTTPS_PROXY=http://{proxy-host}:{proxy-port}
```

Claude Code (Go HTTP client) respects `HTTPS_PROXY` natively. The proxy should whitelist only `api.anthropic.com` via ACL (e.g., Squid `dstdomain`). See Step 0 for recommended Squid setup.

**Proxy URL is configurable** — injected via env var on the project Deployment, not hardcoded. Environments without egress restrictions omit the proxy entirely.

---

## Architecture

### 3-Tier Storage

| Tier | Scope | K8s Resource | Contents |
|------|-------|-------------|----------|
| **User** | Per namespace (`mclaude-{userId}`) | ConfigMap + Secret + Postgres | `CLAUDE.md`, `settings.json`, skills, commands, MCP config, credentials, session/event metadata |
| **Project** | Per project PVC | PVC (RWO) | Bare git repo, worktrees, JSONL files, shared memory |
| **Session** | Per tmux window | Row in Postgres `sessions` table | Metadata in DB, JSONL files on project PVC |
| **Home** | Per pod (emptyDir) | emptyDir | Seeded from Secret + ConfigMap on boot. Writable, ephemeral. Not shared across project pods. |

JSONL lives on the project PVC, not ephemeral storage, so it survives pod restarts and `claude --resume` works.

### Home Directory (ephemeral emptyDir)

`$HOME` is an emptyDir — fresh on every pod start, writable, not persisted, not shared across project pods. This is by design: credentials belong in K8s Secrets (encrypted at rest, K8s RBAC-scoped), not on any persistent storage that's browsable at the infrastructure layer.

**On boot**, the entrypoint seeds `$HOME` from:

- K8s Secret: SSH keys, OAuth tokens, PATs, `.netrc`, any user-defined credential files
- ConfigMap: `settings.json`, `CLAUDE.md`, commands, skills (non-sensitive Claude config)

**During the session**, agents can write anything to `$HOME` — install tools, update PATH, write credentials. Other sessions in the same pod see changes immediately (shared emptyDir). But changes are lost on pod restart.

**Persisting user config changes**: config-sync sidecar watches `~/.claude/settings.json` and `CLAUDE.md` for changes, patches the ConfigMap. These survive pod restarts via re-seeding.

**Persisting new credentials**: user adds them via the secrets management UI. The UI patches the K8s Secret, next pod start picks them up. Credential changes made by agents during a session (e.g., `gh auth login`) are ephemeral — they work for the life of the pod but are not automatically persisted.

**Tradeoff**: `$HOME` is not shared across project pods. An agent installing a tool in project A won't be visible in project B. This is accepted — cross-project home sharing requires a shared filesystem (Azure Files), which exposes secrets at the Azure RBAC layer.

### CLAUDE.md Tiers

Claude Code has a native managed policy location for organization-wide instructions: `/etc/claude-code/CLAUDE.md` on Linux. This is the highest priority tier — loaded before everything else, cannot be excluded by users.

| Tier | Location | Controlled by | Persisted how | Can user override? |
|------|----------|--------------|--------------|-------------------|
| **Global (managed policy)** | `/etc/claude-code/CLAUDE.md` | mclaude platform | Baked into session image (or mounted from `mclaude-system` ConfigMap) | No |
| **User** | `~/.claude/CLAUDE.md` | Individual user | ConfigMap via config-sync sidecar | Yes |
| **Project** | `{worktree}/CLAUDE.md` | Repo (committed) | Git | Yes |

Claude Code also supports `~/.claude/rules/*.md` for modular user-level rules, and `{repo}/.claude/rules/*.md` for project-level rules. These are additive.

**Global CLAUDE.md contents** (platform conventions):

```markdown
# MClaude Platform

## Environment
You are running in a Kubernetes pod. Key paths:
- `/data/repo/` — bare git repo (shared across worktrees)
- `/data/worktrees/{branch}/` — git worktrees (one per branch)
- `/data/shared-memory/` — auto-memory shared across all worktrees
- `$HOME` is ephemeral — rebuilt on every pod restart

## Shell Conventions
- Never write secrets directly to `~/.zshrc` — it is synced to a ConfigMap.
- Use `~/.zshrc.local` for session-scoped shell additions (ephemeral).
- Secrets are available as env vars via `~/.env.secrets` (sourced by .zshrc).
- To add a persistent secret, ask the user to add it via the secrets UI or OpenBao.

## Credentials
- Do not hardcode tokens, passwords, or keys in any persisted file
  (CLAUDE.md, settings.json, .zshrc).
- Credentials in `$HOME` are ephemeral — they work for this session
  but are lost on pod restart.

## Git
- Worktrees are managed by the platform. To work on a new branch,
  ask the user to create a new session — do not use `git checkout`
  or `git switch` (these are blocked by platform hooks).
- The bare repo is at `/data/repo/`. Do not modify it directly.
- Push/pull works normally within a worktree.

## Tool Installation
- Use `pkg install <package>` to install tools.
- Do not use `apt install` or `apt-get` — they are not available.
- Tools are cached and shared across all project pods automatically.
- Installed tools persist across pod restarts.

## Docker
- Docker is available via the dockerd-rootless sidecar.
- `DOCKER_HOST` is pre-configured.
```

**Platform hooks** (enforced via `/etc/claude-code/settings.json`, cannot be overridden):

Claude Code hooks intercept Bash commands at execution time — both agent-initiated and user `!` commands. This is stricter than CLAUDE.md instructions, which agents can ignore.

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hook": "/etc/claude-code/hooks/guard.sh"
      }
    ]
  }
}
```

**Guard script** (`/etc/claude-code/hooks/guard.sh`, baked into session image):

```bash
#!/bin/bash
COMMAND=$(cat | jq -r '.input.command // empty')

# Block git branch switching (platform manages worktrees)
if echo "$COMMAND" | grep -qE '^\s*git\s+(checkout|switch)\s'; then
    echo "BLOCK: Branch switching is managed by the platform. Create a new session for a different branch." >&2
    exit 2
fi

# Block real apt (use pkg shim)
if echo "$COMMAND" | grep -qE '(^|\s|/)(apt-get|apt)\s+install'; then
    echo "BLOCK: Use 'pkg install <package>' instead." >&2
    exit 2
fi

# Block modifying managed platform config
if echo "$COMMAND" | grep -qE '/etc/claude-code/'; then
    echo "BLOCK: Managed platform config cannot be modified." >&2
    exit 2
fi

# Block nuking critical paths
if echo "$COMMAND" | grep -qE 'rm\s+(-rf|-fr)\s+/(data/repo|nix|etc)\b'; then
    echo "BLOCK: Cannot delete platform-managed directories." >&2
    exit 2
fi

exit 0
```

The CLAUDE.md explains *why* (so agents understand and don't try workarounds). The hooks *enforce* it. Two layers.

**Updating the global CLAUDE.md**: either rebuild the session image (for baked-in changes) or mount it from a ConfigMap in `mclaude-system` namespace (for dynamic updates without image rebuilds). The ConfigMap approach is preferred — the controller applies it during provisioning, and platform admins can update it cluster-wide.

### Tool Installation (Nix + shared store)

The session image ships with Nix (single-user mode, ~100MB). `apt` and `brew` are shimmed to Nix so agents can use familiar commands. The Nix store (`/nix/`) lives on a shared Azure Files volume (RWX, standard tier) — install a tool in any project pod and it's immediately available in all pods.

**Three volumes per project pod**:

| Mount | Storage | Contains | Sensitive? |
|-------|---------|----------|-----------|
| `$HOME` | emptyDir | Dotfiles, credentials | Yes — ephemeral, protected |
| `/data/` | project PVC (RWO, `managed-csi-premium`) | Code, worktrees, memory | No |
| `/nix/` | Azure Files (RWX, `azurefile-csi`) | Package binaries, Nix store | No — public packages only |

**Nix store PVC** (one per namespace, shared across all project pods):

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: nix-store
  namespace: mclaude-{userId}
spec:
  accessModes: [ReadWriteMany]
  storageClassName: azurefile-csi
  resources:
    requests:
      storage: 20Gi
```

**Shims** (in session image at `/usr/local/bin/`):

```bash
#!/bin/bash
# /usr/local/bin/apt — shim that redirects to nix
if [ "$1" = "install" ]; then
    shift; for pkg in "$@"; do nix profile install "nixpkgs#$pkg"; done
elif [ "$1" = "remove" ]; then
    shift; for pkg in "$@"; do nix profile remove "nixpkgs#$pkg"; done
else
    echo "apt is shimmed to nix. Use: apt install <package>"
fi
```

Users who want devbox, mise, or any other tool manager can install it via Nix: `nix profile install nixpkgs#devbox`. The platform doesn't opinionate beyond providing Nix as the foundation.

### Projects, Worktrees, and Sessions

| Concept | What it is | K8s resource | Shared across sessions? |
|---------|-----------|-------------|------------------------|
| **Project** | A git repo | Deployment + PVC | Yes — bare repo, Docker, auto-memory |
| **Worktree** | A branch checkout | Directory on PVC | Only sessions on the same branch |
| **Session** | A Claude Code process | tmux window | Shares worktree + project memory |

**Default flow**: each new session gets its own worktree (new branch checkout = isolation). Option to join an existing worktree for collaboration (e.g. monorepo multi-agent on the same branch).

**Auto-memory sharing**: Claude Code keys auto-memory by working directory (`~/.claude/projects/{encoded-cwd}/memory/`). Different worktrees have different cwds, so they'd get separate memories by default. Since project-level memories (feedback, context, references) should be shared across all branches, a background process in the entrypoint symlinks each worktree's memory directory to a single shared location on the PVC:

```bash
mkdir -p /data/shared-memory
(while true; do
    for dir in "$HOME/.claude/projects"/*/; do
        [ -d "$dir" ] && [ ! -L "${dir}memory" ] && {
            rm -rf "${dir}memory"
            ln -s /data/shared-memory "${dir}memory"
        }
    done
    sleep 5
done) &
```

Concurrent memory writes are rare (once per conversation at most). Worst case is a lost index line in MEMORY.md — the memory file itself still exists on disk.

**Non-git projects**: not every project needs a repo. "Empty project" creates a PVC with no git setup — scratch space, prototyping, non-code work.

**Runtime differences**:

| Runtime | Structure | Primary action | Worktree isolation? |
|---------|-----------|---------------|-------------------|
| **K8s** | Project cards → sessions (with branch labels) | "New session" (creates worktree + session) | Yes — each session gets its own worktree by default |
| **Laptop/VM** | Flat session list | "New session" (tmux window) | Same — worktrees work on VMs too |

Worktrees work identically on both runtimes. The only K8s-specific concept is the project pod lifecycle (idle teardown, PVC persistence). On a laptop, the "project" is just a directory.

**New session dialog (same on K8s and VM)**:

```
┌─────────────────────────────────────────┐
│  New session                            │
│                                         │
│  Project: [myapp ▾]  or  [Clone repo]   │
│                           [Empty project]│
│                                         │
│  Branch:  [feature/auth ▾] [new branch] │
│  Path:    [/] (optional subdirectory)   │
│                                         │
│  ☐ Join existing worktree               │
│    (share files with other sessions     │
│     on this branch)                     │
│                                         │
│  [Create]                               │
└─────────────────────────────────────────┘
```

"Clone repo" accepts any git URL, or if connected to GHES, offers search/autocomplete. "Join existing worktree" is unchecked by default — checking it adds the session to an existing worktree instead of creating a new one. This is the advanced flow for multi-agent collaboration on the same branch.

**API**:

- `POST /projects` with `{"gitUrl": "...", "name": "myapp"}` → creates project Deployment + PVC, returns `projectId`
- `GET /projects` → lists projects (pod status, session count, worktrees)
- `DELETE /projects/:id` → scales Deployment to 0 (PVC retained unless `?purge=true`)
- `POST /sessions` with `{"projectId": "myapp", "branch": "main", "cwd": "/", "joinWorktree": false}` → creates worktree + session (or joins existing worktree if `joinWorktree: true`)
- `DELETE /sessions/:id` → kills session, cleans up worktree if no other sessions use it
- `POST /sessions/:id/restart` → kills + relaunches with `--resume`

**UI layout**:

- Main view: project cards, each showing its sessions grouped by branch
- **"New session"** is the primary action — prominent button, top of the page. If no projects exist, prompts to create one first.
- Each project card shows sessions inline with branch labels
- Project `···` menu: delete project, view PVC usage
- Session `···` menu: restart session, delete session

### Pod Structure (one per project)

```
Pod: project-{projectId}-xxxxx        namespace: mclaude-{userId}
├── container: tmux-host
│   ├── project PVC           → /data/                         (RW) bare repo, worktrees, JSONL
│   ├── nix-store PVC         → /nix/                          (RWX) shared Nix package store
│   ├── claude-home emptyDir  → /home/node/.claude/             (RW) writable user config
│   ├── user-config ConfigMap → /home/node/.claude-seed/        (RO) initial seed only
│   ├── user-secrets Secret   → /home/node/.user-secrets/       (RO)
│   └── docker-sock emptyDir  → /var/run/                       (shares docker.sock)
├── container: jsonl-tailer         (sidecar)
│   └── project PVC           → /data/                         (RO) tails JSONL, inserts into Postgres
├── container: config-sync          (sidecar)
│   ├── claude-home emptyDir  → /claude-home/                   (RW) watches for changes
│   └── user-config ConfigMap → /claude-seed/                   (RO) sync target
└── container: dockerd-rootless
    └── docker-sock emptyDir  → /var/run/
```

`/data/` layout:

```
/data/
  repo/                ← bare git repo (shared across all worktrees)
  worktrees/
    main/              ← git worktree for main
    feature-auth/      ← git worktree for feature/auth
    fix-login/         ← git worktree for fix/login
  shared-memory/       ← auto-memory shared across all worktrees (via symlink)
  projects/            ← symlinked to ~/.claude/projects/ (JSONL history per worktree)
```

### Sessions (tmux windows)

Each session = one tmux window. mclaude-server manages them via `kubectl exec {pod} -n mclaude-{userId} -- tmux ...`:

| Operation | Command |
|-----------|---------|
| Create | `tmux new-window -c {cwd} -n {sessionId} "claude --dangerously-skip-permissions"` |
| Delete | `tmux kill-window -t {sessionId}` |
| Send input | `tmux send-keys -t {sessionId} {text} Enter` |
| Capture output | `tmux capture-pane -t {sessionId} -p` |
| List | `tmux list-windows -F "#{window_name} #{window_index}"` |

This is identical to `TmuxMonitor` — just prefixed with `kubectl exec {pod} -n {namespace} --`.

### Session State (Postgres)

Session state lives in the namespace Postgres instance (see schema below). No more `sessions.json` on each PVC. The server writes to the `sessions` table on every create/delete, and the jsonl-tailer writes `conversation_id` once it discovers the JSONL filename.

On pod restart, the entrypoint queries the server API (which reads from Postgres) to get the session list for this project, then relaunches each with `--resume {conversationId}`.

### JSONL Streaming (jsonl-tailer sidecar → Postgres → server via LISTEN/NOTIFY)

Each project pod includes a **jsonl-tailer** sidecar that watches JSONL files on the PVC and inserts events into the namespace Postgres instance. The server receives events in real-time via Postgres `LISTEN/NOTIFY` — no custom WebSocket protocol, no polling, and events are persisted + queryable.

**jsonl-tailer sidecar**:

- Mounts the project PVC at `/data/` (read-only)
- Watches `/data/projects/` for new JSONL files via `inotifywait` (new sessions create new files)
- `tail -F` each active JSONL file, inserts each line into Postgres: `INSERT INTO events (session_id, project_id, data) VALUES (...)`
- Connects to `postgres.mclaude-{userId}.svc.cluster.local:5432`
- Also extracts `conversationId` from JSONL filename and writes it to the `sessions` table

**Postgres trigger** (fires on every event insert):

```sql
CREATE OR REPLACE FUNCTION notify_new_event() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('new_event', json_build_object(
        'session_id', NEW.session_id,
        'id', NEW.id
    )::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER event_inserted
    AFTER INSERT ON events
    FOR EACH ROW EXECUTE FUNCTION notify_new_event();
```

**Per-namespace server**:

- Holds a persistent Postgres connection with `LISTEN new_event`
- On notification: fetches the new event row, parses with `JSONLParser.parseEvent(line:)`, fires `onEvent?(sessionId, event)` → broadcasts to web clients
- Tracks `sessionWorking`, `lastTurnEndTimestamp` per session

**`GET /sessions/:id/events`**: just a SQL query — `SELECT * FROM events WHERE session_id = ? ORDER BY id DESC LIMIT 200`. No in-memory accumulation, survives server restarts.

**Latency**: JSONL write → inotify (~1ms) → INSERT (~1-2ms) → trigger + NOTIFY (~0.1ms) → server LISTEN receives (~0.1ms) → broadcast. Total ~2-3ms, same as the WebSocket approach but with persistence and queryability for free.

### Namespace Postgres

Each user namespace runs a single `postgres:17-alpine` instance. Lightweight (128 MB memory limit, 1Gi PVC), shared by the server and all project pod sidecars.

**Deployment**:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: postgres
  namespace: mclaude-{userId}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: postgres
  template:
    metadata:
      labels:
        app: postgres
    spec:
      containers:
        - name: postgres
          image: postgres:17-alpine
          env:
            - name: POSTGRES_DB
              value: mclaude
            - name: POSTGRES_USER
              value: mclaude
            - name: POSTGRES_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: user-secrets
                  key: postgres-password
          volumeMounts:
            - name: pgdata
              mountPath: /var/lib/postgresql/data
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 500m
              memory: 128Mi
      volumes:
        - name: pgdata
          persistentVolumeClaim:
            claimName: postgres-data
---
apiVersion: v1
kind: Service
metadata:
  name: postgres
  namespace: mclaude-{userId}
spec:
  selector:
    app: postgres
  ports:
    - port: 5432
      targetPort: 5432
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: postgres-data
  namespace: mclaude-{userId}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: managed-csi-premium
  resources:
    requests:
      storage: 1Gi
```

**Schema**:

```sql
CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    git_url TEXT,
    created_at TIMESTAMPTZ DEFAULT now(),
    last_active_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    project_id TEXT REFERENCES projects(id),
    worktree TEXT NOT NULL,
    cwd TEXT NOT NULL,
    name TEXT,
    conversation_id TEXT,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE events (
    id BIGSERIAL PRIMARY KEY,
    session_id TEXT REFERENCES sessions(id),
    project_id TEXT REFERENCES projects(id),
    data JSONB NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_events_session ON events(session_id, id DESC);
```

This replaces:

- `sessions.json` on each project PVC → `sessions` table
- In-memory event accumulation on server → `events` table
- Server recovery dance → `SELECT * FROM sessions`
- JSONL offset tracking → `SELECT MAX(id) FROM events WHERE session_id = ?`

### Database Migrations

Schema changes are managed by an **init container** on the server Deployment that runs before the server starts. Uses [dbmate](https://github.com/amacneil/dbmate) — a standalone migration tool (single binary, no runtime deps).

**Init container** (on server Deployment):

```yaml
initContainers:
  - name: migrate
    image: ghcr.io/amacneil/dbmate:latest
    command: ["dbmate", "--url", "$(DATABASE_URL)", "--migrations-dir", "/migrations", "up"]
    env:
      - name: DATABASE_URL
        value: "postgres://mclaude:$(POSTGRES_PASSWORD)@postgres:5432/mclaude?sslmode=disable"
      - name: POSTGRES_PASSWORD
        valueFrom:
          secretKeyRef:
            name: user-secrets
            key: postgres-password
    volumeMounts:
      - name: migrations
        mountPath: /migrations
volumes:
  - name: migrations
    configMap:
      name: db-migrations
```

**Migration files** (stored in a ConfigMap applied by the controller, or baked into the server image):

```
mclaude-server/migrations/
  001_initial_schema.sql        ← projects, sessions, events tables + trigger
  002_add_event_retention.sql   ← retention policy, auto-cleanup old events
  003_add_worktree_index.sql    ← future schema changes
```

Each migration is idempotent — dbmate tracks applied migrations in a `schema_migrations` table. The init container runs on every server pod start, applies any pending migrations, then exits. If no new migrations, it's a no-op (~100ms).

**How schema updates ship**:

1. Developer adds a new migration file (e.g., `004_add_column.sql`)
2. Controller updates the `db-migrations` ConfigMap (or new server image is deployed)
3. Server pod restarts (rolling update) → init container runs → migration applied → server starts with new schema

**First-time provisioning**: the controller creates the `db-migrations` ConfigMap as part of namespace setup. On the server's first start, the init container creates all tables from scratch (migration 001).

### User-Level Config (writable emptyDir + config-sync sidecar)

ConfigMap `user-config` in `mclaude-{userId}` namespace stores the **seed** values:

- `CLAUDE.md`
- `settings.json`
- `commands/` entries (as individual keys)
- `skills/` entries
- MCP server config (embedded in `settings.json`)

Mounted read-only at `/home/node/.claude-seed/` — used only to populate `~/.claude/` on pod start.

The actual `~/.claude/` is an **emptyDir** (`claude-home`) shared between `tmux-host` and the `config-sync` sidecar. Since emptyDir is ephemeral, the entrypoint copies from the seed on every pod start (not just first boot). This makes it writable — `claude mcp add --scope user`, manual edits to `settings.json`, etc. all work exactly like local.

**Seed flow** (entrypoint, every pod start):

```bash
# Copy from ConfigMap seed (emptyDir is fresh each boot)
for f in CLAUDE.md settings.json; do
    [ -f "/home/node/.claude-seed/$f" ] && \
        cp "/home/node/.claude-seed/$f" "$HOME/.claude/$f"
done
for d in commands skills; do
    [ -d "/home/node/.claude-seed/$d" ] && \
        cp -r "/home/node/.claude-seed/$d" "$HOME/.claude/$d"
done
```

**Sync flow** (`config-sync` sidecar):

- Watches `~/.claude/settings.json`, `CLAUDE.md`, etc. for writes via `inotifywait`
- On change, patches the `user-config` ConfigMap in the pod's namespace via `kubectl`
- The sidecar needs a ServiceAccount with RBAC to patch ConfigMaps in its own namespace

**Cross-pod propagation + session restart** (per-namespace server):

When user-scope config changes (e.g. `claude mcp add`, skill update), ALL sessions across ALL project pods need to restart to pick up the change. MCP servers are started at conversation startup, so a running Claude process won't see new MCP config without a restart.

The per-namespace server handles this:

1. Server watches the `user-config` ConfigMap via K8s API watch
2. On change, for each running project pod:
   - `kubectl exec {pod} -- cp /home/node/.claude-seed/* /home/node/.claude/` (re-seed emptyDir from the now-updated ConfigMap mount — kubelet refreshes ConfigMap mounts within ~60s, but the server can also `kubectl exec` to write the new content directly for immediate propagation)
   - Restart all sessions in that pod via existing `restartSession` (kill tmux window + relaunch with `--resume`)
3. Web UI receives a notification: "User settings changed, restarting sessions..." — sessions resume their conversations with fresh config

This means the config-sync sidecar is responsible for **outbound** sync (local change → ConfigMap), and the server is responsible for **inbound** sync (ConfigMap change → all pods + session restarts).

### System Components

Three components run in `mclaude-system` namespace or cluster-wide:

| Component | Where | Role | RBAC |
|-----------|-------|------|------|
| **mclaude-relay** | `mclaude-system` | Network proxy. Routes WebSocket/HTTP between web clients and per-namespace servers. No cluster management. | None (or minimal — just its own namespace) |
| **mclaude-controller** | `mclaude-system` | Provisions user namespaces, RBAC, Secrets, ConfigMaps, server Deployments. Manages lifecycle of per-namespace resources. | ClusterRole: create/delete namespaces, Deployments, Roles, RoleBindings, ServiceAccounts, Secrets, ConfigMaps |
| **Per-namespace server** | `mclaude-{userId}` | Manages project Deployments, sessions, worktrees, JSONL polling within its namespace. Connects to relay via tunnel. | Namespace Role: pods, pods/exec, Deployments, PVCs, ConfigMaps |

**Provisioning flow** (first-time user):

1. User requests a K8s session via web UI → relay has no tunnel for this user
2. Relay calls controller: `POST /provision/{userId}` with user credentials (oauth token, SSH key, git config)
3. Controller creates namespace `mclaude-{userId}`
4. Controller applies: Secret (`user-secrets`), ConfigMap (`user-config`), ServiceAccount, Role, RoleBinding, Postgres Deployment, nix-store PVC, server Deployment
5. Server pod starts, connects to relay via WebSocket tunnel as `k8s~{userId}`
6. Relay detects new tunnel, forwards the original session request to the server
7. Server creates the project Deployment + PVC + worktree + session

**Subsequent requests**: relay already has a tunnel for the user, routes directly to server. No controller involvement.

### Per-Namespace Server

Each user namespace runs its own mclaude-server as a Deployment (1 replica). It uses the same binary as the laptop server — the only difference is it connects to the relay identified as `k8s~{userId}` instead of a laptop hostname, and manages project pods in its own namespace via kubectl exec.

**Connection to relay**: on startup, the server connects to the relay's `/tunnel` endpoint with `X-Hostname: k8s~{userId}`. The relay sees it as another tunnel alongside any laptop tunnels. The `k8s~` prefix distinguishes it from laptop connections. Web clients select a runtime (laptop or K8s) and the relay routes accordingly.

**Server Deployment**:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mclaude-server
  namespace: mclaude-{userId}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: mclaude-server
  template:
    metadata:
      labels:
        app: mclaude-server
    spec:
      serviceAccountName: mclaude-sa
      containers:
        - name: server
          image: ghcr.io/mclaude-project/mclaude-server:latest
          env:
            - name: RELAY_URL
              value: "wss://mclaude-relay.mclaude-system.svc.cluster.local/tunnel"
            - name: TUNNEL_TOKEN
              valueFrom:
                secretKeyRef:
                  name: user-secrets
                  key: tunnel-token
            - name: NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
            - name: USER_ID
              value: "{userId}"
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 500m
              memory: 256Mi
```

### Pod Creation and Lifecycle (Deployments)

Both the per-namespace server and project pods use **Deployments** (not bare Pods). This gives:

- Auto-restart on crash (Kubernetes recreates the pod)
- Rolling updates when the container image changes
- Scale to 0 for idle teardown, scale to 1 to resume

**Project Deployment** (created by per-namespace server):

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: project-{projectId}
  namespace: mclaude-{userId}
  labels:
    app: mclaude-project
    project: "{projectId}"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: mclaude-project
      project: "{projectId}"
  template:
    metadata:
      labels:
        app: mclaude-project
        project: "{projectId}"
    spec:
      serviceAccountName: mclaude-sa
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        runAsGroup: 1000
        fsGroup: 1000
      volumes:
        - name: project-data
          persistentVolumeClaim:
            claimName: project-{projectId}
        - name: claude-home
          emptyDir: {}
        - name: user-config
          configMap:
            name: user-config
        - name: user-secrets
          secret:
            secretName: user-secrets
        - name: nix-store
          persistentVolumeClaim:
            claimName: nix-store
        - name: docker-sock
          emptyDir: {}
      containers:
        - name: tmux-host
          image: ghcr.io/mclaude-project/mclaude-session:latest
          securityContext:
            allowPrivilegeEscalation: false
          volumeMounts:
            - name: project-data
              mountPath: /data
            - name: nix-store
              mountPath: /nix
            - name: claude-home
              mountPath: /home/node/.claude
            - name: user-config
              mountPath: /home/node/.claude-seed
              readOnly: true
            - name: user-secrets
              mountPath: /home/node/.user-secrets
              readOnly: true
            - name: docker-sock
              mountPath: /var/run
          env:
            - name: GIT_URL
              value: "{gitUrl}"
            - name: DOCKER_HOST
              value: "unix:///var/run/docker.sock"
            - name: HTTPS_PROXY
              value: "{proxyUrl}"       # omit if no proxy needed
            - name: https_proxy
              value: "{proxyUrl}"
            - name: NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
          resources:
            requests:
              cpu: 200m
              memory: 512Mi
            limits:
              cpu: 4000m
              memory: 8Gi
        - name: config-sync
          image: bitnami/kubectl:latest
          command: ["/bin/sh", "-c"]
          args:
            - |
              apk add --no-cache inotify-tools jq 2>/dev/null || true
              echo "[config-sync] Watching /claude-home/ for changes..."
              while true; do
                inotifywait -qq -r -e close_write /claude-home/ 2>/dev/null || sleep 30
                sleep 1  # debounce rapid writes
                PATCH="{\"data\":{"
                SEP=""
                for f in settings.json CLAUDE.md; do
                  if [ -f "/claude-home/$f" ]; then
                    PATCH="${PATCH}${SEP}\"$f\":$(jq -Rs . < "/claude-home/$f")"
                    SEP=","
                  fi
                done
                PATCH="${PATCH}}}"
                kubectl patch configmap user-config -n "$NAMESPACE" -p "$PATCH" 2>/dev/null && \
                  echo "[config-sync] Synced to ConfigMap" || \
                  echo "[config-sync] Sync failed (will retry)"
              done
          env:
            - name: NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
          volumeMounts:
            - name: claude-home
              mountPath: /claude-home
              readOnly: true
          resources:
            requests:
              cpu: 10m
              memory: 32Mi
            limits:
              cpu: 100m
              memory: 64Mi
        - name: jsonl-tailer
          image: ghcr.io/mclaude-project/mclaude-jsonl-tailer:latest
          volumeMounts:
            - name: project-data
              mountPath: /data
              readOnly: true
          env:
            - name: POSTGRES_URL
              value: "postgres://mclaude:$(POSTGRES_PASSWORD)@postgres:5432/mclaude"
            - name: POSTGRES_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: user-secrets
                  key: postgres-password
            - name: PROJECT_ID
              value: "{projectId}"
          resources:
            requests:
              cpu: 10m
              memory: 32Mi
            limits:
              cpu: 100m
              memory: 64Mi
        - name: dockerd-rootless
          image: docker:dind-rootless
          securityContext:
            allowPrivilegeEscalation: false
          volumeMounts:
            - name: docker-sock
              mountPath: /var/run
          resources:
            requests:
              cpu: 100m
              memory: 256Mi
            limits:
              cpu: 2000m
              memory: 4Gi
```

**Project PVC** (`project-{projectId}`, RWO, `managed-csi-premium`):

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: project-{projectId}
  namespace: mclaude-{userId}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: managed-csi-premium
  resources:
    requests:
      storage: 20Gi
```

**Lifecycle operations** (per-namespace server manages these):

| Operation | How |
|-----------|-----|
| Create project | `kubectl apply` Deployment + PVC. Wait for pod Running. |
| Idle teardown | `kubectl scale deployment project-{id} --replicas=0`. PVC persists. |
| Resume from idle | `kubectl scale deployment project-{id} --replicas=1`. Pod starts, mounts existing PVC. |
| Delete project | `kubectl delete deployment project-{id}`. PVC retained unless `?purge=true`. |
| Image update | `kubectl set image deployment/project-{id} tmux-host=new-image`. Rolling update. |

### Server Restart Recovery

The per-namespace server only needs to recover its own state. On startup:

1. **Query Postgres**: `SELECT * FROM sessions JOIN projects ON ...` → gives all session state, conversation IDs, worktrees
2. **Verify pods**: for each project with sessions, confirm the Deployment's pod is Running
3. **Resume LISTEN**: open persistent connection with `LISTEN new_event` — events start flowing immediately
4. **Start ConfigMap watch** on `user-config` — triggers cross-pod config propagation + session restarts on change

No recovery dance. No kubectl exec enumeration. No re-parsing JSONL from offset 0. Postgres has everything.

### Project Pod Lifecycle

Project pods are long-lived — they persist as long as there are active sessions. When the last session in a project is deleted:

1. `deleteSession` removes the tmux window and updates the manifest
2. If the deleted session was the last one using a worktree, clean up the worktree: `git -C /data/repo worktree remove /data/worktrees/{branch}`
3. Server starts an **idle timer** (configurable, default 30 minutes) for the project Deployment
4. If no new session is created before the timer fires, scale to 0: `kubectl scale deployment project-{projectId} --replicas=0`
5. The PVC is **not** deleted — it retains the bare repo, worktrees, JSONL history, and shared memory. Next session creation scales back to 1.

This keeps costs down (no idle pods burning CPU/memory) while preserving all project state. The PVC is the durable unit; the pod is ephemeral.

**Server idle**: if all project Deployments are scaled to 0 and no sessions remain, the server itself could idle-timeout (scale its own Deployment to 0 via the controller). The relay detects the tunnel disconnect. Next time the user creates a session, the relay calls the controller to scale the server back up.

State for idle tracking:

```swift
private var projectIdleTimers: [String: Task<Void, Never>] = [:]  // key: projectId
```

`createSession` cancels any pending idle timer for the project. `deleteSession` starts one if the project has zero remaining tmux windows.

---

## Critical Files

- `mclaude-server/Sources/K8sSessionManager.swift` — manages project Deployments + tmux sessions (namespace-local only)
- `mclaude-server/Sources/APIServer.swift` — project CRUD, session CRUD (delete, restart), endpoints
- `mclaude-server/Sources/main.swift` — wire K8s onEvent callback, tunnel connection to relay
- `mclaude-controller/` — new component: provisions namespaces, RBAC, server Deployments. ClusterRole.
- `mclaude-relay/` — network proxy only (no provisioning). Calls controller for new user bootstrap.
- `mclaude-session/entrypoint.sh` — manifest read, worktree re-creation, session relaunch, config seed, memory-sharing loop
- `mclaude-session/Dockerfile` — session image: claude CLI, tmux, git, jq, inotify-tools
- `mclaude-jsonl-tailer/` — sidecar: tails JSONL files, pushes to server via WebSocket

---

## Implementation Steps

### Step 0: Set Up HTTPS Proxy (if needed)

If the cluster cannot reach `api.anthropic.com` directly, deploy a Squid HTTP CONNECT proxy on a machine with egress access. Squid is production-grade, available in most Linux distro repos, with built-in systemd support and host whitelisting.

```bash
# Install
sudo dnf install -y squid   # or apt-get install squid

# Configure — whitelist only api.anthropic.com
sudo tee /etc/squid/squid.conf << 'EOF'
acl allowed_dst dstdomain api.anthropic.com
acl CONNECT method CONNECT
http_access allow CONNECT allowed_dst
http_access deny all
http_port 3128
EOF

sudo systemctl enable --now squid
```

All other CONNECT requests are denied. Squid handles logging (`/var/log/squid/access.log`), graceful restarts, and connection management.

Verify from the cluster: `curl -x http://{proxy-host}:3128 https://api.anthropic.com/api/hello`

**Skip this step** if the cluster has direct egress to `api.anthropic.com`.

---

### Step 1: Project Deployment + TmuxMonitor Adapter

**K8sSessionManager.swift** — replace pod-per-session with project Deployments:

`ensureProjectDeployment(projectId:, gitUrl:) async -> String`:

- Check if Deployment `project-{projectId}` already exists in this namespace
- If not, create PVC + Deployment (manifests above)
- If scaled to 0 (idle), scale back to 1
- Wait for pod to be Running (poll `kubectl get pods -l project={projectId}`)
- Return pod name

All session operations become kubectl exec tmux commands (see table above). Track `podName` and `windowName` per session id.

---

### Step 2: Project + Session CRUD

**createProject(gitUrl:, name:) async -> String?**:

1. Generate `projectId` from name (slugified) or auto-generate
2. Create PVC `project-{projectId}`
3. Create project Deployment (passes `GIT_URL` env var)
4. Wait for pod Running
5. Return `projectId`

**createSession(projectId:, branch:, cwd:, name:, joinWorktree:) async -> String?**:

1. Ensure project Deployment is scaled to 1 and pod is Running
2. Resolve worktree:
   - If `joinWorktree` and a worktree for `branch` already exists → use it
   - Otherwise → `kubectl exec {pod} -- git -C /data/repo worktree add /data/worktrees/{branch-slug} {branch}`
3. Set session cwd to `/data/worktrees/{branch-slug}/{cwd}`
4. Generate `sessionId = "k8s-\(UUID())"`
5. `kubectl exec {pod} -- tmux new-window -c {cwd} -n {sessionId} "claude --dangerously-skip-permissions"`
6. `INSERT INTO sessions (id, project_id, worktree, cwd, name) VALUES (...)`
7. Return sessionId

**deleteSession(id:) async -> Bool**:

1. `kubectl exec {pod} -- tmux kill-window -t {sessionId}`
2. `DELETE FROM sessions WHERE id = ?`
3. If this was the last session using its worktree, clean up: `git -C /data/repo worktree remove /data/worktrees/{branch-slug}`
4. If project has zero remaining sessions, start idle timer

**restartSession(id:) async -> Bool**:

1. `SELECT conversation_id, cwd FROM sessions WHERE id = ?`
2. `kubectl exec {pod} -- tmux kill-window -t {sessionId}`
3. `kubectl exec {pod} -- tmux new-window -c {cwd} -n {sessionId} "claude --dangerously-skip-permissions --resume {conversationId}"`

This gives a fresh Claude Code process (re-reads `settings.json`, starts new MCP servers) but resumes the exact conversation. Useful for picking up config changes or recovering hung sessions.

**APIServer.swift** — new endpoints:

| Method | Path | Action |
|--------|------|--------|
| `POST` | `/projects` | Create project (gitUrl, name) → returns projectId |
| `GET` | `/projects` | List projects (pod status, session count, worktrees) |
| `DELETE` | `/projects/:id` | Scale to 0 (PVC retained unless `?purge=true`) |
| `POST` | `/sessions` | Create session (projectId, branch, cwd, joinWorktree) → creates worktree + session |
| `DELETE` | `/sessions/:id` | Kill tmux window, clean up worktree if last session |
| `POST` | `/sessions/:id/restart` | Kill + relaunch with `--resume` |

**Web UI** (described in "New session dialog" above):

- Main view: project cards, each showing sessions with branch labels
- **"New session"** is the primary action — creates worktree + session in one step
- **"Join existing worktree"** checkbox for the multi-agent collaboration case
- Project `···` menu: delete project
- Session `···` menu: restart session, delete session

---

### Step 3: JSONL Streaming + Event Broadcast

**jsonl-tailer sidecar** (runs in each project pod):

- Watches `/data/projects/` for new JSONL files via `inotifywait`
- Tails each active JSONL file, inserts each line into Postgres `events` table
- Maps JSONL files to session IDs by querying `sessions` table (or by matching encoded-cwd paths)
- Extracts `conversationId` from JSONL filename and updates `sessions.conversation_id`

**Server LISTEN handler**:

- On startup, opens persistent Postgres connection with `LISTEN new_event`
- On notification: fetches the new event row, parses with `JSONLParser.parseEvent(line:)`
- Fires `onEvent?(sessionId, event)` → broadcasts to web clients
- Tracks `sessionWorking`, `lastTurnEndTimestamp` per session in memory (fast path for status checks)

```swift
// Server connects to Postgres and listens
let pgConn = try await PostgresConnection.connect(to: postgresURL)
try await pgConn.query("LISTEN new_event")

// On each notification:
for try await notification in pgConn.notifications {
    let payload = try JSONDecoder().decode(EventNotification.self, from: notification.payload)
    let event = try await fetchAndParse(eventId: payload.id)
    onEvent?(payload.sessionId, event)
}
```

**`GET /sessions/:id/events`**: `SELECT data FROM events WHERE session_id = ? ORDER BY id DESC LIMIT 200`. Persisted, survives server restarts, no in-memory accumulation needed.

---

### Step 4: entrypoint.sh

```bash
#!/bin/bash
set -e

# Consume user secrets
if [ -f "/home/node/.user-secrets/id_rsa" ]; then
    mkdir -p "$HOME/.ssh" && chmod 700 "$HOME/.ssh"
    cp /home/node/.user-secrets/id_rsa "$HOME/.ssh/id_rsa"
    chmod 600 "$HOME/.ssh/id_rsa"
    ssh-keyscan github.com >> "$HOME/.ssh/known_hosts" 2>/dev/null || true
fi
[ -f "/home/node/.user-secrets/.gitconfig" ] && \
    cp /home/node/.user-secrets/.gitconfig "$HOME/.gitconfig"
[ -f "/home/node/.user-secrets/oauth-token" ] && \
    export CLAUDE_CODE_OAUTH_TOKEN=$(cat /home/node/.user-secrets/oauth-token)

# Seed user-level Claude config from ConfigMap (copy, not symlink — keeps ~/.claude writable)
# emptyDir is fresh each boot, so always copy from seed
for f in CLAUDE.md settings.json; do
    [ -f "/home/node/.claude-seed/$f" ] && \
        cp "/home/node/.claude-seed/$f" "$HOME/.claude/$f"
done
for d in commands skills; do
    [ -d "/home/node/.claude-seed/$d" ] && \
        cp -r "/home/node/.claude-seed/$d" "$HOME/.claude/$d"
done

# Link JSONL projects dir to PVC (history persists across restarts)
mkdir -p /data/projects
ln -sf /data/projects "$HOME/.claude/projects"

# Skip onboarding
echo '{"hasCompletedOnboarding":true,"bypassPermissions":true}' > "$HOME/.claude.json"

# Git setup (bare repo only — worktrees are created by the server per session)
if [ -n "$GIT_URL" ]; then
    if [ ! -d "/data/repo/HEAD" ]; then
        git clone --bare "$GIT_URL" /data/repo
    else
        git -C /data/repo fetch --all --prune || true
    fi
    mkdir -p /data/worktrees
fi

# Shared memory across worktrees — symlink each worktree's memory dir to /data/shared-memory/
mkdir -p /data/shared-memory
(while true; do
    for dir in "$HOME/.claude/projects"/*/; do
        [ -d "$dir" ] && [ ! -L "${dir}memory" ] && {
            rm -rf "${dir}memory"
            ln -s /data/shared-memory "${dir}memory"
            echo "[memory-sync] Linked ${dir}memory → /data/shared-memory/"
        }
    done
    sleep 5
done) &

# Wait for dockerd
while [ ! -S /var/run/docker.sock ]; do sleep 0.5; done

# Start tmux server
tmux new-session -d -s main -x 220 -y 50 2>/dev/null || true

# Relaunch sessions (query server API, which reads from Postgres)
echo "[entrypoint] Querying server for sessions to relaunch..."
RETRIES=0
while [ $RETRIES -lt 30 ]; do
    SESSIONS=$(curl -sf "http://mclaude-server:8377/internal/sessions?projectId=$PROJECT_ID" 2>/dev/null) && break
    RETRIES=$((RETRIES + 1))
    sleep 2
done

if [ -n "$SESSIONS" ] && [ "$SESSIONS" != "[]" ]; then
    # Re-create worktrees (they persist on PVC, but re-add if pruned)
    echo "$SESSIONS" | jq -r '.[].worktree' | sort -u | while IFS= read -r wt; do
        [ -z "$wt" ] && continue
        if [ ! -d "/data/worktrees/$wt/.git" ]; then
            git -C /data/repo worktree add "/data/worktrees/$wt" "$wt" 2>/dev/null || true
            echo "[entrypoint] Re-created worktree $wt"
        fi
    done

    # Relaunch sessions
    echo "$SESSIONS" | jq -c '.[]' | while IFS= read -r session; do
        SESSION_ID=$(echo "$session" | jq -r '.id')
        SESSION_CWD=$(echo "$session" | jq -r '.cwd')
        CONVERSATION_ID=$(echo "$session" | jq -r '.conversation_id // empty')
        if [ -n "$CONVERSATION_ID" ]; then
            RESUME_FLAG="--resume $CONVERSATION_ID"
        else
            RESUME_FLAG="--continue"
        fi
        tmux new-window -t main -c "$SESSION_CWD" -n "$SESSION_ID" \
            "claude --dangerously-skip-permissions $RESUME_FLAG" 2>/dev/null || true
        echo "[entrypoint] Relaunched $SESSION_ID in $SESSION_CWD (conversation: ${CONVERSATION_ID:-unknown})"
    done
fi

# Keep alive: exit when tmux server exits (triggers restart via Deployment)
tmux wait-for -L main_done 2>/dev/null || while tmux has-session -t main 2>/dev/null; do
    sleep 10
done
```

---

### Step 5: User Resources (Namespace + Secret + ConfigMap + RBAC)

Applied by the **mclaude-controller** during provisioning.

**ensureUserNamespace(userId:)** — `kubectl apply` namespace `mclaude-{userId}`.

**ensureUserResources(userId:, oauthToken:, sshKey:, gitConfig:)** — idempotent `kubectl apply`:

```yaml
# Secret: user-secrets
apiVersion: v1
kind: Secret
metadata:
  name: user-secrets
  namespace: mclaude-{userId}
stringData:
  oauth-token: "{oauthToken}"
  tunnel-token: "{tunnelToken}"
  postgres-password: "{generated}"
  id_rsa: "{sshKey}"
  .gitconfig: "{gitConfig}"
---
# ConfigMap: user-config (seed — config-sync sidecar keeps it updated)
apiVersion: v1
kind: ConfigMap
metadata:
  name: user-config
  namespace: mclaude-{userId}
data:
  CLAUDE.md: "{contents of host ~/.claude/CLAUDE.md}"
  settings.json: "{contents of host ~/.claude/settings.json}"
  # commands and skills entries added as individual keys
---
# ServiceAccount for server + project pods
apiVersion: v1
kind: ServiceAccount
metadata:
  name: mclaude-sa
  namespace: mclaude-{userId}
---
# Role: server + config-sync need namespace-local access
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: mclaude-role
  namespace: mclaude-{userId}
rules:
  - apiGroups: [""]
    resources: ["configmaps"]
    resourceNames: ["user-config"]
    verbs: ["get", "watch", "patch"]
  - apiGroups: [""]
    resources: ["pods", "pods/exec"]
    verbs: ["get", "list", "create", "delete"]
  - apiGroups: [""]
    resources: ["persistentvolumeclaims"]
    verbs: ["get", "list", "create", "delete"]
  - apiGroups: ["apps"]
    resources: ["deployments", "deployments/scale"]
    verbs: ["get", "list", "create", "update", "patch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: mclaude-role
  namespace: mclaude-{userId}
subjects:
  - kind: ServiceAccount
    name: mclaude-sa
roleRef:
  kind: Role
  name: mclaude-role
  apiGroup: rbac.authorization.k8s.io
---
# Service: project pod entrypoints call server internal API for session recovery
apiVersion: v1
kind: Service
metadata:
  name: mclaude-server
  namespace: mclaude-{userId}
spec:
  selector:
    app: mclaude-server
  ports:
    - port: 8377
      targetPort: 8377
```

### Step 6: mclaude-controller

A Deployment in `mclaude-system` namespace. Exposes a ClusterIP Service for internal API calls.

**Endpoints**:

| Method | Path | Action |
|--------|------|--------|
| `POST` | `/provision/{userId}` | Create namespace + apply all user resources + deploy server |
| `POST` | `/scale/{userId}/server` | Scale server Deployment (0 or 1) |
| `DELETE` | `/namespace/{userId}` | Tear down entire namespace (destructive) |
| `GET` | `/status/{userId}` | Check if namespace + server exist and are healthy |

**ClusterRole** (applied at install time):

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: mclaude-controller
rules:
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["get", "list", "create", "delete"]
  - apiGroups: [""]
    resources: ["secrets", "configmaps", "serviceaccounts"]
    verbs: ["get", "list", "create", "update", "delete"]
  - apiGroups: ["apps"]
    resources: ["deployments", "deployments/scale"]
    verbs: ["get", "list", "create", "update", "patch", "delete"]
  - apiGroups: ["rbac.authorization.k8s.io"]
    resources: ["roles", "rolebindings"]
    verbs: ["get", "list", "create", "update", "delete"]
```

---

## Implementation Order

**Phase 1 — Foundation:**

1. **Step 0** — set up HTTPS proxy if cluster egress is blocked (unblocks all K8s work)
2. **Auth** — local user system (JWT), login endpoint on controller
3. **Step 6** — mclaude-controller (provisioning service in mclaude-system, ClusterRole, NetworkPolicies)
4. **Step 5** — user resource manifests (namespace, Secret, ConfigMap, SA, Role, RoleBinding, Postgres, nix-store PVC, server Deployment)
5. **Postgres + migrations** — namespace Postgres Deployment, dbmate init container, initial schema

**Phase 2 — Core:**
6. **Step 4** — session image (Dockerfile with Claude CLI, Nix, tmux, git, jq, zsh, pkg shim, guard hooks, managed CLAUDE.md at `/etc/claude-code/`)
7. **Entrypoint** — config seed, `--resume` support, memory-sharing loop, platform + user hooks, registry mirror config
8. **Server tunnel** — server connects to relay as `k8s~{userId}`, relay routes to it
9. **Step 1** — project Deployment + TmuxMonitor adapter + config-sync sidecar + jsonl-tailer sidecar
10. **Step 2** — createProject / createSession (with worktree creation) / deleteSession (with worktree cleanup) / restartSession

**Phase 3 — Streaming + Lifecycle:**
11. **Step 3** — jsonl-tailer (INSERT into Postgres) + server LISTEN/NOTIFY handler + event broadcast
12. **Server recovery** — query Postgres on startup, resume LISTEN, ConfigMap watch
13. **Project + server lifecycle** — idle timers, scale to 0/1, web UI `···` overflow menu (restart, delete)
14. **Event retention** — archive job (session end → blob storage), Postgres pruning, pg_dump CronJob

**Phase 4 — Hardening:**
15. **Observability** — OTEL metrics/logs export, FinOps dashboard
16. **Registry mirrors** — consume `registry-mirrors` ConfigMap, renderers in session image
17. **Network policies** — deny cross-namespace, allow mclaude-system
18. **Write future plans** — all 8 plans in acceptance criteria

---

## Authentication

Local users for now. Entra (Azure AD) SSO is the target but requires corporate Entra admin approval (blocked).

**Local user system**:

- Controller manages a `users` table (or ConfigMap) with userId, password hash, display name
- Web UI has a login page — username/password → controller issues a JWT
- JWT included in WebSocket connection to relay and all API calls
- Relay validates JWT, extracts userId, routes to the right namespace

**Future**: swap local auth for Entra OIDC. The JWT claims stay the same — just the issuer changes. All downstream components (relay routing, controller provisioning, namespace naming) key off userId regardless of auth backend.

## Observability

OTEL stack is already on the cluster. All components export to it.

**Metrics** (Prometheus/OTEL):

- Per-namespace: active sessions, events/sec, Postgres connections, PVC usage %
- Per-project: pod status, session count, worktree count
- System: relay tunnel count, controller provisioning latency, Squid proxy request rate
- FinOps: compute cost per user (CPU/memory request × time), storage cost per user (PVC GiB)

**Logging** (OTEL/Loki):

- All containers log to stdout → collected by cluster-level log agent
- Structured JSON logs with `namespace`, `projectId`, `sessionId` labels
- Entrypoint logs prefixed with `[entrypoint]`, `[memory-sync]`, etc. (already in the plan)

**FinOps monitoring (day 1)**:

- Dashboard per user: compute hours, storage GiB, estimated monthly cost
- Alert when a user exceeds a cost threshold
- Idle project detection — flag projects with no sessions for >7 days but PVC still allocated

**Cost estimate per user** (2 active projects):

| Resource | Spec | Monthly cost |
|----------|------|-------------|
| Server pod | 50m CPU, 64Mi | ~$2 |
| Postgres pod | 50m CPU, 64Mi + 1Gi PVC | ~$3 |
| Project pod ×2 | 350m CPU, 900Mi each | ~$12 |
| Project PVC ×2 | 20Gi managed-csi-premium each | ~$6 |
| Nix store | 20Gi azurefile-csi | ~$1.20 |
| Blob storage (events) | ~1Gi long-term | ~$0.02 |
| **Total** | | **~$24/month** |

## Event Retention (two-tier storage)

Postgres is a **hot cache** for recent events. Azure Blob Storage is **cold storage** for long-term retention (7 year policy).

**Hot tier (Postgres)**: events from active and recently-active sessions. Fast queries for the web UI (`GET /sessions/:id/events`). Retained for 30 days or until session is explicitly archived.

**Cold tier (Azure Blob Storage)**: when a session ends (user deletes it or it's idle for >24h), all its events are batch-exported to blob storage as a gzipped JSONL file, then pruned from Postgres.

```
Blob path: mclaude/{userId}/{projectId}/{sessionId}/events.jsonl.gz
```

**Historical queries**: `GET /sessions/:id/events?source=archive` → server fetches from blob, decompresses, returns. Slower (~500ms) but works for reviewing past conversations.

**Retention policy**: 7 years in blob storage (Azure lifecycle management policy). Postgres events auto-pruned after 30 days by a server-side cron job:

```sql
DELETE FROM events
WHERE session_id NOT IN (SELECT id FROM sessions)
   OR created_at < now() - interval '30 days';
```

**Server-side archive job** (runs on session delete or idle timeout):

```swift
func archiveSession(id: String) async {
    let events = try await db.query("SELECT data FROM events WHERE session_id = $1 ORDER BY id", [id])
    let gzipped = gzip(events.map { $0.data.jsonString }.joined(separator: "\n"))
    try await blobStorage.upload(path: "\(userId)/\(projectId)/\(id)/events.jsonl.gz", data: gzipped)
    try await db.query("DELETE FROM events WHERE session_id = $1", [id])
}
```

## Network Policies

Restrict cross-namespace traffic. Pods in `mclaude-alice` should not reach Postgres in `mclaude-bob`.

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: deny-cross-namespace
  namespace: mclaude-{userId}
spec:
  podSelector: {}
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector: {}          # allow within namespace
    - from:
        - namespaceSelector:
            matchLabels:
              name: mclaude-system  # allow from relay/controller
```

Applied by the controller during provisioning.

## Image Build Pipeline

All images are open source. CI/CD via GitHub Actions (on GHES).

**Images**:

| Image | Repo | Contents |
|-------|------|----------|
| `mclaude-session` | `mclaude/session` | Claude Code CLI, Nix, tmux, git, jq, zsh, `pkg` shim, guard hooks |
| `mclaude-server` | `mclaude/server` | Swift binary, kubectl |
| `mclaude-jsonl-tailer` | `mclaude/jsonl-tailer` | Lightweight Go/Python binary, Postgres client, inotify |
| `mclaude-controller` | `mclaude/controller` | Go/Python binary, kubectl, dbmate |

**Build triggers**: push to main → build → push to internal container registry (Artifactory). Tagged releases for production.

## Web UI

The current relay serves a single `index.html`. The K8s integration requires: project cards, session wizard, overflow menus, secrets management, onboarding flow, FinOps dashboard. This is beyond what a single HTML file can support.

**Decision needed**: refactor the relay's static frontend into a proper SPA. Framework TBD (React, Solid, Svelte). The relay continues to serve the built static assets — no separate frontend deployment. This is a separate task from the K8s backend work.

## Postgres Backup

Regular `pg_dump` via a CronJob in each user namespace:

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: pg-backup
  namespace: mclaude-{userId}
spec:
  schedule: "0 2 * * *"   # daily at 2am
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - name: backup
              image: postgres:17-alpine
              command: ["/bin/sh", "-c"]
              args:
                - pg_dump -h postgres -U mclaude mclaude | gzip > /backup/mclaude-$(date +%Y%m%d).sql.gz
              env:
                - name: PGPASSWORD
                  valueFrom:
                    secretKeyRef:
                      name: user-secrets
                      key: postgres-password
              volumeMounts:
                - name: backup
                  mountPath: /backup
          volumes:
            - name: backup
              persistentVolumeClaim:
                claimName: postgres-backup
          restartPolicy: OnFailure
```

Backups retained for 30 days. Upload to blob storage for longer retention.

## Open Questions

- **`hostUsers: false` on AKS**: omitted from pod manifests for now. Needs a test pod to confirm it works on this cluster before adding it.
- **Config change notification UX**: when user config changes trigger session restarts, should the UI ask for confirmation first (user might be mid-conversation) or restart automatically with a toast notification? Leaning toward auto-restart with notification since `--resume` preserves the conversation.
- **Secrets management UI**: users need a web UI to add/update/remove credential files in the K8s Secret. Values never displayed, just names + update/delete actions.
- **PVC resize**: 20Gi default. Large repos may need more. `managed-csi-premium` supports online expansion — add a monitoring alert or admin endpoint.
- **GHES search in "Clone repo" dialog**: server calls GHES API using SSH key or a separate PAT from user-secrets. Details TBD.
- **OpenBao seed scripts**: community repo for tool-specific credential seed scripts. Contract: read from Bao, write to `$HOME`, exit 0 if secret missing.
- **Web UI framework**: React vs Solid vs Svelte for the SPA refactor. Separate design task.

### Artifactory / Registry Configuration

Enterprise deployments pull all dependencies through Artifactory — Docker base images, npm packages, pip, Go modules, Nix packages, etc. This must be configurable so the same images work on both personal laptop (public registries) and enterprise (Artifactory mirrors).

**Build-time**: Docker build args for base image registry:

```dockerfile
ARG BASE_REGISTRY=docker.io
FROM ${BASE_REGISTRY}/library/node:20-slim
```

**Runtime**: entrypoint runs **platform hooks** that configure each package manager's registry. Hooks read from env vars injected via a `registry-config` ConfigMap in `mclaude-system`. On personal laptop, env vars are empty → hooks skip → tools use public defaults.

Entrypoint hook order:

1. `/etc/mclaude/hooks.d/*.sh` — platform hooks (registry config, same for all users)
2. User hooks (OpenBao seed scripts, user-specific credentials)

**Registry ConfigMap contract**: mclaude consumes a `registry-mirrors` ConfigMap published by a controller in `t1v0-infra`. The schema is defined here — mclaude is the consumer and needs these fields for its renderers to work.

**Expected schema** (`mirrors.json` key in ConfigMap):

```json
[
  {
    "origin": "https://registry.npmjs.org",
    "mirror": "https://npm.artifactory.example.com/",
    "type": "npm",
    "auth": {
      "secretRef": {"name": "artifactory-creds", "key": "token"}
    },
    "tls": {
      "caBundle": "corporate-ca"
    },
    "scopes": ["@myorg"]
  },
  {
    "origin": "https://pypi.org/simple/",
    "mirror": "https://pypi.artifactory.example.com/simple/",
    "type": "pypi",
    "auth": {
      "secretRef": {"name": "artifactory-creds", "key": "token"}
    }
  }
]
```

Auth is just a K8s Secret reference. How the Secret is populated (static PAT, token exchanger, operator, imagePullSecret) is outside mclaude's scope — that's t1v0-infra infrastructure. mclaude renderers just read the Secret.

| Field | Required | Used by renderer to |
|-------|----------|-------------------|
| `origin` | Yes | Match against the tool's default registry URL |
| `mirror` | Yes | Substitute into tool config |
| `type` | Yes | Select which renderer to invoke (`npm` → write `.npmrc`, `pypi` → write `pip.conf`, etc.) |
| `auth.secretRef` | No | K8s Secret name + key containing the credential. How the Secret is populated is infra's concern (token exchanger, operator, manual). |
| `tls.caBundle` | No | Configure custom CA for tools that need explicit cert config |
| `tls.insecure` | No (default: `false`) | Skip TLS verification (adds `strict-ssl=false` in npm, `trusted-host` in pip, etc.) |
| `scopes` | No | npm/cargo scoped registries — apply mirror only to specific package scopes |

Renderers are in the session image at `/etc/mclaude/hooks.d/`. Each reads `mirrors.json`, filters by `type`, and writes the tool-specific config. Example npm renderer:

```bash
#!/bin/bash
# /etc/mclaude/hooks.d/npm-registry.sh
MIRRORS_FILE="/etc/mclaude/mirrors.json"
[ ! -f "$MIRRORS_FILE" ] && exit 0

jq -c '.[] | select(.type == "npm")' "$MIRRORS_FILE" | while IFS= read -r entry; do
    MIRROR=$(echo "$entry" | jq -r '.mirror')
    INSECURE=$(echo "$entry" | jq -r '.tls.insecure // false')
    SECRET_NAME=$(echo "$entry" | jq -r '.auth.secretRef.name // empty')
    SECRET_KEY=$(echo "$entry" | jq -r '.auth.secretRef.key // empty')

    echo "registry=$MIRROR" >> "$HOME/.npmrc"
    [ "$INSECURE" = "true" ] && echo "strict-ssl=false" >> "$HOME/.npmrc"

    # Auth: read token from K8s Secret if configured
    SECRET_KEY=$(echo "$entry" | jq -r '.auth.secretRef.key // empty')
    if [ -n "$SECRET_KEY" ]; then
        TOKEN=$(cat "/etc/mclaude/secrets/$SECRET_KEY" 2>/dev/null)
        [ -n "$TOKEN" ] && echo "//${MIRROR#https://}:_authToken=$TOKEN" >> "$HOME/.npmrc"
    fi

    # Scoped registries
    for scope in $(echo "$entry" | jq -r '.scopes[]? // empty'); do
        echo "$scope:registry=$MIRROR" >> "$HOME/.npmrc"
    done
done
```

See `t1v0-infra/docs/plan-registry-mirror-controller.md` for the producer side (Artifactory API discovery, ConfigMap generation, reconciliation).

Each platform hook is a small script:

```bash
#!/bin/bash
# /etc/mclaude/hooks.d/npm-registry.sh
[ -z "$NPM_REGISTRY" ] && exit 0
echo "registry=$NPM_REGISTRY" >> "$HOME/.npmrc"
```

Platform hooks are baked into the session image or mounted from a ConfigMap. Adding a new tool = adding a 3-line hook script.

---

## Verification

**Project lifecycle:**

1. `POST /projects` with `{"gitUrl":"...","name":"myapp"}` → project Deployment + PVC created, bare repo cloned
2. `GET /projects` → lists project with status Running, 0 sessions

**Session + worktree lifecycle:**
3. `POST /sessions` with `{"projectId":"myapp","branch":"main"}` → worktree created at `/data/worktrees/main/`, tmux window opened
4. Send a message → web app Events tab shows live events
5. `POST /sessions` with `{"projectId":"myapp","branch":"feature/auth"}` → second worktree, second session, different branch
6. `POST /sessions/{id}/restart` → tmux window killed + relaunched with `--resume`, events continue appending
7. `DELETE /sessions/{id}` → tmux window killed, worktree cleaned up if last session on that branch

**Shared worktree (multi-agent collaboration):**
8. `POST /sessions` with `{"projectId":"myapp","branch":"main","joinWorktree":true}` → joins existing `main` worktree, both sessions share files

**Memory sharing:**
9. Claude learns feedback in session on `main` → memory saved to `/data/shared-memory/`
10. Create new session on `feature/auth` → same feedback is available (symlinked memory dir)

**User config:**
11. `claude mcp add --scope user` inside a session → config-sync patches ConfigMap within seconds

**Resilience:**
12. `kubectl delete pod project-myapp-xxxxx` → Deployment recreates pod, worktrees re-created, sessions relaunch with `--resume {conversationId}`
13. Restart mclaude-server pod → `recover()` rebuilds all session state, web clients reconnect seamlessly
14. Delete all sessions in a project → Deployment scales to 0 after 30min idle, PVC persists
15. Create a new session for the same project after idle → Deployment scales to 1, mounts existing PVC

**Provisioning:**
16. First-time user → relay calls controller `/provision/{userId}` → namespace, RBAC, server Deployment created → server connects via tunnel
17. Server idle timeout → controller scales server to 0. Next request → controller scales back to 1.

---

## Acceptance Criteria

This plan is complete when all verification steps pass AND the following future work plans have been written (as separate `docs/plan-*.md` files in the repo):

| Future plan | Scope | Why it's separate |
|------------|-------|-------------------|
| `plan-entra-sso.md` | Replace local user auth with Entra OIDC | Blocked on corporate Entra admin approval |
| `plan-web-ui-refactor.md` | SPA refactor of relay frontend (framework selection, component design, routing) | Design task — needs its own wireframes and tech decisions |
| `plan-openbao-integration.md` | OpenBao deployment, Kubernetes auth, seed script framework, community repo | Separate infra + developer experience workstream |
| `plan-secrets-management-ui.md` | Web UI for managing K8s Secret entries (add/update/remove credentials) | Part of the web UI refactor but scoped independently |
| `plan-laptop-worktrees.md` | Worktree-per-session support on laptop/VM runtime (TmuxMonitor changes, new session dialog) | Parity feature, separate from K8s work |
| `plan-finops-dashboard.md` | Per-user cost tracking dashboard, idle resource alerts, budget thresholds | Depends on OTEL stack + web UI refactor |
| `plan-ghes-repo-browser.md` | GHES API integration for "Clone repo" search/autocomplete in new session dialog | UX feature, needs GHES API exploration |
| `t1v0-infra: plan-registry-mirror-controller.md` | Controller that discovers Artifactory remote repos and publishes a `registry-mirrors` ConfigMap for cluster-wide consumption | Platform concern, lives in t1v0-infra, not mclaude |

Each plan follows the same format as this one: context, architecture, implementation steps, verification criteria. Writing these plans is a deliverable of this project, not a "nice to have."
