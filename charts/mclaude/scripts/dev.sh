#!/usr/bin/env bash
# dev.sh — build images and deploy mclaude to a local k3d cluster.
#
# Usage:
#   ./charts/mclaude/scripts/dev.sh              # full build + deploy
#   ./charts/mclaude/scripts/dev.sh --no-build   # skip docker build
#   ./charts/mclaude/scripts/dev.sh --keep       # don't delete cluster on ctrl-c
#
# Requires: k3d, kubectl, helm, docker on PATH.
# Run from any directory inside the mclaude repo.

set -euo pipefail

# ── path detection ───────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CHART_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"

# Support worktree layout (pre-merge) and flat layout (post-merge).
if [ -d "$REPO_ROOT/worktrees/control-plane/mclaude-control-plane" ]; then
  CP_DIR="$REPO_ROOT/worktrees/control-plane/mclaude-control-plane"
  SPA_DIR="$REPO_ROOT/worktrees/spa/mclaude-web"
  SA_DIR="$REPO_ROOT/worktrees/session-agent/mclaude-session-agent"
else
  CP_DIR="$REPO_ROOT/mclaude-control-plane"
  SPA_DIR="$REPO_ROOT/mclaude-web"
  SA_DIR="$REPO_ROOT/mclaude-session-agent"
fi

CLUSTER="mclaude-dev"
RELEASE="mclaude"
NS="mclaude-system"
BUILD=true

while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-build) BUILD=false; shift ;;
    --keep)     shift ;;   # no-op: dev cluster is never auto-deleted
    *) echo "Unknown arg: $1"; exit 1 ;;
  esac
done

# ── 1. k3d cluster ───────────────────────────────────────────────────────────
if k3d cluster list 2>/dev/null | grep -q "^${CLUSTER} "; then
  echo "→ cluster $CLUSTER already exists, reusing"
else
  echo "→ creating k3d cluster: $CLUSTER"
  k3d cluster create "$CLUSTER" --wait
fi
k3d kubeconfig merge "$CLUSTER" --kubeconfig-merge-default --overwrite

# ── 2. build images ──────────────────────────────────────────────────────────
if [ "$BUILD" = true ]; then
  echo "→ building mclaude-control-plane:dev  ($CP_DIR)"
  docker build -t mclaude-control-plane:dev "$CP_DIR"

  echo "→ building mclaude-spa:dev  ($SPA_DIR)"
  docker build -t mclaude-spa:dev "$SPA_DIR"

  echo "→ building mclaude-session-agent:dev  ($SA_DIR)"
  docker build -t mclaude-session-agent:dev "$SA_DIR"
fi

# ── 3. load images into k3d ─────────────────────────────────────────────────
echo "→ importing images into k3d"
k3d image import \
  mclaude-control-plane:dev \
  mclaude-spa:dev \
  mclaude-session-agent:dev \
  --cluster "$CLUSTER"

# ── 4. namespace + secrets ───────────────────────────────────────────────────
echo "→ namespace $NS"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -

echo "→ secrets"
kubectl create secret generic mclaude-postgres \
  --namespace "$NS" \
  --from-literal=postgres-password="devpassword" \
  --dry-run=client -o yaml | kubectl apply -f -

PG_HOST="${RELEASE}-postgres.${NS}.svc.cluster.local"

kubectl create secret generic mclaude-control-plane \
  --namespace "$NS" \
  --from-literal=database-url="postgres://mclaude:devpassword@${PG_HOST}:5432/mclaude?sslmode=disable" \
  --from-literal=admin-token="dev-admin-token" \
  --from-literal=nats-operator-jwt="dev-stub-jwt" \
  --from-literal=nats-operator-seed="dev-stub-seed" \
  --dry-run=client -o yaml | kubectl apply -f -

# ── 5. helm install ──────────────────────────────────────────────────────────
echo "→ helm upgrade --install $RELEASE"
helm upgrade --install "$RELEASE" "$CHART_DIR" \
  --namespace "$NS" \
  --values "$CHART_DIR/values-dev.yaml" \
  --wait \
  --timeout 120s

# ── 6. print status + access instructions ────────────────────────────────────
echo ""
kubectl get pods -n "$NS"
echo ""
echo "✓ mclaude-dev running. Access via port-forward:"
echo ""
echo "  # Control-plane API"
echo "  kubectl port-forward -n $NS svc/${RELEASE}-control-plane 8080:8080"
echo "  curl http://localhost:8080/version"
echo "  curl http://localhost:8080/health"
echo ""
echo "  # Admin port (metrics + break-glass)"
echo "  kubectl port-forward -n $NS svc/${RELEASE}-control-plane 9090:9090"
echo "  curl -H 'Authorization: Bearer dev-admin-token' http://localhost:9090/admin/users"
echo "  curl http://localhost:9090/metrics"
echo ""
echo "  # SPA"
echo "  kubectl port-forward -n $NS svc/${RELEASE}-spa 3000:8080"
echo "  open http://localhost:3000"
echo ""
echo "  # NATS monitoring"
echo "  kubectl port-forward -n $NS svc/${RELEASE}-nats 8222:8222"
echo "  curl http://localhost:8222/varz"
echo ""
echo "  Teardown: k3d cluster delete $CLUSTER"
