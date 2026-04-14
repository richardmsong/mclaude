# Preview DNS via CoreDNS

## Overview

Preview deployments need hostnames that resolve on any Tailnet device, including mobile
on cellular. This subsystem runs a CoreDNS container on the k3d host, bound to the
host's Tailscale IP. It serves zone `mclaude.internal.` with a wildcard A record pointing
to that same IP. Tailscale split DNS routes all `*.mclaude.internal` queries from any
Tailnet device to this server — no public DNS dependency, no wildcard DNS workarounds.

## Spec

### CoreDNS container (host)

A Docker container named `coredns-preview` runs on the k3d host:

- Image: `coredns/coredns:1.12.0`
- Published ports: `{TAILSCALE_IP}:53:53/udp` and `{TAILSCALE_IP}:53:53/tcp`
- Restart policy: `unless-stopped`
- Deployed by `charts/coredns-preview/deploy.sh`

The deploy script:
1. Reads the host's Tailscale IP via `tailscale ip -4`
2. Substitutes `TAILSCALE_IP` into `Corefile.template` to produce a rendered `Corefile`
3. Stops and removes any existing `coredns-preview` container (idempotent)
4. Starts the container, bind-mounting the rendered `Corefile` and zone file

### Zone file

`charts/coredns-preview/mclaude.internal.zone`:
```
$ORIGIN mclaude.internal.
@   300 IN SOA dns.mclaude.internal. admin.mclaude.internal. 1 3600 900 604800 300
@   300 IN NS  dns.mclaude.internal.
dns 300 IN A   TAILSCALE_IP_PLACEHOLDER
*   300 IN A   TAILSCALE_IP_PLACEHOLDER
```

The deploy script replaces `TAILSCALE_IP_PLACEHOLDER` with the actual Tailscale IP.

### Corefile

`charts/coredns-preview/Corefile.template`:
```
mclaude.internal. {
    file /etc/coredns/mclaude.internal.zone
    log
    errors
}
```

No substitution needed in the Corefile itself; substitution happens in the zone file.

### Deploy script

`charts/coredns-preview/deploy.sh`:
- Idempotent: safe to run on every push
- Reads TAILSCALE_IP via `tailscale ip -4`
- Renders zone file with TAILSCALE_IP substituted
- Stops+removes `coredns-preview` if running
- Starts fresh container

### Tailscale split DNS (one-time manual setup)

In the Tailscale admin console → DNS → Nameservers → Add nameserver → Custom:
- IP: `{TAILSCALE_IP}` (the k3d host's Tailscale IP)
- Restrict to domain: `mclaude.internal`

This routes all `*.mclaude.internal` DNS queries from every Tailnet device (including
mobile on cellular) to the CoreDNS container via Tailscale. No public DNS involved.

### Ingress hostname format

Preview Ingresses use the hostname:
```
preview-{branch-slug}.mclaude.internal
```

`deploy-preview.yml` sets `ingress.host` to this value. CoreDNS resolves it to the
host's Tailscale IP; Traefik routes by hostname to the correct preview release.

### deploy-preview.yml

`PREVIEW_HOST` is computed as:
```bash
PREVIEW_HOST="preview-${BRANCH_SLUG}.mclaude.internal"
```

No Tailscale IP lookup, no external DNS services.

### Helm chart changes

- `charts/mclaude/values.yaml`: remove `ingress.externalDns` section entirely
- `charts/mclaude/templates/ingress.yaml`: remove ExternalDNS hostname annotation

## Component Responsibilities

| Component | Responsibility |
|-----------|---------------|
| `charts/coredns-preview/deploy.sh` | Idempotent script: reads Tailscale IP, renders zone file, starts CoreDNS container |
| `charts/coredns-preview/Corefile.template` | CoreDNS config: serves `mclaude.internal.` zone from file |
| `charts/coredns-preview/mclaude.internal.zone` | DNS zone: wildcard A record → `TAILSCALE_IP_PLACEHOLDER` |
| `charts/mclaude/templates/ingress.yaml` | Ingress with host from values; no ExternalDNS annotation |
| `charts/mclaude/values.yaml` | `ingress.externalDns` section removed |
| `.github/workflows/deploy-preview.yml` | `PREVIEW_HOST=preview-${BRANCH_SLUG}.mclaude.internal` |

## Failure modes

- **CoreDNS container stopped**: DNS fails for preview hostnames. Restart with `charts/coredns-preview/deploy.sh`. The container has `--restart unless-stopped` so it survives reboots automatically.
- **Port 53 conflict on host**: Another process holds port 53. Check with `lsof -i UDP:53`. On macOS, disable `mDNSResponder` DNS proxy if needed, or stop the conflicting service.
- **Tailscale split DNS not configured**: Queries for `*.mclaude.internal` fall through to public DNS and fail. One-time setup in Tailscale admin required per device fleet.
- **Tailscale IP changes**: Update Tailscale split DNS nameserver entry and re-run `deploy.sh`.
