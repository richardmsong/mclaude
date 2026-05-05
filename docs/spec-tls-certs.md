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

One wildcard cert per cluster, owned by the `mclaude-cp` Helm chart per ADR-0097. Rendered when `tls.certManager.enabled` is `true` (overlays like `values-k3d-ghcr.yaml` flip it on; AKS/airgap leave it at the default `false`). Carries `helm.sh/resource-policy: keep` so a `helm uninstall` does **not** delete the Certificate or its derived TLS Secret — the cert survives wipe/reinstall cycles, and Let's Encrypt is not asked to re-issue on each install (LE rate-limit safe).

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: {{ include "mclaude-cp.fullname" . }}-wildcard   # → mclaude-cp-wildcard
  namespace: {{ .Release.Namespace }}                    # → mclaude-system
  annotations:
    helm.sh/resource-policy: keep
spec:
  secretName: {{ (index .Values.ingress.tls 0).secretName }}   # single source of truth
  issuerRef:
    kind: {{ .Values.tls.certManager.issuerRef.kind | default "ClusterIssuer" }}
    name: {{ required "tls.certManager.issuerRef.name is required when tls.certManager.enabled" .Values.tls.certManager.issuerRef.name }}
  commonName: {{ required "tls.certManager.commonName is required when tls.certManager.enabled" .Values.tls.certManager.commonName | quote }}
  dnsNames:
    {{- range (required "tls.certManager.dnsNames is required when tls.certManager.enabled" .Values.tls.certManager.dnsNames) }}
    - {{ . | quote }}
    {{- end }}
  duration: {{ .Values.tls.certManager.duration | default "2160h" }}    # 90 days (LE default)
  renewBefore: {{ .Values.tls.certManager.renewBefore | default "720h" }} # 30 days before expiry
