# Core Containers Plan

Development of the mclaude container images and application logic. Cloud-agnostic — no deployment-specific details. This plan contains enough architectural context for an agent or developer to build the full system without access to any deployment-specific documentation.

---

## Architecture Overview

mclaude is a platform for running managed Claude Code sessions. Users interact via a web UI. The platform manages Claude Code processes inside containers, streams events back to the browser in real-time, and handles session lifecycle (create, restart, delete, idle teardown).

### System Topology

```
Browser (web UI)
    ↕ WebSocket
mclaude-relay (network proxy, routes by userId)
    ↕ WebSocket tunnel
mclaude-server (one per user namespace, manages sessions)
    ↕ kubectl exec (tmux commands)
    ↕ Postgres LISTEN/NOTIFY (event streaming)
project pods (one per git repo, runs Claude Code in tmux)
    ├── tmux-host       — Claude Code sessions as tmux windows
    ├── jsonl-tailer    — tails JSONL files, INSERTs into Postgres
    ├── config-sync     — watches ~/.claude/, syncs back to K8s ConfigMap
    └── dockerd-rootless — Docker daemon for tool use
```

### Key Concepts

| Concept | What it is | How it maps to infrastructure |
|---------|-----------|------------------------------|
| **User** | A person using the platform | One K8s namespace (`mclaude-{userId}`), one server, one Postgres instance |
| **Project** | A git repo | One K8s Deployment + PVC per repo. Contains bare git repo, worktrees, JSONL history, shared memory. |
| **Worktree** | A branch checkout within a project | A directory on the project PVC (`/data/worktrees/{branch}/`). Default: one worktree per session. |
| **Session** | A Claude Code process | A tmux window inside a project pod. Tracked as a row in Postgres. |

### Data Flow: User Input → Response

```
1. User types message in browser
2. Browser sends via WebSocket → relay → server
3. Server runs: kubectl exec {pod} -- tmux send-keys -t {sessionId} "{message}" Enter
4. Claude Code processes the message, calls Anthropic API, writes JSONL to disk
5. jsonl-tailer (sidecar) detects JSONL write via inotify
6. jsonl-tailer INSERTs event into Postgres
7. Postgres trigger fires: NOTIFY new_event
8. Server receives notification via persistent LISTEN connection
9. Server parses event, broadcasts to browser via WebSocket (through relay)
10. Browser renders the streaming response
```

Latency added by the platform (vs running Claude Code locally): ~10-15ms per token chunk. Imperceptible during streaming since Anthropic API inference dominates (~2-30s).

### Storage Model

| Mount point | Type | Contains | Persists across restarts? | Shared across project pods? |
|------------|------|----------|--------------------------|---------------------------|
| `$HOME` (`/home/node/`) | emptyDir | Dotfiles, credentials, Claude config. Seeded from K8s Secret + ConfigMap on boot. | No — ephemeral by design (credentials stay in K8s Secrets, not on persistent storage) | No |
| `/data/` | PVC (RWO) | Bare git repo, worktrees, JSONL history, shared auto-memory | Yes | No — one PVC per project |
| `/nix/` | PVC (RWX) | Nix package store. Content-addressed, deduplicated. | Yes | Yes — shared across all project pods for this user |

### Session Lifecycle

| State | Trigger | What happens |
|-------|---------|-------------|
| **Create** | User clicks "New session" | Server creates worktree (if needed), opens tmux window with `claude --dangerously-skip-permissions`, INSERTs session row in Postgres |
| **Running** | User sends messages | Input via `tmux send-keys`, output via JSONL → Postgres → LISTEN/NOTIFY → browser |
| **Restart** | User clicks restart in `···` menu | Server kills tmux window, relaunches with `--resume {conversationId}`. Fresh Claude process, same conversation. Picks up new settings/MCP config. |
| **Delete** | User clicks delete in `···` menu | Server kills tmux window, DELETEs session row, cleans up worktree if last session using it |
| **Pod restart** | Crash, rolling update, etc. | Deployment recreates pod. Entrypoint queries server API for sessions to relaunch. Re-creates worktrees, relaunches with `--resume`. |
| **Idle teardown** | No sessions for 30min | Server scales Deployment to 0 replicas. PVC persists. Next session creation scales back to 1. |

### CLAUDE.md Three-Tier System

Claude Code reads CLAUDE.md files at multiple scopes. mclaude controls all three:

| Tier | Location | Controlled by | Can user override? |
|------|----------|--------------|-------------------|
| **Global (managed policy)** | `/etc/claude-code/CLAUDE.md` | Platform (baked into session image) | No — Claude Code enforces managed policy |
| **User** | `~/.claude/CLAUDE.md` | User (synced via ConfigMap + config-sync sidecar) | Yes |
| **Project** | `{worktree}/CLAUDE.md` | Repo (committed to git) | Yes |

