# Tailscale ExternalDNS

## Overview

Preview deployments currently use `sslip.io` hostnames that embed the Tailscale IP
(`preview-{slug}.{ts-ip}.sslip.io`). These only resolve when the device's DNS can reach
sslip.io servers, which fails on some mobile networks and carriers.

This subsystem replaces sslip.io with Tailscale's native DNS: ExternalDNS watches
Ingress resources and writes records directly into the Tailnet via the Tailscale API.
Any device on the Tailnet (phone, laptop, CI runner) resolves preview hostnames
automatically via Tailscale MagicDNS — no sslip.io, no carrier DNS dependency.

## Spec

### Prerequisites

A Kubernetes Secret `tailscale-dns-credentials` in `mclaude-system`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: tailscale-dns-credentials
  namespace: mclaude-system
type: Opaque
stringData:
  TAILSCALE_API_KEY: "<oauth-client-secret>"  # OAuth client with dns:write scope
```

This secret is created once manually and never managed by Helm. It must exist before
ExternalDNS is deployed.

### ExternalDNS deployment

A standalone `external-dns` Deployment in `mclaude-system`:

- Image: `registry.k8s.io/external-dns/external-dns:v0.14.2`
- Provider: `tailscale`
- Sources: `ingress`
- Policy: `upsert-only` (never delete records automatically)
- Interval: `1m`
- Env: `EXTERNAL_DNS_TAILSCALE_API_KEY` from `tailscale-dns-credentials` Secret

Deployed as a standalone YAML manifest at `charts/external-dns/external-dns.yaml`
(not part of the mclaude Helm chart, since it is a singleton for the whole cluster).
Applied once via `kubectl apply` and committed to the repo for GitOps tracking.

### Ingress annotation

When `ingress.externalDns.enabled` is `true` (the default), the Helm chart adds this
annotation to the Ingress:

```
external-dns.alpha.kubernetes.io/hostname: {{ .Values.ingress.host }}
```

ExternalDNS picks up the annotation and creates an A record pointing to the Tailscale
IP of the host running the ingress controller.

### Hostname format

Preview hostnames use the short form — just the hostname label with no IP:

```
preview-{branch-slug}
```

ExternalDNS registers this as an A record in the Tailnet. Tailscale MagicDNS resolves
`preview-{branch-slug}` (or `preview-{branch-slug}.{tailnet}.ts.net`) on any device
connected to the Tailnet.

### deploy-preview.yml changes

The deploy job computes `PREVIEW_HOST` as the short hostname, not sslip.io:

```
PREVIEW_HOST="preview-${BRANCH_SLUG}"
```

The `ingress.host` Helm value is set to `${PREVIEW_HOST}`. ExternalDNS registers it.
The preview URL logged at the end of the workflow is `http://${PREVIEW_HOST}`.

### Tailscale IP resolution

ExternalDNS needs to know which IP to register. The Tailscale provider resolves the
node's Tailscale IP automatically from the API using the node's hostname. No IP is
hardcoded in the workflow.

## Component Responsibilities

| Component | Responsibility |
|-----------|---------------|
| `charts/external-dns/external-dns.yaml` | Standalone ExternalDNS Deployment + RBAC in mclaude-system |
| `charts/mclaude/templates/ingress.yaml` | Add ExternalDNS hostname annotation when `ingress.externalDns.enabled` |
| `charts/mclaude/values.yaml` | `ingress.externalDns.enabled` defaults to `true` |
| `.github/workflows/deploy-preview.yml` | Compute `PREVIEW_HOST` as short slug, not sslip.io |

## Failure modes

- **ExternalDNS pod not running**: DNS record not created; preview still accessible via IP but not by name. CI logs the preview URL; operator can access directly via `kubectl port-forward` while troubleshooting.
- **OAuth token expired/invalid**: ExternalDNS logs auth error; existing records persist (upsert-only policy). Renew the Secret to restore.
- **Record propagation delay**: MagicDNS updates propagate within ~60s after ExternalDNS writes the record. CI does not wait for propagation.
