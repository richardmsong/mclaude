---
name: deploy-local-preview
description: Stand up a local k3d cluster and deploy mclaude to it. Builds images locally if ghcr.io images are unavailable. HTTPS via Let's Encrypt wildcard cert for *.mclaude.richardmcsong.com. DNS via DigitalOcean + ExternalDNS. Idempotent — safe to re-run.
user_invocable: true
---

# Deploy Local Preview (k3d)

Deploys mclaude to a local k3d cluster. Tries ghcr.io images first, falls back to local Docker builds. HTTPS via a Let's Encrypt wildcard cert for `*.mclaude.richardmcsong.com` obtained by cert-manager using the DigitalOcean DNS-01 solver. DNS A records are managed by ExternalDNS pointing at the k3d host's Tailscale IP.

**Ingress URL**: `https://dev.mclaude.richardmcsong.com`

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
which bw                     # bitwarden CLI (for OAuth + DO API token)
docker info                  # Docker Desktop must be running
gh auth status               # must be authenticated
bw status                    # must be unlocked
```

Also ensure a Bitwarden entry exists with the **exact name**:
`"DigitalOcean API token — richardmcsong.com zone edit"`
holding a DigitalOcean API token scoped to write on the `richardmcsong.com` zone.

---

## Algorithm

### Step 1 — Create or reuse the k3d cluster

```bash
CLUSTER="mclaude-dev"
TS_IP=$(tailscale ip -4)

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
    --port "443:443@loadbalancer" \
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

# GitHub OAuth client-secret (matches controlPlane.providers[github].clientSecretRef
# in values-k3d-ghcr.yaml). Pulled from Bitwarden entry "Mclaude dev GitHub app".
GH_OAUTH_SECRET=$(bw get password "YOUR_BITWARDEN_GITHUB_OAUTH_ITEM_ID" 2>/dev/null)
if [ -z "$GH_OAUTH_SECRET" ]; then
  echo "WARNING: GitHub OAuth client-secret not found in Bitwarden — /auth/providers will fail for GitHub"
else
  kubectl create secret generic github-oauth-secret \
    --namespace "$NS" \
    --from-literal=client-secret="$GH_OAUTH_SECRET" \
    --dry-run=client -o yaml | kubectl apply -f -
fi
```

### Step 4 — cert-manager, ExternalDNS, and Let's Encrypt wildcard cert

Pull the DigitalOcean API token from Bitwarden and apply it as a K8s Secret in both namespaces:

```bash
DO_TOKEN=$(bw get password "DigitalOcean API token — richardmcsong.com zone edit")

kubectl create namespace cert-manager --dry-run=client -o yaml | kubectl apply -f -
kubectl create namespace external-dns --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic digitalocean-api-token \
  --namespace cert-manager \
  --from-literal=access-token="${DO_TOKEN}" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic digitalocean-api-token \
  --namespace external-dns \
  --from-literal=access-token="${DO_TOKEN}" \
  --dry-run=client -o yaml | kubectl apply -f -
```

Install cert-manager (waits for CRDs to be registered before continuing):

```bash
helm repo add jetstack https://charts.jetstack.io --force-update
helm upgrade --install cert-manager jetstack/cert-manager \
  -n cert-manager --create-namespace \
  --set installCRDs=true \
  --wait
```

Install ExternalDNS (DigitalOcean provider, watches Ingresses):

```bash
helm repo add external-dns https://kubernetes-sigs.github.io/external-dns/ --force-update
helm upgrade --install external-dns external-dns/external-dns \
  -n external-dns --create-namespace \
  --set provider=digitalocean \
  --set env[0].name=DO_TOKEN \
  --set env[0].valueFrom.secretKeyRef.name=digitalocean-api-token \
  --set env[0].valueFrom.secretKeyRef.key=access-token \
  --set domainFilters[0]=mclaude.richardmcsong.com \
  --set policy=sync \
  --set txtOwnerId=mclaude-k3d \
  --set sources[0]=ingress \
  --wait
```

Apply the ClusterIssuer and wildcard Certificate CR:

```bash
kubectl apply -f - <<'EOF'
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: mclaude-letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: richard@richardmcsong.com
    privateKeySecretRef:
      name: mclaude-letsencrypt-prod-account
    solvers:
      - dns01:
          digitalocean:
            tokenSecretRef:
              name: digitalocean-api-token
              key: access-token
EOF

kubectl apply -f - <<'EOF'
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: mclaude-richardmcsong-wildcard
  namespace: mclaude-system