The global tier sets platform conventions: paths, shell rules, credential handling, git/worktree conventions, tool installation. Platform hooks (`/etc/claude-code/settings.json`) enforce rules at the Bash tool level — blocking `git checkout`, `apt install`, deletion of critical paths.

### Auto-Memory Sharing

Claude Code stores auto-memories (feedback, project context) per working directory at `~/.claude/projects/{encoded-cwd}/memory/`. Different worktrees have different cwds → separate memories by default.

Since memories (especially feedback like "don't mock the database in tests") should be shared across all branches of the same project, the entrypoint symlinks each worktree's memory directory to a single shared location on the PVC (`/data/shared-memory/`).

### Config Sync

User-scope config (`settings.json`, `CLAUDE.md`) lives in `$HOME` (emptyDir, ephemeral). The config-sync sidecar watches for changes and patches a K8s ConfigMap. On pod restart, the entrypoint re-seeds from the ConfigMap.

When config changes (e.g., `claude mcp add --scope user`), the server detects the ConfigMap change (K8s API watch), re-seeds all project pods, and restarts all sessions with `--resume`. This ensures new MCP servers are picked up.

### Registry Mirror System

Enterprise deployments need package managers (npm, pip, go, nix, cargo) configured to pull from internal mirrors instead of public registries. The session image includes a **platform hooks framework** — shell scripts that run at entrypoint time and configure each tool. Hooks read from a `mirrors.json` file (mounted from a ConfigMap). If the file doesn't exist (personal laptop), hooks skip — tools use public defaults.

**Mirror schema** (consumed by renderers):
```json
[
  {
    "origin": "https://registry.npmjs.org",
    "mirror": "https://npm.internal.example.com/",
    "type": "npm",
    "auth": {
      "secretRef": {"name": "registry-creds", "key": "token"}
    },
    "tls": {
      "caBundle": "corporate-ca"
    },
    "scopes": ["@myorg"]
  }
]
```

| Field | Required | Used by renderer to |
|-------|----------|-------------------|
| `origin` | Yes | Match against the tool's default registry URL |
| `mirror` | Yes | Substitute into tool config |
| `type` | Yes | Select which renderer to invoke (`npm` → `.npmrc`, `pypi` → `pip.conf`, etc.) |
| `auth.secretRef` | No | Look up credential from K8s Secret |
| `tls.caBundle` | No | Configure custom CA |
| `scopes` | No | npm/cargo scoped registries |

### Tool Installation (Nix)

The session image ships with Nix (single-user mode). A `pkg` shim provides a simple CLI (`pkg install nodejs`). The Nix store (`/nix/`) is on a shared PVC (RWX) — install a tool in any project pod, it's available in all pods for this user. Content-addressed caching means packages are downloaded once and deduplicated.

---

## Components

| Image | Language | Role | Communicates with |
|-------|----------|------|------------------|
| `mclaude-session` | Bash + Claude CLI | Session container: tmux, Claude Code, Nix, entrypoint, hooks | Server (via HTTP for session recovery on boot) |
| `mclaude-server` | Swift | Per-user server: project/session CRUD, LISTEN/NOTIFY, relay tunnel, K8s API, config watching | Relay (WebSocket tunnel), Postgres (LISTEN/NOTIFY + queries), K8s API (kubectl exec, Deployments) |
| `mclaude-jsonl-tailer` | Go | Sidecar: tails JSONL files, INSERTs into Postgres | Postgres (INSERT), project PVC (read JSONL files) |
| `mclaude-controller` | Go | Cluster controller: provisions user namespaces, RBAC, Deployments | K8s API (cluster-level operations) |

---

## Local Development

All components are testable locally via docker-compose. No Kubernetes required for core development.

