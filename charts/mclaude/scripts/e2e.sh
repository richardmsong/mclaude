#!/usr/bin/env bash
# mclaude Helm E2E Test
# Installs the chart into an existing cluster, runs helm test, then cleans up.
# Usage:
#   ./scripts/e2e.sh                        # use current kubectl context
#   ./scripts/e2e.sh --context k3d-cluster  # switch to a specific context
#   ./scripts/e2e.sh --keep                 # skip cleanup after test
# Requires: helm, kubectl on PATH.
set -euo pipefail

RELEASE=mclaude-test
NS=mclaude-system
CHART="$(cd "$(dirname "$0")/.." && pwd)"
KUBE_CONTEXT=""
KEEP=false

# Parse args
while [[ $# -gt 0 ]]; do
  case "$1" in
    --context) KUBE_CONTEXT="$2"; shift 2 ;;
    --keep)    KEEP=true; shift ;;
    *)         echo "Unknown arg: $1"; exit 1 ;;
  esac
done

cleanup() {
  if [[ "$KEEP" == "true" ]]; then
    echo "→ --keep: skipping cleanup (release=$RELEASE namespace=$NS)"
    return
  fi
  echo "→ cleanup"
  helm uninstall "$RELEASE" -n "$NS" 2>/dev/null || true
  kubectl delete namespace "$NS" --wait=false 2>/dev/null || true
}
trap cleanup EXIT

# ── 1. Context ───────────────────────────────────────────────────────────────
if [[ -n "$KUBE_CONTEXT" ]]; then
  echo "→ switching to context: $KUBE_CONTEXT"
  kubectl config use-context "$KUBE_CONTEXT"
fi
echo "→ cluster: $(kubectl config current-context)"
kubectl get nodes --no-headers | awk '{print "  " $1 " " $2}'

# ── 2. Namespace + secrets ───────────────────────────────────────────────────
echo "→ creating namespace $NS"
# values-e2e.yaml sets namespace.create: false so Helm never touches this
# namespace (Helm v4 SSA rejects managedFields on pre-annotated namespaces).
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -

echo "→ creating secrets (stub values for e2e)"
kubectl create secret generic mclaude-postgres \
  --namespace "$NS" \
  --from-literal=postgres-password="e2e-test-password" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic mclaude-control-plane \
  --namespace "$NS" \
  --from-literal=database-url="postgres://mclaude:e2e-test-password@${RELEASE}-postgres.${NS}.svc.cluster.local:5432/mclaude?sslmode=disable" \
  --from-literal=admin-token="e2e-admin-token" \
  --from-literal=nats-operator-jwt="stub-jwt" \
  --from-literal=nats-operator-seed="stub-seed" \
  --dry-run=client -o yaml | kubectl apply -f -

# ── 3. Helm install ──────────────────────────────────────────────────────────
echo "→ helm upgrade --install $RELEASE"
helm upgrade --install "$RELEASE" "$CHART" \
  --namespace "$NS" \
  --values "$CHART/values-e2e.yaml" \
  --wait \
  --timeout 120s

echo "→ pods:"
kubectl get pods -n "$NS"

# ── 4. Helm test ─────────────────────────────────────────────────────────────
echo "→ helm test"
helm test "$RELEASE" --namespace "$NS" --timeout 60s

echo ""
echo "✓ e2e passed"