spec:
  secretName: mclaude-richardmcsong-tls
  issuerRef:
    kind: ClusterIssuer
    name: mclaude-letsencrypt-prod
  commonName: "*.mclaude.richardmcsong.com"
  dnsNames:
    - "*.mclaude.richardmcsong.com"
    - "mclaude.richardmcsong.com"
  duration: 2160h
  renewBefore: 720h
EOF
```

Wait for cert issuance (DNS-01 challenge + LE validation takes ~60–180s):

```bash
kubectl wait --for=condition=Ready \
  certificate/mclaude-richardmcsong-wildcard \
  -n mclaude-system --timeout=5m
```

> **No keychain step**: the Let's Encrypt cert is trusted by every device out of the box. No `sudo security add-trusted-cert` needed.

### Step 5 — Build images locally (if ghcr.io images are unavailable)

Try pulling from ghcr.io first. If the images don't exist (no packages published yet), build locally and import into k3d:

```bash
REPO_ROOT=$(git rev-parse --show-toplevel)
CP_DIR="$REPO_ROOT/mclaude-control-plane"
SPA_DIR="$REPO_ROOT/mclaude-web"
SA_DIR="$REPO_ROOT/mclaude-session-agent"

# Try ghcr.io pull — if ANY image fails, build all locally
if ! docker pull ghcr.io/richardmsong/mclaude-control-plane:latest 2>/dev/null || \
   ! docker pull ghcr.io/richardmsong/mclaude-spa:latest 2>/dev/null || \
   ! docker pull ghcr.io/richardmsong/mclaude-session-agent:latest 2>/dev/null; then
  echo "ghcr.io images not available — building locally"

  # Build control-plane (Go binary → Docker)
  cd "$CP_DIR"
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o control-plane .
  docker build -t mclaude-control-plane:dev .
  rm -f control-plane
  cd "$REPO_ROOT"

  # Build SPA (npm build happens inside Dockerfile)
  docker build -t mclaude-spa:dev "$SPA_DIR"

  # Build session-agent (Go binary → Docker)
  cd "$SA_DIR"
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o session-agent .
  docker build -t mclaude-session-agent:dev .
  rm -f session-agent
  cd "$REPO_ROOT"

  # Import into k3d
  k3d image import \
    mclaude-control-plane:dev \
    mclaude-spa:dev \
    mclaude-session-agent:dev \
    --cluster mclaude-dev

  USE_LOCAL_IMAGES=true
else
  USE_LOCAL_IMAGES=false
fi
```

> **Why local build**: ghcr.io packages may not exist yet (no CI publishing configured). The Go binaries are cross-compiled for linux/arm64 (k3d runs arm64 Linux nodes on Apple Silicon). The SPA Dockerfile does the npm build internally.

### Step 6 — Helm install

The `values-k3d-ghcr.yaml` file in this repo is the authoritative values for this setup.

Extract the Claude OAuth token from Bitwarden (primary) or local credentials (fallback):

```bash
# Primary: Bitwarden
OAUTH_TOKEN=$(bw get password "YOUR_BITWARDEN_CLAUDE_TOKEN_ITEM_ID" 2>/dev/null)

