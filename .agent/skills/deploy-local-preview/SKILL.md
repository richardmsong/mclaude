---
name: deploy-local-preview
description: Stand up a local k3d cluster pulling from ghcr.io and deploy mclaude to it. Idempotent — safe to re-run. Use when airgapped from the main dev setup or when you need a fully self-contained local cluster.
---

# Deploy Local Preview (k3d + ghcr.io)

Deploys mclaude to a local k3d cluster using pre-built images from ghcr.io.
No local Docker builds. DNS via CoreDNS inside the cluster, surfaced to the host via NodePort on UDP 5053.

**Ingress URL**: `http://dev.mclaude.local`

---

## Usage

```
/deploy-local-preview
```

No arguments. The script is fully idempotent — re-running it is safe.

---

## Prerequisites

These must be in place before running. Check, don't assume.

```bash
which k3d kubectl helm gh    # all must exist on PATH
docker info                  # Docker Desktop must be running
gh auth status               # must be authenticated to ghcr.io org
```

Also check that port 5053 UDP is free on the host (used for DNS NodePort):
```bash
lsof -iUDP:5053 2>/dev/null | grep LISTEN && echo "port in use!" || echo "free"
```

---

## Algorithm

### Step 1 — Create or reuse the k3d cluster

```bash
CLUSTER="mclaude-dev"

if k3d cluster list 2>/dev/null | grep -q "^${CLUSTER} "; then
  echo "cluster already exists, reusing"
else
  # Write registry config (insecure_skip_verify bypasses corporate TLS interception)
  GHCR_TOKEN=$(gh auth token)
  cat > /tmp/k3d-registries.yaml <<EOF
configs:
  "ghcr.io":
    auth:
      username: YOUR_GITHUB_USERNAME
      password: ${GHCR_TOKEN}
    tls:
      insecure_skip_verify: true
  "docker.io":
    tls:
      insecure_skip_verify: true
  "registry-1.docker.io":
    tls:
      insecure_skip_verify: true
EOF

  k3d cluster create "$CLUSTER" \
    --port "80:80@loadbalancer" \
    --port "5053:30053/udp@server:0" \
    --registry-config /tmp/k3d-registries.yaml \
    --wait
fi

k3d kubeconfig merge "$CLUSTER" --kubeconfig-merge-default --overwrite
```

### Step 2 — Fix kubeconfig (k3d sets server to 0.0.0.0; TLS cert doesn't cover it)

```bash
PORT=$(kubectl config view --raw -o jsonpath='{.clusters[?(@.name=="k3d-mclaude-dev")].cluster.server}' | grep -oE '[0-9]+$')
kubectl config set-cluster k3d-mclaude-dev --server="https://127.0.0.1:${PORT}"
kubectl cluster-info   # verify: should show 127.0.0.1, not 0.0.0.0
```

### Step 3 — Namespace + secrets

```bash
NS="mclaude-system"
RELEASE="mclaude"

kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -

# Postgres password
kubectl create secret generic mclaude-postgres \
  --namespace "$NS" \
  --from-literal=postgres-password="devpassword" \
  --dry-run=client -o yaml | kubectl apply -f -

# Control-plane credentials
PG_HOST="${RELEASE}-postgres.${NS}.svc.cluster.local"
kubectl create secret generic mclaude-control-plane \
  --namespace "$NS" \
  --from-literal=database-url="postgres://mclaude:devpassword@${PG_HOST}:5432/mclaude?sslmode=disable" \
  --from-literal=admin-token="dev-admin-token" \
  --from-literal=nats-operator-jwt="dev-stub-jwt" \
  --from-literal=nats-operator-seed="dev-stub-seed" \
  --dry-run=client -o yaml | kubectl apply -f -

# ghcr.io pull secret
GHCR_TOKEN=$(gh auth token)
kubectl create secret docker-registry ghcr-pull-secret \
  --namespace "$NS" \
  --docker-server=ghcr.io \
  --docker-username=YOUR_GITHUB_USERNAME \
  --docker-password="$GHCR_TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -
```

### Step 4 — CoreDNS custom zone for *.mclaude.local

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: coredns-custom
  namespace: kube-system
data:
  mclaude.local.server: |
    mclaude.local. {
        template IN A {
            answer "{{ .Name }} 60 IN A 127.0.0.1"
        }
        log
        errors
    }
