---
name: deploy-preview
description: Trigger a preview deploy via CI and monitor it using local tools. Use when the user wants to deploy a feature branch to the k3d preview cluster.
---

# Deploy Preview

Trigger CI to build and deploy the current branch to the k3d `mclaude-dev` cluster, then monitor progress locally.

**You do not build images or run helm yourself.** CI owns the build and deploy. Local tools (`kubectl`, `gh`) are used only for observing.

## Usage

```
/deploy-preview [branch]
```

Omit branch to use the current branch.

---

## Algorithm

```
1. Ensure changes are pushed:
   git push origin HEAD

2. Trigger the CI workflow:
   gh workflow run deploy-preview.yml --ref {branch}
   OR: just push to the branch — the workflow triggers on push automatically.

3. Get the run ID immediately (do NOT use `gh run watch` — it blocks):
   gh run list --workflow=deploy-preview.yml --branch {branch} --limit 1

4. Poll for build completion using gh run view (non-blocking):
   gh run view {run-id} --log-failed   # only if failed
   gh run view {run-id}                # for status summary

5. Once build jobs complete (check job statuses in gh run view output),
   watch pod status locally — this is fast feedback, no blocking:
   kubectl get pods -n mclaude-system | grep "preview-{slug}"

6. Verify readiness:
   Watch for 1/1 Ready on all pods. Check individual pods if stuck:
   kubectl describe pod {pod} -n mclaude-system | tail -20
   kubectl logs {pod} -n mclaude-system -c {container}

7. Smoke test (optional):
   kubectl port-forward svc/mclaude-preview-{slug}-spa 18080:80 -n mclaude-system &
   curl -s http://localhost:18080/ | grep -q '<div id="root">' && echo "SPA OK"
   kill %1
```

---

## Key Rules

- **Never use `gh run watch`** — it blocks for the full run duration before returning.
- **Never run helm upgrade yourself** — CI does the deploy. Only observe with kubectl/gh.
- **Never run docker build or k3d image import** — CI pushes to GHCR, cluster pulls from there.
- Poll with `gh run view {id}` (one-shot) then check `kubectl get pods` for fast local feedback.

---

## Branch Slug Convention

The workflow computes: `slug="${GITHUB_REF_NAME//\//-"}`
So branch `preview/new-project-ui` → slug `preview-new-project-ui` → release `mclaude-preview-preview-new-project-ui`.

---

## Known Issues / Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `ErrImagePull` + "Unable to retrieve pull secret" | `imagePullSecrets` passed as object not string in helm | Bug in `deploy-preview.yml`: use `--set 'global.imagePullSecrets[0]=ghcr-pull-secret'` (no `.name`) |
| `ErrImagePull` + 401 Unauthorized | GHCR auth missing in cluster | `ghcr-pull-secret` in `mclaude-system` must exist — create with `gh auth token` if missing |
| Init container `StartError`: dbmate not found | Component image on GHCR missing dbmate | Rebuild and push the control-plane image from CI |
| Namespace conflict on helm install | Namespace exists without Helm labels | `namespace.create=false` in helm command |
| zsh: no matches found: `global.imagePullSecrets[0]...` | zsh glob expansion | In workflow YAML: quote the arg. In local shell: `--set 'global.imagePullSecrets[0]=...'` |

---

## Cleanup

To remove a preview release (run on the cluster host):
```bash
helm uninstall "mclaude-preview-{slug}" -n mclaude-system
```

To list all preview releases:
```bash
helm list -n mclaude-system | grep preview
```
