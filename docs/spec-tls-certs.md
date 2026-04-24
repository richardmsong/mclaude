# TLS Certificates

## Overview

mclaude uses a single wildcard TLS certificate for `*.mclaude.richardmcsong.com`
(and the apex) issued by Let's Encrypt production. The certificate is obtained
and renewed by **cert-manager** running in the k3d cluster using the
**DigitalOcean DNS-01 solver**. DNS records for every Ingress host are created
and kept current at DigitalOcean by **ExternalDNS** also running in the
cluster. Both controllers read their credentials from a shared
`digitalocean-api-token` Secret copied into both the `cert-manager` and
`external-dns` namespaces.

This subsystem replaces the old self-signed-for-`.local` + plain-HTTP-for-
`.internal` setup and the CoreDNS-on-host / Tailscale-split-DNS plumbing.

## Spec

### Domain + DNS

- Zone: `richardmcsong.com` hosted at **DigitalOcean**.
- All mclaude hostnames live under `*.mclaude.richardmcsong.com` — one label
  deep so a single wildcard cert covers everything.
- DNS is **fully public**. The A records resolve to the k3d host's private
  Tailscale IP; only Tailnet devices can actually route to it. No split DNS,
  no CoreDNS on the host.

### Hostnames

| Hostname | Purpose |
|----------|---------|
| `dev.mclaude.richardmcsong.com` | Main SPA + API for `/deploy-local-preview` |
| `dev-nats.mclaude.richardmcsong.com` | NATS WebSocket for `/deploy-local-preview` |
| `preview-<branch-slug>.mclaude.richardmcsong.com` | Main SPA + API for CI preview deploys |
| `nats-preview-<branch-slug>.mclaude.richardmcsong.com` | NATS WebSocket for CI preview deploys |
| `mclaude.richardmcsong.com` (apex) | Reserved; cert SAN includes it; no Ingress binds to it today |

All CI preview deploys target the **same k3d cluster** as local dev (via a
self-hosted GitHub Actions runner on the same Mac). One cert, one ExternalDNS
controller, one txtOwnerId.

### cert-manager