EOF

kubectl rollout restart deployment/coredns -n kube-system
kubectl rollout status deployment/coredns -n kube-system --timeout=60s
```

Then create a NodePort service to expose CoreDNS on UDP 5053 (required because `kubectl port-forward` is TCP-only; DNS needs UDP):

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Service
metadata:
  name: coredns-nodeport
  namespace: kube-system
spec:
  selector:
    k8s-app: kube-dns
  type: NodePort
  ports:
    - name: dns-udp
      port: 53
      targetPort: 53
      nodePort: 30053
      protocol: UDP
EOF
```

### Step 5 — macOS /etc/resolver (run once; requires sudo)

Tell the user to run this command themselves:

```bash
sudo mkdir -p /etc/resolver
printf 'nameserver 127.0.0.1\nport 5053\n' | sudo tee /etc/resolver/mclaude.local
```

Verify DNS resolves:
```bash
ping -c 1 dev.mclaude.local   # should resolve to 127.0.0.1
```

> **Why `.local`**: The corporate PAC file (`corporate-proxy.pac`) explicitly bypasses the proxy for `shExpMatch(host, "*.local")`. Using `.internal` or `.mclaude.internal` goes through the proxy and the browser can't reach the cluster. `.local` on macOS is mDNS/Bonjour, but `/etc/resolver/mclaude.local` overrides mDNS for the `mclaude.local` subdomain specifically.

### Step 6 — Helm install

The `values-k3d-ghcr.yaml` file in this repo is the authoritative values for this setup.

Extract the Claude OAuth token and pass it as `devOAuthToken`. The reconciler writes it into the `user-secrets` Secret in each user namespace so session-agent pods can auth Claude Code without a manual login.

From local credentials if available:
```bash
OAUTH_TOKEN=$(python3 -c "
import json, os
p = os.path.expanduser('~/.claude/.credentials.json')
print(json.load(open(p))['claudeAiOauth']['accessToken'])
")

LOCAL_DEPLOY=1 helm upgrade --install mclaude ./charts/mclaude \
  -n mclaude-system \
  -f charts/mclaude/values-k3d-ghcr.yaml \
  --set "controlPlane.devOAuthToken=${OAUTH_TOKEN}"
```

`LOCAL_DEPLOY=1` is required — the pre-tool-use hook blocks `helm upgrade/install` unless this is set (CI-only guard).

> **Why this matters**: without `devOAuthToken`, the session-agent pod has no Claude credentials. It receives messages and silently does nothing. The token flows: Helm → `DEV_OAUTH_TOKEN` env on control-plane → reconciler writes `oauth-token` key into `user-secrets` Secret → session-agent mounts it at `/home/node/.user-secrets/oauth-token`.

### Step 7 — Wait for pods

```bash
kubectl rollout status deployment/mclaude-control-plane -n mclaude-system --timeout=120s
kubectl rollout status deployment/mclaude-spa -n mclaude-system --timeout=120s
kubectl get pods -n mclaude-system
```

All pods should show `1/1 Running`.

### Step 8 — Patch session-agent pods for RBC corporate TLS

The corporate proxy intercepts TLS. Session-agent pods don't have the RBC CA bundle, so Claude Code's API calls fail with `SSL certificate verification failed`. Patch all session-agent deployments with `NODE_TLS_REJECT_UNAUTHORIZED=0`:

```bash
# Wait for devSeed to provision the user namespace (may take a few seconds)
for i in $(seq 1 20); do
  USER_NS=$(kubectl get ns -l mclaude.io/managed=true -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
  [ -n "$USER_NS" ] && break
  sleep 2
done

if [ -n "$USER_NS" ]; then
  # Wait for the session-agent deployment to appear
  for i in $(seq 1 20); do
    DEPLOY=$(kubectl get deploy -n "$USER_NS" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    [ -n "$DEPLOY" ] && break
    sleep 2
  done
  if [ -n "$DEPLOY" ]; then
    kubectl patch deployment "$DEPLOY" -n "$USER_NS" \
      --type=json \
      -p='[{"op":"add","path":"/spec/template/spec/containers/0/env/-","value":{"name":"NODE_TLS_REJECT_UNAUTHORIZED","value":"0"}}]'
    kubectl rollout status deployment/"$DEPLOY" -n "$USER_NS" --timeout=60s
    echo "patched $DEPLOY in $USER_NS"
  else
    echo "WARNING: no session-agent deployment found in $USER_NS — patch manually if needed"
  fi
else
  echo "WARNING: no user namespace found — patch any session-agent deployments manually"
fi
```