```yaml
# docker-compose.yml
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_DB: mclaude
      POSTGRES_USER: mclaude
      POSTGRES_PASSWORD: dev
    ports: ["5432:5432"]
    volumes:
      - pgdata:/var/lib/postgresql/data

  migrate:
    image: ghcr.io/amacneil/dbmate:latest
    command: ["--url", "postgres://mclaude:dev@postgres:5432/mclaude?sslmode=disable", "--migrations-dir", "/migrations", "up"]
    volumes:
      - ./mclaude-server/migrations:/migrations
    depends_on: [postgres]

  session:
    build: ./mclaude-session
    volumes:
      - ./test-data:/data
      - claude-home:/home/node/.claude
    environment:
      GIT_URL: ""
      PROJECT_ID: test-project
    tty: true

  jsonl-tailer:
    build: ./mclaude-jsonl-tailer
    volumes:
      - ./test-data:/data:ro
    environment:
      POSTGRES_URL: postgres://mclaude:dev@postgres:5432/mclaude
      PROJECT_ID: test-project
    depends_on: [postgres, migrate]

  server:
    build: ./mclaude-server
    ports: ["8377:8377"]
    environment:
      DATABASE_URL: postgres://mclaude:dev@postgres:5432/mclaude
      MODE: local  # skip tunnel connection, skip K8s API — uses local Docker/tmux
    depends_on: [postgres, migrate]

volumes:
  pgdata:
  claude-home:
```

`MODE=local` makes the server work without a relay tunnel or K8s API — uses local Docker/tmux instead of kubectl exec. This is the same mode the laptop server already uses.

---

## 1. mclaude-session

The session container image. Runs Claude Code inside tmux. This is the most complex image — it sets up the entire runtime environment for Claude Code.

### Dockerfile

```dockerfile
FROM node:20-slim

# System deps
RUN apt-get update && apt-get install -y --no-install-recommends \
    tmux git jq curl zsh inotify-tools ca-certificates openssh-client \
    && rm -rf /var/lib/apt/lists/*

# Nix (single-user mode)
RUN curl -L https://nixos.org/nix/install | sh -s -- --no-daemon
ENV PATH="/root/.nix-profile/bin:$PATH"

# Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code

# pkg shim
COPY bin/pkg /usr/local/bin/pkg
RUN chmod +x /usr/local/bin/pkg

# Remove real apt to avoid confusion (agents will try apt install)
RUN mv /usr/bin/apt /usr/bin/apt-real 2>/dev/null || true
RUN mv /usr/bin/apt-get /usr/bin/apt-get-real 2>/dev/null || true

# Managed policy (global CLAUDE.md + settings with hooks — cannot be overridden by users)
COPY etc/claude-code/ /etc/claude-code/

# Platform hooks (registry mirrors, etc. — run at entrypoint time)
COPY hooks.d/ /etc/mclaude/hooks.d/

# Entrypoint
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Non-root user
RUN useradd -m -s /bin/zsh node
USER node
WORKDIR /home/node

ENTRYPOINT ["/entrypoint.sh"]
```

### entrypoint.sh

The entrypoint is the orchestrator for the session container. It runs sequentially on every pod start:

1. Seed credentials from K8s Secret mount
2. Seed user config from ConfigMap mount
3. Run platform hooks (registry mirrors, etc.)
4. Run user hooks (credential seed scripts, etc.)
5. Set up JSONL projects directory on PVC
6. Clone bare git repo (first boot) or fetch updates (subsequent boots)
7. Start background memory-sharing loop (symlinks worktree memories to shared location)
8. Wait for Docker daemon sidecar
9. Start tmux server
10. Query server API for sessions to relaunch (pod restart recovery)
11. Keep alive until tmux exits