# Fallback: local credentials file
if [ -z "$OAUTH_TOKEN" ]; then
  OAUTH_TOKEN=$(python3 -c "
import json, os
p = os.path.expanduser('~/.claude/.credentials.json')
print(json.load(open(p))['claudeAiOauth']['accessToken'])
" 2>/dev/null) || true
fi

if [ -z "$OAUTH_TOKEN" ]; then
  echo "WARNING: no OAuth token found — session-agent will not authenticate"
fi
```

Build the helm install command. TLS ingress settings and hostnames are in `values-k3d-ghcr.yaml`. The Tailscale IP is passed via `--set` since it is not known at file-edit time. If local images were built, override the image tags:

```bash
HELM_ARGS=(
  -n mclaude-system
  -f charts/mclaude/values-k3d-ghcr.yaml
  --set "controlPlane.devOAuthToken=${OAUTH_TOKEN}"
  --set "ingress.externalDnsTarget=${TS_IP}"
  --wait --timeout 5m
  --force-conflicts
)

if [ "$USE_LOCAL_IMAGES" = true ]; then
  HELM_ARGS+=(
    --set "controlPlane.image.registry="
    --set "controlPlane.image.repository=mclaude-control-plane"
    --set "controlPlane.image.tag=dev"
    --set "controlPlane.image.pullPolicy=Never"
    --set "spa.image.registry="
    --set "spa.image.repository=mclaude-spa"
    --set "spa.image.tag=dev"
    --set "spa.image.pullPolicy=Never"
    --set "sessionAgent.image.registry="
    --set "sessionAgent.image.repository=mclaude-session-agent"
    --set "sessionAgent.image.tag=dev"
    --set "sessionAgent.image.pullPolicy=Never"
    --set "global.imagePullSecrets="
  )
fi

SDD_DEBUG=1 helm upgrade --install mclaude ./charts/mclaude "${HELM_ARGS[@]}"
```

`SDD_DEBUG=1` is required — the spec-driven-dev hook blocks `helm upgrade/install` unless this is set (CI-only guard).

> **Why this matters**: without `devOAuthToken`, the session-agent pod has no Claude credentials. It receives messages and silently does nothing. The token flows: Helm → `DEV_OAUTH_TOKEN` env on control-plane → reconciler writes `oauth-token` key into `user-secrets` Secret → session-agent mounts it at `/home/node/.user-secrets/oauth-token`.

### Step 7 — Wait for pods

```bash
kubectl rollout status deployment/mclaude-control-plane -n mclaude-system --timeout=120s
kubectl rollout status deployment/mclaude-spa -n mclaude-system --timeout=120s
kubectl get pods -n mclaude-system
```

All pods should show `1/1 Running`.

### Step 8 — Verify

```bash
# Ingress resolves with valid Let's Encrypt cert
curl -s https://dev.mclaude.richardmcsong.com/healthz   # should return 200

# SPA
open https://dev.mclaude.richardmcsong.com
```

---

## Teardown

```bash
k3d cluster delete mclaude-dev
```

---

## Known Issues / Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `server gave HTTP response to HTTPS client` | kubeconfig has `0.0.0.0` as server address | Step 2: repoint server to `127.0.0.1:<port>` |
| `ErrImagePull` / 403 from ghcr.io | GHCR token expired in registry config | Re-run Step 1 (delete cluster first if needed): `k3d cluster delete mclaude-dev` |
| `ErrImagePull` / TLS handshake failure | Corporate proxy intercepts TLS | Ensure `insecure_skip_verify: true` is in `/tmp/k3d-registries.yaml` and cluster was created with `--registry-config` |
| DNS doesn't resolve (`ping dev.mclaude.richardmcsong.com` hangs) | ExternalDNS hasn't written A records yet (takes ~1 min after Ingress is created) | `kubectl logs -n external-dns deploy/external-dns`; verify DO API token is valid; retry after 60s |
| Certificate stuck in `Issuing` | DO API token invalid or DNS propagation slow | `kubectl describe certificate mclaude-richardmcsong-wildcard -n mclaude-system`; check cert-manager logs; verify token with `curl -X GET -H "Authorization: Bearer ${DO_TOKEN}" "https://api.digitalocean.com/v2/domains"` |
| Login 405 | Ingress not routing to control-plane | Check `kubectl get ingress -n mclaude-system`; host must be `dev.mclaude.richardmcsong.com` |
| NATS WebSocket fails | `natsUrl` in login response is internal cluster URL | Old binary — SPA falls back to `wss://<origin>/nats` which routes through Traefik correctly |
| `projects` table missing (login succeeds but projects 500s) | pgx `pool.Exec` only runs first statement in multi-statement SQL | Manually create: `kubectl exec -it deploy/mclaude-postgres -n mclaude-system -- psql -U mclaude -c "CREATE TABLE IF NOT EXISTS projects (...)"` |
| devSeed user not created (old binary) | Old ghcr.io `latest` predates devSeed feature | Manually seed: see "Manual DB Seeding" below |
| `helm upgrade/install` blocked by hook | spec-driven-dev hook guards against local deploys | Set `SDD_DEBUG=1` env var (Step 8) |
| `StatefulSet.apps "mclaude-nats" / "mclaude-postgres" is invalid: spec: Forbidden: updates to statefulset spec for fields other than...` | An older install set `persistence.enabled: false`; chart defaults are now `true` and K8s forbids adding `volumeClaimTemplates` in place. | `helm uninstall mclaude -n mclaude-system` (wipes local NATS KV + postgres state — local dev only), then re-run `/deploy-local-preview`. |
| Session-agent: `SSL certificate verification failed` | Corporate proxy intercepts TLS; container lacks CA bundle | Mount corporate CA bundle into container and set `NODE_EXTRA_CA_CERTS` |
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