> **Why this is needed**: The RBC CA bundle is not baked into the session-agent container image. Until `NODE_EXTRA_CA_CERTS` support is added to the Helm chart (tracked separately), this manual patch is required for local k3d deploys behind the corporate proxy.
>
> **Also patch new projects**: If you create a new project after this step, re-run the kubectl patch for the new namespace/deployment.

### Step 9 — Verify

```bash
# Ingress resolves
curl -s http://dev.mclaude.local/healthz   # should return 200 from control-plane

# SPA
open http://dev.mclaude.local
```

---

## Teardown

```bash
k3d cluster delete mclaude-dev
sudo rm -f /etc/resolver/mclaude.local
```

---

## Known Issues / Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `server gave HTTP response to HTTPS client` | kubeconfig has `0.0.0.0` as server address | Step 2: repoint server to `127.0.0.1:<port>` |
| `ErrImagePull` / 403 from ghcr.io | GHCR token expired in registry config | Re-run Step 1 (delete cluster first if needed): `k3d cluster delete mclaude-dev` |
| `ErrImagePull` / TLS handshake failure | Corporate proxy intercepts TLS | Ensure `insecure_skip_verify: true` is in `/tmp/k3d-registries.yaml` and cluster was created with `--registry-config` |
| DNS doesn't resolve (`ping dev.mclaude.local` hangs) | `/etc/resolver/mclaude.local` missing or wrong port | Step 5: check file exists with `cat /etc/resolver/mclaude.local`; dig `@127.0.0.1 -p 5053 dev.mclaude.local` to test CoreDNS directly |
| Browser can't connect despite DNS working | Corporate proxy intercepting (wrong TLD) | Ensure using `.local` not `.internal`; `.local` is already in PAC bypass list |
| Login 405 | Ingress not routing to control-plane | Check `kubectl get ingress -n mclaude-system`; host must be `dev.mclaude.local` |
| NATS WebSocket fails | `natsUrl` in login response is internal cluster URL | Old binary — SPA falls back to `ws://<origin>/nats` which routes through Traefik correctly |
| `projects` table missing (login succeeds but projects 500s) | pgx `pool.Exec` only runs first statement in multi-statement SQL | Manually create: `kubectl exec -it deploy/mclaude-postgres -n mclaude-system -- psql -U mclaude -c "CREATE TABLE IF NOT EXISTS projects (...)"` |
| devSeed user not created (old binary) | Old ghcr.io `latest` predates devSeed feature | Manually seed: see "Manual DB Seeding" below |
| `helm upgrade/install` blocked by hook | Pre-tool-use hook guards against local deploys | Set `LOCAL_DEPLOY=1` env var (Step 6) |
| Session-agent: `SSL certificate verification failed` | Corporate proxy intercepts TLS; container lacks RBC CA | Step 8: patch deployment with `NODE_TLS_REJECT_UNAUTHORIZED=0` |
| Session-agent: `Not logged in · Please run /login` | `CLAUDE_CODE_OAUTH_TOKEN` not set in pod | Check `user-secrets` Secret has `oauth-token` key; if pod started before token was added, `kubectl rollout restart` |

---

## Manual DB Seeding (old binary workaround)

If the ghcr.io `latest` binary predates devSeed support, seed the dev user manually:

```bash
# Get bcrypt hash of "dev" password (cost 12)
HASH=$(docker run --rm httpd:2.4-alpine htpasswd -bnBC 12 x dev | cut -d: -f2)

# Seed into postgres
kubectl exec -it deploy/mclaude-postgres -n mclaude-system -- \
  psql -U mclaude -c "
    INSERT INTO users (id, email, password_hash, created_at, updated_at)
    VALUES (gen_random_uuid(), 'dev@mclaude.local', '$HASH', now(), now())
    ON CONFLICT (email) DO NOTHING;
  "
```

Login: `dev@mclaude.local` / `dev`