```

The `required` directives fail `helm template` / `helm install` fast with a clear error if `tls.certManager.enabled=true` but configuration is incomplete (e.g., empty `commonName`).

**Resource name**: per ADR-0097, the chart-managed Certificate is `{release}-wildcard` (`mclaude-cp-wildcard` for the default release). The pre-ADR-0097 hardcoded name `mclaude-richardmcsong-wildcard` (applied manually by `/deploy-local-preview`) is no longer in use. Clusters that have the legacy CR must follow the migration runbook in ADR-0097: delete the legacy Certificate first (cert-manager won't share Secret ownership), then `helm install mclaude-cp` to render the new one.

The resulting Secret (e.g. `mclaude-richardmcsong-tls` for the dev overlay) is consumed by every Ingress in `mclaude-system` (main + NATS, across all Helm releases in that namespace) by reference name. The Secret is created/updated by cert-manager based on the Certificate; no Helm release tracks the Secret directly, so it persists naturally regardless of helm uninstall behaviour.

**Adoption semantics**: on `helm install`/`helm upgrade`, helm checks whether `mclaude-cp-wildcard` exists. If it does (e.g., from a prior install that was helm-uninstalled), helm adopts it as a no-op — cert-manager does not re-issue, no Let's Encrypt rate-limit slot is consumed. If it doesn't exist, helm creates it and cert-manager issues fresh.

**Externally-managed TLS path**: when `tls.certManager.enabled=false` (default), the chart skips the Certificate template entirely. The operator pre-creates a TLS Secret out-of-band (Azure Application Gateway export, corporate CA, etc.) and references it via `ingress.tls[0].secretName`. The chart's Ingress resources reference whatever name is supplied; cert-manager is not involved.

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
- `helm install mclaude-cp --wait` returns when Pods/Services are Ready, but does **not** wait for cert-manager to fulfill the Certificate (cert-manager is a separate controller; helm has no insight into its reconcile loop). The Certificate-Ready wait must be explicit:
  ```bash
  kubectl wait --for=condition=Ready certificate/mclaude-cp-wildcard \
    -n mclaude-system --timeout=5m
  ```
  Per ADR-0097 the Certificate is helm-managed (rendered into the chart); `/deploy-local-preview` no longer pre-applies it but keeps the post-install wait above. On subsequent installs the Certificate is already Ready (`helm.sh/resource-policy: keep` preserves it across uninstalls), so the wait is a no-op.

## Component Responsibilities

| Component | Responsibility |
|-----------|---------------|
| `/deploy-local-preview` skill | Bootstraps cert-manager, ExternalDNS, DO-token Secret, and ClusterIssuer (one-time per cluster, infrastructure layer). Per ADR-0097, the Certificate CR is **not** owned by this skill — the `mclaude-cp` Helm chart owns it via a `helm.sh/resource-policy: keep` template gated by `tls.certManager.enabled`. After cluster bootstrap, the skill runs `helm install mclaude-cp` with the values overlay that opts in (`values-k3d-ghcr.yaml`). |
| `.github/workflows/deploy-preview.yml` | Assumes cert-manager + ExternalDNS already exist in the cluster (bootstrapped once by `/deploy-local-preview`). Runs `helm upgrade --install mclaude-preview-${BRANCH_SLUG} charts/mclaude` with the preview hostnames, the Tailscale IP as the ExternalDNS target, and the shared Secret `mclaude-richardmcsong-tls` in the `tls[0]` stanza. |
| `charts/mclaude/templates/ingress.yaml` | Main Ingress. Emits `external-dns.alpha.kubernetes.io/hostname` + `target` annotations when `ingress.externalDnsTarget` is set. Consumes `ingress.tls` from values (caller supplies `mclaude-richardmcsong-tls` for k3d/CI). |
| `charts/mclaude/templates/nats-ws-ingress.yaml` | NATS WebSocket Ingress. Same annotations, `hostname` is `ingress.natsHost`. Shares the `ingress.tls` stanza. |
| `charts/mclaude/values.yaml` | `ingress.tls` default is `[]` (non-k3d environments supply their own or leave empty); `ingress.externalDnsTarget` defaults to empty. k3d and CI preview pass both via `values-k3d-ghcr.yaml` and `--set` flags respectively. |
| Bitwarden | Source of truth for the DO API token. |

## Failure modes

- **`Certificate` stuck in `Issuing`** (any cause) — `kubectl describe certificate mclaude-cp-wildcard -n mclaude-system` surfaces cert-manager's detailed status (Order, Challenge, current message). Common root causes: wrong `issuerRef.name`, hostname not in the DO zone, ClusterIssuer not Ready, DNS-01 challenge stuck waiting on TXT propagation, or an Order in error state. Walk the linked CRs (`kubectl get order -n mclaude-system`, `kubectl get challenge -n mclaude-system`) for the specific failure.
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
- **`tls.certManager.enabled=true` with missing required fields** (per ADR-0097) —
  `helm install`/`helm template` fails fast with the exact missing values key
  named in the error (e.g. `Error: execution error at (mclaude-cp/templates/certificate.yaml:14:21): tls.certManager.commonName is required when tls.certManager.enabled`).
  No partial install; operator fixes the overlay and retries.
- **`helm upgrade` with Certificate spec changes** (per ADR-0097, e.g. adding a
  new entry to `tls.certManager.dnsNames`) — helm updates the kept resource;
  cert-manager re-issues on material spec changes. The `keep` annotation only
  prevents *delete*, not *update*. A re-issuance burns one Let's Encrypt rate-
  limit slot (50/week/registered domain). Avoid editing dnsNames frequently.
- **`helm uninstall mclaude-cp` then `helm install` again** (per ADR-0097) — Certificate
  survives uninstall via `helm.sh/resource-policy: keep`; the derived TLS Secret
  also persists (cert-manager-managed, not helm-tracked). Reinstall adopts the
  existing Certificate as a no-op — no LE re-issuance, no rate-limit cost.
  This is the default outcome on dev wipe/reinstall cycles.
