---
name: deploy-local-preview
description: Stand up a local k3d cluster and deploy mclaude to it. Builds images locally if ghcr.io images are unavailable. HTTPS via Traefik with a self-signed wildcard cert. Idempotent — safe to re-run.
user_invocable: true
---

# Deploy Local Preview (k3d)

Deploys mclaude to a local k3d cluster. Tries ghcr.io images first, falls back to local Docker builds. HTTPS via Traefik with a self-signed wildcard cert for `*.mclaude.local`. DNS via CoreDNS inside the cluster, surfaced to the host via NodePort on UDP 5053.

**Ingress URL**: `https://dev.mclaude.local`

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
which bw                     # bitwarden CLI (for OAuth token)
docker info                  # Docker Desktop must be running
gh auth status               # must be authenticated
bw status                    # must be unlocked
```

Also check that port 53 UDP is free on the host (used for DNS NodePort):
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
    --port "443:443@loadbalancer" \
    --port "53:30053/udp@server:0" \
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

### Step 4 — TLS certificate for *.mclaude.local

Generate a self-signed wildcard cert and install it as the default Traefik TLS cert:

```bash
# Generate self-signed wildcard cert (valid 10 years)
openssl req -x509 -nodes -days 3650 \
  -newkey rsa:2048 \
  -keyout /tmp/mclaude-local.key \
  -out /tmp/mclaude-local.crt \
  -subj "/CN=*.mclaude.local" \
  -addext "subjectAltName=DNS:*.mclaude.local,DNS:mclaude.local"

# Create K8s TLS secret
kubectl create secret tls mclaude-local-tls \
  --namespace mclaude-system \
  --cert=/tmp/mclaude-local.crt \
  --key=/tmp/mclaude-local.key \
  --dry-run=client -o yaml | kubectl apply -f -

# Also install the cert in kube-system for Traefik's default TLS store
kubectl create secret tls mclaude-local-tls \
  --namespace kube-system \
  --cert=/tmp/mclaude-local.crt \
  --key=/tmp/mclaude-local.key \
  --dry-run=client -o yaml | kubectl apply -f -

# Set as Traefik's default TLS cert via TLSStore CRD
kubectl apply -f - <<'EOF'
apiVersion: traefik.io/v1alpha1
kind: TLSStore
metadata:
  name: default
  namespace: kube-system
spec:
  defaultCertificate:
    secretName: mclaude-local-tls
EOF
```

Trust the cert on macOS (tell the user to run this themselves — requires sudo):

```bash
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain /tmp/mclaude-local.crt
```

> **Why self-signed**: k3d is local-only. The cert is trusted via macOS Keychain so browsers don't warn. The wildcard covers `dev.mclaude.local`, `dev-nats.mclaude.local`, and any future subdomains.

### Step 5 — CoreDNS custom zone for *.mclaude.local

Add a custom zone to the cluster's CoreDNS so `*.mclaude.local` resolves to the host's Tailscale IP:

```bash
TS_IP=$(tailscale ip -4)

kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: coredns-custom
  namespace: kube-system