```bash
#!/bin/bash
set -e

# --- 1. Secrets (mounted read-only from K8s Secret) ---
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

# --- 2. User config (mounted read-only from K8s ConfigMap, copied to writable $HOME) ---
# $HOME is an emptyDir — fresh each boot, so always copy from seed
for f in CLAUDE.md settings.json; do
    [ -f "/home/node/.claude-seed/$f" ] && \
        cp "/home/node/.claude-seed/$f" "$HOME/.claude/$f"
done
for d in commands skills; do
    [ -d "/home/node/.claude-seed/$d" ] && \
        cp -r "/home/node/.claude-seed/$d" "$HOME/.claude/$d"
done

# --- 3. Platform hooks (registry mirrors, environment setup) ---
# These read from mounted ConfigMaps/env vars. If nothing is mounted, they skip.
if [ -d "/etc/mclaude/hooks.d" ]; then
    for hook in /etc/mclaude/hooks.d/*.sh; do
        [ -x "$hook" ] && source "$hook"
    done
fi

# --- 4. User hooks (credential seed scripts, e.g. OpenBao → .netrc) ---
if [ -d "$HOME/.claude/hooks.d" ]; then
    for hook in "$HOME/.claude/hooks.d"/*.sh; do
        [ -x "$hook" ] && source "$hook"
    done
fi

# --- 5. JSONL projects dir on PVC (conversation history persists across restarts) ---
mkdir -p /data/projects
ln -sf /data/projects "$HOME/.claude/projects"

# --- 6. Skip Claude Code onboarding ---
echo '{"hasCompletedOnboarding":true,"bypassPermissions":true}' > "$HOME/.claude.json"

# --- 7. Git (bare repo only — worktrees are created by server per session) ---
if [ -n "$GIT_URL" ]; then
    if [ ! -d "/data/repo/HEAD" ]; then
        git clone --bare "$GIT_URL" /data/repo
    else
        git -C /data/repo fetch --all --prune || true
    fi
    mkdir -p /data/worktrees
fi

# --- 8. Shared memory across worktrees ---
# Claude Code keys auto-memory by cwd. Different worktrees = different cwds = separate memories.
# This loop symlinks every worktree's memory dir to a single shared location so feedback,
# project context, and references are shared across all branches.
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

# --- 9. Wait for dockerd sidecar (if present) ---
if [ -d "/var/run" ] && [ -n "$DOCKER_HOST" ]; then
    echo "[entrypoint] Waiting for dockerd..."
    while [ ! -S /var/run/docker.sock ]; do sleep 0.5; done
fi

# --- 10. Start tmux server ---
tmux new-session -d -s main -x 220 -y 50 2>/dev/null || true

# --- 11. Relaunch sessions on pod restart (query server API → Postgres) ---
if [ -n "$SERVER_URL" ] && [ -n "$PROJECT_ID" ]; then
    echo "[entrypoint] Querying server for sessions to relaunch..."
    RETRIES=0
    while [ $RETRIES -lt 30 ]; do
        SESSIONS=$(curl -sf "$SERVER_URL/internal/sessions?projectId=$PROJECT_ID" 2>/dev/null) && break
        RETRIES=$((RETRIES + 1))
        sleep 2
    done

    if [ -n "$SESSIONS" ] && [ "$SESSIONS" != "[]" ]; then
        # Re-create worktrees if pruned
        echo "$SESSIONS" | jq -r '.[].worktree' | sort -u | while IFS= read -r wt; do
            [ -z "$wt" ] && continue
            if [ ! -d "/data/worktrees/$wt/.git" ]; then
                git -C /data/repo worktree add "/data/worktrees/$wt" "$wt" 2>/dev/null || true
            fi
        done

        # Relaunch each session with --resume to continue the exact conversation
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
            echo "[entrypoint] Relaunched $SESSION_ID (conversation: ${CONVERSATION_ID:-unknown})"
        done
    fi
fi

# --- 12. Keep alive (exit when tmux exits → Deployment restarts pod) ---
tmux wait-for -L main_done 2>/dev/null || while tmux has-session -t main 2>/dev/null; do
    sleep 10
done
```

### Environment Variables

| Var | Source | Description |
|-----|--------|-------------|
| `GIT_URL` | Deployment env | Git repo URL (bare clone on first boot) |
| `PROJECT_ID` | Deployment env | This project's ID (for server API queries) |
| `SERVER_URL` | Deployment env | Server internal URL (e.g., `http://mclaude-server:8377`) |
| `DOCKER_HOST` | Deployment env | Docker socket path (e.g., `unix:///var/run/docker.sock`) |
| `CLAUDE_CODE_OAUTH_TOKEN` | Secret mount | Anthropic OAuth token (set by entrypoint from Secret) |
| `HTTPS_PROXY` | Deployment env (optional) | HTTP proxy for Anthropic API access (enterprise environments) |

### Volume Mounts

| Mount | Source | Mode | Purpose |
|-------|--------|------|---------|
| `/data/` | Project PVC (RWO) | RW | Bare repo, worktrees, JSONL, shared memory |
| `/nix/` | Shared Nix PVC (RWX) | RW | Nix package store (shared across project pods) |
| `/home/node/.claude/` | emptyDir | RW | Writable user config (ephemeral) |
| `/home/node/.claude-seed/` | ConfigMap | RO | Seed for user config |
| `/home/node/.user-secrets/` | Secret | RO | SSH key, OAuth token, PATs |
| `/var/run/` | emptyDir | RW | Shared docker.sock with dockerd sidecar |
| `/etc/mclaude/mirrors.json` | ConfigMap (optional) | RO | Registry mirror config (enterprise) |
| `/etc/mclaude/secrets/` | Secret (optional) | RO | Registry auth tokens (enterprise) |

### /etc/claude-code/CLAUDE.md (managed policy — cannot be overridden)

