# MClaude Project Rules

## Deployments — CI owns builds, you observe

**Never run `helm upgrade`, `docker build`, or `k3d image import` yourself.**
CI builds images and deploys. Your tools are `kubectl` (read-only observation) and `gh` (triggering + status checks).

**Never use `gh run watch`** — it blocks until timeout. Use `gh run view {id}` (one-shot poll) instead.

**Before triggering a new CI run**, cancel any in-progress runs for the same workflow + branch:
```bash
gh run cancel $(gh run list --workflow=X.yml --branch=Y --status=in_progress --json databaseId -q '.[].databaseId') 2>/dev/null || true
```

**To clean up a Helm release** (broken state or orphaned), use helm uninstall — it knows all resources including cluster-scoped (ClusterRoles, ClusterRoleBindings) and Ingresses that `kubectl delete all` misses:
```bash
helm uninstall "{release}" -n mclaude-system
```
If that fails, use `helm get manifest {release} -n mclaude-system` to get the full resource list first.

## UI changes — spec first, then dev-harness

**Never write UI implementation code directly.**
Order: update `docs/ui-spec.md` → commit spec → run `/dev-harness spa` → it implements and tests.

## Polling — keep checking until done

After triggering any async operation (CI run, pod rollout), **keep polling** until it resolves. Do not summarize and hand back to the user mid-flight. Check `gh run view` + `kubectl get pods` on every cycle.

## Before debugging a config mismatch — read the source

Before guessing at a port, command, or env var, read the relevant Dockerfile / config file first. The answer is always there.