data:
  mclaude.local.server: |
    mclaude.local. {
        template IN A {
            answer "{{ .Name }} 60 IN A ${TS_IP}"
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

### Step 6 — Tailscale split DNS (one-time)

The cluster's CoreDNS is exposed on host port 53. Configure Tailscale split DNS so all tailnet devices (including iPhones) resolve `*.mclaude.local` automatically:

- Tailscale admin console → DNS → Nameservers → Add custom nameserver
- IP: `<tailscale-ip>` (output of `tailscale ip -4`)
- Restrict to domain: `mclaude.local`

Verify:
```bash
dig @$(tailscale ip -4) dev.mclaude.local +short   # should return Tailscale IP
```

**Enterprise setup** (no Tailscale): DNS is handled by the relay/gateway — not covered here.

> **Why `.local`**: The corporate PAC file (`corporate-proxy.pac`) explicitly bypasses the proxy for `shExpMatch(host, "*.local")`. Using `.internal` or `.mclaude.internal` goes through the proxy and the browser can't reach the cluster.

### Step 7 — Build images locally (if ghcr.io images are unavailable)

Try pulling from ghcr.io first. If the images don't exist (no packages published yet), build locally and import into k3d:

```bash
REPO_ROOT=$(git rev-parse --show-toplevel)
CP_DIR="$REPO_ROOT/mclaude-control-plane"
SPA_DIR="$REPO_ROOT/mclaude-web"
SA_DIR="$REPO_ROOT/mclaude-session-agent"

# Try ghcr.io pull — if ANY image fails, build all locally
if ! docker pull ghcr.io/mclaude-project/mclaude-control-plane:latest 2>/dev/null || \
   ! docker pull ghcr.io/mclaude-project/mclaude-spa:latest 2>/dev/null || \
   ! docker pull ghcr.io/mclaude-project/mclaude-session-agent:latest 2>/dev/null; then
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

### Step 8 — Helm install

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

Build the helm install command. TLS ingress annotations and the TLS secret name are in `values-k3d-ghcr.yaml` — no `--set` needed for those. If local images were built, override the image tags:

```bash
HELM_ARGS=(
  -n mclaude-system
  -f charts/mclaude/values-k3d-ghcr.yaml
  --set "controlPlane.devOAuthToken=${OAUTH_TOKEN}"
  --wait --timeout 5m
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

LOCAL_DEPLOY=1 helm upgrade --install mclaude ./charts/mclaude "${HELM_ARGS[@]}"
```

`LOCAL_DEPLOY=1` is required — the pre-tool-use hook blocks `helm upgrade/install` unless this is set (CI-only guard).

> **Why this matters**: without `devOAuthToken`, the session-agent pod has no Claude credentials. It receives messages and silently does nothing. The token flows: Helm → `DEV_OAUTH_TOKEN` env on control-plane → reconciler writes `oauth-token` key into `user-secrets` Secret → session-agent mounts it at `/home/node/.user-secrets/oauth-token`.

### Step 9 — Wait for pods

```bash
kubectl rollout status deployment/mclaude-control-plane -n mclaude-system --timeout=120s
kubectl rollout status deployment/mclaude-spa -n mclaude-system --timeout=120s
kubectl get pods -n mclaude-system
```

All pods should show `1/1 Running`.

### Step 10 — Verify

```bash
# Ingress resolves (use -k if cert not yet trusted)
curl -s https://dev.mclaude.local/healthz   # should return 200 from control-plane

# SPA
open https://dev.mclaude.local
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
| DNS doesn't resolve (`ping dev.mclaude.local` hangs) | `/etc/resolver/mclaude.local` missing or wrong port | Step 6: check file exists with `cat /etc/resolver/mclaude.local`; dig `@127.0.0.1 -p 5053 dev.mclaude.local` to test CoreDNS directly |
| Browser can't connect despite DNS working | Corporate proxy intercepting (wrong TLD) | Ensure using `.local` not `.internal`; `.local` is already in PAC bypass list |
| Login 405 | Ingress not routing to control-plane | Check `kubectl get ingress -n mclaude-system`; host must be `dev.mclaude.local` |
| NATS WebSocket fails | `natsUrl` in login response is internal cluster URL | Old binary — SPA falls back to `wss://<origin>/nats` which routes through Traefik correctly |
| `projects` table missing (login succeeds but projects 500s) | pgx `pool.Exec` only runs first statement in multi-statement SQL | Manually create: `kubectl exec -it deploy/mclaude-postgres -n mclaude-system -- psql -U mclaude -c "CREATE TABLE IF NOT EXISTS projects (...)"` |
| devSeed user not created (old binary) | Old ghcr.io `latest` predates devSeed feature | Manually seed: see "Manual DB Seeding" below |
| `helm upgrade/install` blocked by hook | Pre-tool-use hook guards against local deploys | Set `LOCAL_DEPLOY=1` env var (Step 8) |
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