```markdown
# MClaude Platform

## Environment
You are running in a managed container. Key paths:
- `/data/repo/` — bare git repo (shared across worktrees)
- `/data/worktrees/{branch}/` — git worktrees (one per branch)
- `/data/shared-memory/` — auto-memory shared across all worktrees
- `$HOME` is ephemeral — rebuilt on every container restart

## Shell Conventions
- Never write secrets directly to `~/.zshrc` — it is synced to a ConfigMap.
- Use `~/.zshrc.local` for session-scoped shell additions (ephemeral).
- Secrets are available as env vars via `~/.env.secrets` (sourced by .zshrc).

## Credentials
- Do not hardcode tokens, passwords, or keys in any persisted file
  (CLAUDE.md, settings.json, .zshrc).
- Credentials in `$HOME` are ephemeral — they work for this session
  but are lost on restart.

## Git
- Worktrees are managed by the platform. To work on a new branch,
  ask the user to create a new session — do not use `git checkout`
  or `git switch` (these are blocked by platform hooks).
- The bare repo is at `/data/repo/`. Do not modify it directly.
- Push/pull works normally within a worktree.

## Tool Installation
- Use `pkg install <package>` to install tools.
- Do not use `apt install` or `apt-get` — they are not available.
- Tools are cached and shared across all sessions automatically.

## Docker
- Docker is available via the dockerd-rootless sidecar.
- `DOCKER_HOST` is pre-configured.
```

### /etc/claude-code/settings.json (managed hooks)

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

### /etc/claude-code/hooks/guard.sh

Platform hooks enforce behavior at the Bash tool execution level. They intercept both agent-initiated commands AND user `!` commands. This is stricter than CLAUDE.md instructions, which agents can choose to ignore.

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

### bin/pkg (Nix shim)

```bash
#!/bin/bash
case "$1" in
    install)
        shift
        for pkg in "$@"; do
            echo "[pkg] Installing $pkg..."
            nix profile install "nixpkgs#$pkg"
        done
        ;;
    remove)
        shift
        for pkg in "$@"; do
            nix profile remove "nixpkgs#$pkg"
        done
        ;;
    search)
        shift
        nix search nixpkgs "$1"
        ;;
    list)
        nix profile list
        ;;
    *)
        echo "pkg — package manager (powered by Nix)"
        echo "  pkg install <package>    Install a package"
        echo "  pkg remove <package>     Remove a package"
        echo "  pkg search <query>       Search packages"
        echo "  pkg list                 List installed"
        ;;
esac
```

### hooks.d/registry-mirrors.sh (platform hook — enterprise registry config)

Reads `mirrors.json` (mounted from ConfigMap in enterprise environments). If the file doesn't exist (personal laptop), exits immediately — tools use public defaults.

```bash
#!/bin/bash
MIRRORS_FILE="/etc/mclaude/mirrors.json"
[ ! -f "$MIRRORS_FILE" ] && exit 0

# npm
jq -c '.[] | select(.type == "npm")' "$MIRRORS_FILE" | while IFS= read -r entry; do
    MIRROR=$(echo "$entry" | jq -r '.mirror')
    INSECURE=$(echo "$entry" | jq -r '.tls.insecure // false')
    echo "registry=$MIRROR" >> "$HOME/.npmrc"
    [ "$INSECURE" = "true" ] && echo "strict-ssl=false" >> "$HOME/.npmrc"
    SECRET_KEY=$(echo "$entry" | jq -r '.auth.secretRef.key // empty')
    if [ -n "$SECRET_KEY" ]; then
        TOKEN=$(cat "/etc/mclaude/secrets/$SECRET_KEY" 2>/dev/null)
        [ -n "$TOKEN" ] && echo "//${MIRROR#https://}:_authToken=$TOKEN" >> "$HOME/.npmrc"
    fi
    for scope in $(echo "$entry" | jq -r '.scopes[]? // empty'); do
        echo "$scope:registry=$MIRROR" >> "$HOME/.npmrc"
    done
done

# pip
jq -c '.[] | select(.type == "pypi")' "$MIRRORS_FILE" | while IFS= read -r entry; do
    MIRROR=$(echo "$entry" | jq -r '.mirror')
    mkdir -p "$HOME/.config/pip"
    echo -e "[global]\nindex-url = $MIRROR" > "$HOME/.config/pip/pip.conf"
done

# go
jq -c '.[] | select(.type == "go")' "$MIRRORS_FILE" | while IFS= read -r entry; do
    MIRROR=$(echo "$entry" | jq -r '.mirror')
    export GOPROXY="$MIRROR,direct"
    echo "export GOPROXY=$MIRROR,direct" >> "$HOME/.zshrc"
done

# nix
jq -c '.[] | select(.type == "nix")' "$MIRRORS_FILE" | while IFS= read -r entry; do
    MIRROR=$(echo "$entry" | jq -r '.mirror')
    mkdir -p "$HOME/.config/nix"
    echo "substituters = $MIRROR https://cache.nixos.org" >> "$HOME/.config/nix/nix.conf"
done
```