- Installed via upstream chart: `jetstack/cert-manager`.
- Namespace: `cert-manager`.
- Installed by `/deploy-local-preview` (and the CI workflow's equivalent step)
  using:
  ```bash
  helm repo add jetstack https://charts.jetstack.io
  helm upgrade --install cert-manager jetstack/cert-manager \
    -n cert-manager --create-namespace \
    --set installCRDs=true --wait
  ```
- The `--wait` flag blocks until the CRDs are registered, so the subsequent
  `kubectl apply` of ClusterIssuer / Certificate CRs never races.

### ClusterIssuer

A single cluster-scoped issuer:

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: mclaude-letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: <user email>
    privateKeySecretRef:
      name: mclaude-letsencrypt-prod-account
    solvers:
      - dns01:
          digitalocean:
            tokenSecretRef:
              name: digitalocean-api-token
              key: access-token
```

No staging issuer is created. LE production is the only path.

### Certificate CR

One wildcard cert, in `mclaude-system`:

```yaml
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
  duration: 2160h   # 90 days (LE default)
  renewBefore: 720h # 30 days before expiry
```

The resulting Secret `mclaude-richardmcsong-tls` is consumed by every Ingress
in `mclaude-system` (main + NATS, across all Helm releases in that namespace).

### ExternalDNS

- Installed via upstream chart: `external-dns/external-dns`.
- Namespace: `external-dns`.
- Installed by `/deploy-local-preview` (and CI's equivalent) using:
  ```bash
  helm repo add external-dns https://kubernetes-sigs.github.io/external-dns/
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

Key settings:
- `policy: sync` — records are created, updated, **and deleted** to match the
  current set of Ingresses. Deleting a preview Helm release removes its A
  records automatically.
- `txtOwnerId: mclaude-k3d` — ExternalDNS writes a sibling TXT record for
  every A record marking ownership. Prevents conflicts if another ExternalDNS
  instance ever writes to the same zone.
- `sources: [ingress]` — only Ingress objects trigger record management.
  Services (LoadBalancer) are not watched.

### Ingress annotations

Every Ingress that needs DNS automation (both mclaude Ingresses — main and
NATS WS) emits these annotations:

```yaml
external-dns.alpha.kubernetes.io/hostname: <ingress host>
external-dns.alpha.kubernetes.io/target: <k3d host Tailscale IP>
```

`target` is set explicitly because k3d's built-in Traefik LoadBalancer
resolves to `127.0.0.1` on the host — wrong for anyone else on the Tailnet.
The skill/workflow passes the Tailscale IP as `ingress.externalDnsTarget`
when running `helm upgrade --install`.

### DigitalOcean API token

- A single API token with **write scope on zone `richardmcsong.com` only**.
- Stored in Bitwarden as the source of truth ("DigitalOcean API token —
  richardmcsong.com zone edit").
- Pulled at deploy time by `bw get password` and written to a K8s Secret
  `digitalocean-api-token` (key: `access-token`) in **both** the
  `cert-manager` and `external-dns` namespaces. Each controller reads the
  Secret in its own namespace.

### Renewal

- cert-manager monitors the Certificate CR. ~30 days before expiry
  (`renewBefore: 720h`) it triggers a new DNS-01 challenge.
- The TXT record `_acme-challenge.mclaude.richardmcsong.com` is written via
  DO API, validated by LE, then deleted.
- The new cert replaces `tls.crt`/`tls.key` in the existing Secret. Traefik
  watches the Secret and hot-reloads without downtime.
- A successful renewal does not require any human action.

### First-time issuance expected duration

- cert-manager install: ~30s (CRD registration + deployments ready).
- ExternalDNS install: ~15s.
- First cert issuance: ~60–180s (DO TXT propagation + LE validation).
- `/deploy-local-preview` should `kubectl wait --for=condition=Ready
  certificate/mclaude-richardmcsong-wildcard -n mclaude-system --timeout=5m`
  before proceeding to the main `helm install mclaude`.

## Component Responsibilities

| Component | Responsibility |
|-----------|---------------|
| `/deploy-local-preview` skill | Bootstraps cert-manager, ExternalDNS, DO-token Secret, ClusterIssuer, and Certificate CR. Waits for the Certificate to become Ready. Then installs the mclaude chart with `ingress.externalDnsTarget=$TS_IP`. |
| `.github/workflows/deploy-preview.yml` | Assumes cert-manager + ExternalDNS already exist in the cluster (bootstrapped once by `/deploy-local-preview`). Runs `helm upgrade --install mclaude-preview-${BRANCH_SLUG} charts/mclaude` with the preview hostnames, the Tailscale IP as the ExternalDNS target, and the shared Secret `mclaude-richardmcsong-tls` in the `tls[0]` stanza. |
| `charts/mclaude/templates/ingress.yaml` | Main Ingress. Emits `external-dns.alpha.kubernetes.io/hostname` + `target` annotations when `ingress.externalDnsTarget` is set. Consumes `ingress.tls` from values (caller supplies `mclaude-richardmcsong-tls` for k3d/CI). |
| `charts/mclaude/templates/nats-ws-ingress.yaml` | NATS WebSocket Ingress. Same annotations, `hostname` is `ingress.natsHost`. Shares the `ingress.tls` stanza. |
| `charts/mclaude/values.yaml` | `ingress.tls` default is `[]` (non-k3d environments supply their own or leave empty); `ingress.externalDnsTarget` defaults to empty. k3d and CI preview pass both via `values-k3d-ghcr.yaml` and `--set` flags respectively. |
| Bitwarden | Source of truth for the DO API token. |

## Failure modes

- **DO API token invalid/revoked** — `Certificate` stuck Issuing; ExternalDNS
  logs 401. Rotate the token in Bitwarden, re-run the skill, both controllers
  pick up the new Secret.
- **Let's Encrypt rate limit (50/week/registered domain)** — hit only by
  repeated back-to-back cluster-destroys. Point the `ClusterIssuer` at LE
  staging temporarily to unblock, then switch back.
- **DO DNS propagation slow** — cert-manager retries with exponential backoff.
  If persistent, raise `solver.dns01.digitalocean.propagationTimeout` on the
  ClusterIssuer.
- **Tailscale IP changes** — the skill's next run picks up the new IP and
  passes it as `ingress.externalDnsTarget`; ExternalDNS updates records.
  Until then, subdomains resolve to the old (unreachable) IP.
- **ExternalDNS `sync` deletes records after a bad `helm uninstall`** —
  re-running `/deploy-local-preview` (or `helm install` in CI) recreates the
  Ingress, and ExternalDNS re-writes the records within its reconcile
  interval (~1 minute).
- **Corporate HTTPS-intercepting proxy MITMs the domain** — browser sees the
  corp CA cert instead of LE. Out of scope; user's Mac must bypass the proxy
  for this domain via PAC.
