---
name: deploy-preview
description: Trigger any CI deploy and monitor it using local tools. CI builds and deploys; local kubectl/gh are for observing only.
---

# Deploy (CI + Local Monitoring)

This skill applies whenever CI deploys to the k3d cluster — preview builds, production deploys, hotfixes. The pattern is the same regardless of which workflow fires.

**You do not build images, run helm, or push to GHCR yourself.** CI owns every build and deploy. Local tools (`kubectl`, `gh`) are used only for observing.

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

2. Cancel any in-progress runs for this workflow + branch BEFORE triggering:
   gh run cancel $(gh run list --workflow={workflow}.yml --branch {branch} \
     --status=in_progress --json databaseId -q '.[].databaseId') 2>/dev/null || true

3. Trigger the CI workflow (or let push trigger it automatically):
   gh workflow run {workflow}.yml --ref {branch}

4. Get the run ID:
   gh run list --workflow={workflow}.yml --branch {branch} --limit 1

5. Background-poll until build jobs complete AND deploy job starts.
   Do NOT poll the full run — the deploy step uses --wait and can hang for minutes.
   Instead, poll until the deploy job transitions from "queued" to "in_progress":

   while true; do
     DEPLOY_STATUS=$(gh run view {run-id} --json jobs \
       -q '.jobs[] | select(.name=="Deploy preview") | .status' 2>/dev/null)
     BUILD_CONCLUSION=$(gh run view {run-id} --json jobs \
       -q '[.jobs[] | select(.name | startswith("Build"))] | map(.conclusion) | unique | .[]' 2>/dev/null)
     echo "build: $BUILD_CONCLUSION  deploy: $DEPLOY_STATUS"
     [[ "$DEPLOY_STATUS" == "in_progress" ]] && break
     [[ "$BUILD_CONCLUSION" == *"failure"* ]] && break
     sleep 10
   done
   
   Run this with run_in_background: true. Respond to the user while it runs,
   and resume when notified.

6. Once deploy job is in_progress, switch to kubectl — pods appear within seconds,
   faster feedback than waiting for gh:
   kubectl get pods -n mclaude-system | grep "{release-name}"

7. Watch for 1/1 Ready on target pods. Do NOT wait for helm --wait to finish.
   Helm may timeout on non-critical components (e.g. control-plane missing dbmate)
   while the SPA is already healthy. Trust kubectl, not helm exit code.

8. On failure, check logs:
   kubectl describe pod {pod} -n mclaude-system | tail -20
   kubectl logs {pod} -n mclaude-system -c {container}
   gh run view {run-id} --log-failed   # only if build step failed
```

---

## Key Rules

- **Never use `gh run watch`** — it blocks for the full run duration before returning. Use `gh run view` (one-shot) instead.
- **Never run helm upgrade yourself** — CI does the deploy. Only observe with kubectl/gh.
- **Never run docker build or k3d image import** — CI pushes to GHCR, cluster pulls from there.
- After triggering CI, don't just wait — poll `gh run view` and `kubectl get pods` actively.

---

## Branch Slug Convention

The workflow computes: `slug="${GITHUB_REF_NAME//\//-"}`
So branch `preview/new-project-ui` → slug `preview-new-project-ui` → release `mclaude-preview-preview-new-project-ui`.

The k3d cluster (`k3d-mclaude-dev`) maps host port 80 → cluster port 80 (Traefik).
Preview URL is `http://preview-{slug}.mclaude.internal` — check `kubectl get ingress` for the exact hostname.

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

To remove a preview release, use helm uninstall — it knows every resource the release owns,
including cluster-scoped ones (ClusterRoles, ClusterRoleBindings) and Ingresses that a
namespace-scoped `kubectl delete` would miss:

```bash
helm uninstall "mclaude-preview-{slug}" -n mclaude-system
```

If the release is in a broken state and `helm uninstall` fails, get the manifest first to
see every resource, then delete them:

```bash
helm get manifest "mclaude-preview-{slug}" -n mclaude-system | kubectl delete -f - --ignore-not-found
# Also delete cluster-scoped resources (not in the manifest namespace):
kubectl delete clusterrole,clusterrolebinding -l "app.kubernetes.io/instance=mclaude-preview-{slug}"
```

Never use `kubectl delete all,...` with a label selector alone — it misses ClusterRoles,
ClusterRoleBindings, and Ingresses. Use helm uninstall or the manifest approach above.

To list all preview releases:
```bash
helm list -n mclaude-system | grep preview
```