---

## 2. mclaude-server

The per-user server. One instance per user namespace. Manages projects, sessions, worktrees. Connects to the relay via WebSocket tunnel. Receives real-time events from Postgres LISTEN/NOTIFY.

### Modes

The server runs in two modes:

| Mode | How | Used when |
|------|-----|-----------|
| `local` | Manages sessions via local tmux (no K8s API) | Laptop development, docker-compose |
| `k8s` | Manages sessions via `kubectl exec` into project pods | Production K8s deployment |

Mode is set via `MODE` env var. The API surface is identical — only the session management backend differs.

### Key changes from current server

The server already exists (`mclaude-server/`). The K8s mode is a rewrite of `K8sSessionManager.swift`:

**Current**: creates bare pods, polls JSONL via kubectl exec, tracks state in memory.
**New**: creates Deployments, state in Postgres, events via LISTEN/NOTIFY, sessions via kubectl exec tmux.

### New/modified source files

| File | Change |
|------|--------|
| `Sources/K8sSessionManager.swift` | Full rewrite: Deployment-based project management, Postgres-backed session CRUD, worktree creation via kubectl exec |
| `Sources/PostgresClient.swift` | New: connection pool, LISTEN/NOTIFY handler, query helpers |
| `Sources/APIServer.swift` | New endpoints: `/projects` CRUD, `/sessions` CRUD, `/sessions/:id/restart`, `/internal/sessions` |
| `Sources/ConfigWatcher.swift` | New: watches `user-config` ConfigMap via K8s API, triggers cross-pod config propagation + session restarts |
| `Sources/IdleManager.swift` | New: per-project idle timers, scales Deployments to 0/1 |
| `Sources/ArchiveManager.swift` | New: archives session events to blob storage on session delete/idle, prunes old events from Postgres |
| `Sources/main.swift` | Wire Postgres LISTEN → event broadcast, add config watcher, recovery on startup |

### Postgres LISTEN/NOTIFY

The server holds a persistent Postgres connection with `LISTEN new_event`. When the jsonl-tailer INSERTs an event, a trigger fires `NOTIFY new_event` with the event ID. The server fetches the event, parses it, and broadcasts to web clients.

```swift
// On startup
let pgConn = try await PostgresConnection.connect(to: databaseURL)
try await pgConn.query("LISTEN new_event")

// Event loop — receives notifications in real-time
for try await notification in pgConn.notifications {
    let payload = try JSONDecoder().decode(EventNotification.self, from: notification.payload)
    let row = try await db.query("SELECT data FROM events WHERE id = $1", [payload.id])
    let event = JSONLParser.parseEvent(line: row.data)
    onEvent?(payload.sessionId, event)
}
```

### Recovery on startup

No enumeration dance. Just query Postgres:

```swift
func recover() async {
    // All session state is in Postgres — one query
    let sessions = try await db.query("""
        SELECT s.*, p.name as project_name, p.git_url
        FROM sessions s JOIN projects p ON s.project_id = p.id
    """)
    for session in sessions {
        // Verify the project pod is Running
        // If not, scale its Deployment back to 1
    }
    // Start LISTEN for new events
    // Start ConfigMap watcher
}
```

### Config change propagation

The server watches the `user-config` ConfigMap via K8s API watch. When it changes (e.g., user ran `claude mcp add` in one pod, config-sync patched the ConfigMap):

1. For each running project pod: `kubectl exec` to re-copy config from the ConfigMap mount to `$HOME/.claude/`
2. Restart all sessions with `--resume` (picks up new MCP config)
3. Notify web UI: "Settings changed, sessions restarting..."

Uses a diff check (hash comparison) to avoid feedback loops between config-sync and the server.

### API endpoints

| Method | Path | Auth | Action |
|--------|------|------|--------|
| `POST` | `/projects` | JWT | Create project (gitUrl, name) → K8s Deployment + PVC |
| `GET` | `/projects` | JWT | List projects (pod status, session count, worktrees) |
| `DELETE` | `/projects/:id` | JWT | Scale to 0 (PVC retained unless `?purge=true`) |
| `POST` | `/sessions` | JWT | Create session (projectId, branch, cwd, joinWorktree) → worktree + tmux window |
| `DELETE` | `/sessions/:id` | JWT | Kill tmux window, clean up worktree if last session, archive events |
| `POST` | `/sessions/:id/restart` | JWT | Kill + relaunch with `--resume {conversationId}` |
| `GET` | `/sessions/:id/events` | JWT | `SELECT data FROM events WHERE session_id = ? ORDER BY id DESC LIMIT 200` |
| `GET` | `/internal/sessions` | Internal | Returns sessions for a projectId (used by entrypoint on pod restart) |

