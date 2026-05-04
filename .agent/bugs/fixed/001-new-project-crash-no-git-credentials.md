# BUG-001: New project with git URL crashes — no git credentials in pod

**Severity**: Critical — blocks project creation with any private repo  
**Component**: session-agent (entrypoint.sh)  
**Reported**: 2026-04-16  

## Symptoms

- User creates a new project in the SPA with a git URL (e.g., `https://github.com/mclaude-project/mclaude`)
- Project appears to create successfully (DB, KV, MCProject CR all written)
- Session-agent pod enters CrashLoopBackOff
- No session-agent ever starts for this project

## Root Cause

The session-agent entrypoint.sh attempts `git clone $GIT_URL /data/repo` on startup. For private repos, git prompts for credentials but there's no TTY and no credential helper configured:

```
fatal: could not read Username for 'https://github.com': No such device or address
[entrypoint] Git clone failed — exiting for restart
```

The entrypoint exits with code 1, causing k8s to restart the pod in a crash loop.

The real fix is the github-oauth feature (`docs/adr-0007-github-oauth.md`) which provisions `gh auth` credential helpers via K8s Secrets. That feature's control-plane endpoints are not yet implemented (0/17 spec items).

## Evidence

```
kubectl logs -n mclaude-4196156f-... project-5e738072-...:
  Cloning into bare repository '/data/repo'...
  fatal: could not read Username for 'https://github.com': No such device or address
  [entrypoint] Git clone failed — exiting for restart
```

MCProject CR shows `gitUrl: https://github.com/mclaude-project/mclaude` and `phase: Ready` (reconciler succeeded, but pod crashes).

## Fix

**Short-term**: Make entrypoint.sh fallback to `git init --bare /data/repo` when clone fails. Log a warning instead of exiting. The project starts with an empty repo — git operations will fail until credentials are configured, but the session-agent can still run.

**Long-term**: Implement github-oauth control-plane endpoints so tokens are written to `user-secrets` Secret and `gh auth` credential helpers work inside the pod.

## Files

- `mclaude-session-agent/entrypoint.sh` — clone logic that exits on failure
- `docs/adr-0007-github-oauth.md` — complete spec for OAuth credential flow (design audit: CLEAN)
- `mclaude-control-plane/projects.go:44-124` — project creation handler (does not provision git credentials)
