#!/usr/bin/env bash
# deploy.sh — idempotent CoreDNS preview DNS setup.
# Reads the host's Tailscale IP, renders the zone file, then (re)starts the
# coredns-preview container bound to that IP on port 53 UDP+TCP.
#
# Usage: charts/coredns-preview/deploy.sh
# Run from anywhere; paths are resolved relative to this script.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONTAINER_NAME="coredns-preview"
IMAGE="coredns/coredns:1.12.0"

# -- Resolve Tailscale IP ----------------------------------------------------
TAILSCALE_IP="$(tailscale ip -4 2>/dev/null)" || {
  echo "ERROR: tailscale ip -4 failed — is Tailscale running?" >&2
  exit 1
}
echo "Tailscale IP: ${TAILSCALE_IP}"

# -- Check port 53 availability -----------------------------------------------
if lsof -iTCP:53 -iUDP:53 -sTCP:LISTEN -n -P 2>/dev/null | grep -v "${CONTAINER_NAME}" | grep -qv "^COMMAND"; then
  echo "WARNING: something else may be using port 53 — check with: lsof -i UDP:53" >&2
fi

# -- Render zone file ---------------------------------------------------------
ZONE_SRC="${SCRIPT_DIR}/mclaude.internal.zone"
ZONE_RENDERED="$(mktemp)"
sed "s/TAILSCALE_IP_PLACEHOLDER/${TAILSCALE_IP}/g" "${ZONE_SRC}" > "${ZONE_RENDERED}"
echo "Zone file rendered to: ${ZONE_RENDERED}"

# -- Stop and remove existing container (idempotent) -------------------------
if docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
  echo "Stopping existing ${CONTAINER_NAME}..."
  docker stop "${CONTAINER_NAME}" >/dev/null 2>&1 || true
  docker rm   "${CONTAINER_NAME}" >/dev/null 2>&1 || true
fi

# -- Start CoreDNS ------------------------------------------------------------
docker run -d \
  --name "${CONTAINER_NAME}" \
  --restart unless-stopped \
  -p "${TAILSCALE_IP}:53:53/udp" \
  -p "${TAILSCALE_IP}:53:53/tcp" \
  -v "${SCRIPT_DIR}/Corefile.template:/etc/coredns/Corefile:ro" \
  -v "${ZONE_RENDERED}:/etc/coredns/mclaude.internal.zone:ro" \
  "${IMAGE}" \
  -conf /etc/coredns/Corefile

echo "Started ${CONTAINER_NAME} (${IMAGE}) bound to ${TAILSCALE_IP}:53"
echo ""
echo "Next: configure Tailscale split DNS in the admin console:"
echo "  DNS → Nameservers → Add nameserver → Custom"
echo "  IP: ${TAILSCALE_IP}"
echo "  Restrict to domain: mclaude.internal"