### Environment Variables

| Var | Description |
|-----|-------------|
| `DATABASE_URL` | Postgres connection string |
| `MODE` | `local` or `k8s` |
| `RELAY_URL` | WebSocket URL for relay tunnel (K8s mode) |
| `TUNNEL_TOKEN` | Auth token for relay tunnel |
| `NAMESPACE` | K8s namespace (K8s mode, from downward API) |
| `USER_ID` | User identifier |

---

## 3. mclaude-jsonl-tailer

Sidecar that runs in each project pod. Tails Claude Code's JSONL log files and inserts events into Postgres. This is the bridge between Claude Code (which writes to the filesystem) and the server (which reads from Postgres).

### How Claude Code stores JSONL

Claude Code writes conversation logs to:
```
~/.claude/projects/{encoded-cwd}/{session-uuid}/subagents/agent-{id}.jsonl
```

Where `{encoded-cwd}` is the working directory with `/` replaced by `-` (e.g., `/data/worktrees/main` → `-data-worktrees-main`).

In our pod, `~/.claude/projects/` is symlinked to `/data/projects/` on the PVC. So the tailer watches `/data/projects/`.

### Behavior

1. On startup, query Postgres `sessions` table for active sessions in this project
2. Watch `/data/projects/` for new directories and JSONL files via `inotifywait`
3. For each JSONL file: `tail -F`, parse each new line, `INSERT INTO events`
4. Map JSONL files to session IDs by matching the encoded-cwd path against session records
5. Extract `conversationId` (the session UUID directory name) and `UPDATE sessions SET conversation_id = $1`
6. Reconnect to Postgres on connection loss with exponential backoff

### Dockerfile

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o /jsonl-tailer .

FROM alpine:3.20
RUN apk add --no-cache inotify-tools
COPY --from=builder /jsonl-tailer /usr/local/bin/jsonl-tailer
ENTRYPOINT ["jsonl-tailer"]
```

### Environment Variables

| Var | Description |
|-----|-------------|
| `POSTGRES_URL` | Postgres connection string |
| `PROJECT_ID` | This project's ID (for filtering sessions and tagging events) |
| `DATA_DIR` | Path to project data (default: `/data`) |

### Volume Mounts

| Mount | Mode | Purpose |
|-------|------|---------|
| `/data/` | RO | Read JSONL files from project PVC |

---

## 4. mclaude-controller

Cluster-level controller. Runs in `mclaude-system` namespace. Provisions user namespaces with all required resources. Has ClusterRole for cross-namespace operations.

### What it creates during provisioning

When `/provision/{userId}` is called:

1. **Namespace** `mclaude-{userId}`
2. **Secret** `user-secrets` — OAuth token, SSH key, git config, postgres password (from request body)
3. **ConfigMap** `user-config` — CLAUDE.md, settings.json (from request body)
4. **ConfigMap** `db-migrations` — embedded SQL migration files
5. **ServiceAccount** `mclaude-sa`
6. **Role** `mclaude-role` — namespace-scoped RBAC (pods, exec, Deployments, PVCs, ConfigMaps, Secrets)
7. **RoleBinding** `mclaude-role`
8. **Postgres Deployment** + Service + PVC (1Gi)
9. **Server Deployment** + Service (port 8377)
10. **NetworkPolicy** `deny-cross-namespace` — blocks traffic from other user namespaces
11. **Nix store PVC** (RWX, shared across project pods)

### Endpoints

| Method | Path | Action |
|--------|------|--------|
| `POST` | `/provision/{userId}` | Full namespace provisioning (body: credentials, config) |
| `POST` | `/scale/{userId}/server` | Scale server Deployment (body: `{"replicas": 0}` or `1`) |
| `DELETE` | `/namespace/{userId}` | Tear down entire namespace (destructive) |
| `GET` | `/status/{userId}` | Check if namespace + server exist and are healthy |

### Dockerfile

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o /controller .

FROM alpine:3.20
RUN apk add --no-cache kubectl
COPY --from=builder /controller /usr/local/bin/controller
COPY manifests/ /etc/mclaude/manifests/
ENTRYPOINT ["controller"]
```

Manifests are Go templates stored in `/etc/mclaude/manifests/`. Controller renders them with user-specific values and applies via the K8s Go client.

### Database Migrations

The controller embeds migration SQL files in the `db-migrations` ConfigMap. The server Deployment has a **dbmate init container** that runs migrations before the server starts:

```yaml
initContainers:
  - name: migrate
    image: ghcr.io/amacneil/dbmate:latest
    command: ["dbmate", "--url", "$(DATABASE_URL)", "--migrations-dir", "/migrations", "up"]
    volumeMounts:
      - name: migrations
        mountPath: /migrations
volumes:
  - name: migrations
    configMap:
      name: db-migrations
```

dbmate tracks applied migrations in a `schema_migrations` table. Runs on every server pod start — no-op if all migrations are applied (~100ms).

---

## 5. Postgres Schema + Migrations

### migrations/001_initial_schema.sql

```sql
-- migrate:up
CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    git_url TEXT,
    created_at TIMESTAMPTZ DEFAULT now(),
    last_active_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    worktree TEXT NOT NULL,
    cwd TEXT NOT NULL,
    name TEXT,
    conversation_id TEXT,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE events (
    id BIGSERIAL PRIMARY KEY,
    session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL,
    data JSONB NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_events_session ON events(session_id, id DESC);
CREATE INDEX idx_events_created ON events(created_at);

-- LISTEN/NOTIFY trigger: fires on every event INSERT, notifies the server
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

-- migrate:down
DROP TRIGGER IF EXISTS event_inserted ON events;
DROP FUNCTION IF EXISTS notify_new_event();
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS projects;
```

### migrations/002_event_retention.sql

```sql
-- migrate:up
-- Index for retention cleanup (server-side cron job deletes old events)
CREATE INDEX idx_events_retention ON events(created_at) WHERE created_at < now() - interval '30 days';

-- migrate:down
DROP INDEX IF EXISTS idx_events_retention;
```

---

## 6. config-sync sidecar

Not a separate image — runs as a shell script in a `bitnami/kubectl` container within each project pod. Watches `~/.claude/settings.json` and `CLAUDE.md` for changes, patches the `user-config` ConfigMap. Uses a hash comparison before patching to avoid feedback loops (server re-seeding triggers a write, config-sync detects it, but hashes match → skip).

Defined inline in the project pod Deployment manifest — no separate image or build step needed.

---

## Build Order

```
1. Postgres schema (migrations/001_initial_schema.sql)
   ↓
2. mclaude-session (Dockerfile, entrypoint, hooks, pkg shim)
   ↓
3. mclaude-server ──────────┐ (can develop in parallel)
4. mclaude-jsonl-tailer ────┘
   ↓
5. mclaude-controller
```

Steps 3 and 4 are independent — they only share Postgres. Develop in parallel.

## Testing

### Local (docker-compose)

```bash
# Start the stack
docker-compose up -d

# Verify Postgres + migrations
docker-compose exec postgres psql -U mclaude -c '\dt'

# Verify session container starts tmux
docker-compose exec session tmux list-sessions

# Verify server API
curl http://localhost:8377/projects

# Create a test project + session
curl -X POST http://localhost:8377/projects -d '{"name":"test","gitUrl":""}'
curl -X POST http://localhost:8377/sessions -d '{"projectId":"test","branch":"main"}'

# Verify jsonl-tailer: create a test JSONL file, check it appears in Postgres
echo '{"type":"user","message":"hello"}' >> test-data/projects/-data-worktrees-main/test-session/test.jsonl
docker-compose exec postgres psql -U mclaude -c 'SELECT count(*) FROM events'
```

### Integration (requires K8s cluster)

Deploy using the deployment plan (`docs/plan-k8s-integration.md`) and run its verification section (17 steps covering project lifecycle, session lifecycle, memory sharing, config sync, resilience, and provisioning).

## Repo Structure

```
mclaude/
  mclaude-session/
    Dockerfile
    entrypoint.sh
    bin/
      pkg
    etc/
      claude-code/
        CLAUDE.md              # managed policy (global tier)
        settings.json          # managed hooks config
        hooks/
          guard.sh             # blocks git checkout, apt install, etc.
    hooks.d/
      registry-mirrors.sh     # enterprise registry mirror config
  mclaude-server/
    Package.swift
    Sources/
      K8sSessionManager.swift  (rewrite)
      PostgresClient.swift     (new)
      APIServer.swift          (extend)
      ConfigWatcher.swift      (new)
      IdleManager.swift        (new)
      ArchiveManager.swift     (new)
      main.swift               (extend)
    migrations/
      001_initial_schema.sql
      002_event_retention.sql
  mclaude-jsonl-tailer/
    main.go
    Dockerfile
  mclaude-controller/
    main.go
    manifests/                 (K8s manifest Go templates)
    Dockerfile
  docker-compose.yml
  docs/
    plan-k8s-integration.md   (deployment plan — cluster-specific)
    plan-core-containers.md   (this file — core software)
```
