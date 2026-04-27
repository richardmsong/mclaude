# ADR: Let's Encrypt TLS on *.mclaude.richardmcsong.com

**Status**: implemented
**Status history**:
- 2026-04-22: draft
- 2026-04-22: accepted — paired with docs/spec-tls-certs.md (new), docs/charts-mclaude/spec-helm.md, docs/mclaude-control-plane/spec-control-plane.md, docs/feature-list.md, docs/_sidebar.md, docs/spec-doc-layout.md; docs/spec-tailscale-dns.md deleted
- 2026-04-26: implemented — all scope CLEAN

## Overview

Replace the self-signed wildcard cert for `*.mclaude.local` (local dev) and the
plain-HTTP preview deployments on `*.mclaude.internal` (Tailnet) with a single
wildcard TLS certificate issued by Let's Encrypt under the user-owned domain
`richardmcsong.com`. Certificates are obtained via DNS-01 ACME challenges and
renewed automatically by cert-manager running inside the k3d cluster. No more
keychain-trust steps, no more `-k`/`insecure_skip_verify` flags, browsers and
mobile Safari just work.

## Motivation

The current two-domain setup has real operational pain:

- **Self-signed for `.local`** — every new laptop/phone needs to trust the cert
  manually. iOS in particular requires installing a profile + enabling full
  trust in Settings. Cert rotation resets this ceremony.
- **Plain HTTP (or self-signed) for `*.mclaude.internal`** — mobile Safari and
  modern browsers increasingly gate features (service workers, `crypto.subtle`,
  clipboard) on a valid HTTPS origin. Preview envs on Tailnet hit these gates.
- **Two separate setups** — `.local` for local dev, `.internal` for preview,
  different code paths, different DNS providers, different cert stories. The
  user wants one story.

A domain the user actually owns (`richardmcsong.com`) with a real CA-signed
wildcard cert collapses both problems: the cert is trusted by every device out
of the box, and one subdomain scheme covers both local k3d and CI preview.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Domain | `mclaude.richardmcsong.com` (one zone for mclaude under the user's existing personal domain) | Keeps `richardmcsong.com` root free for other use; `mclaude.*` prefix scopes cleanly |
| Cert type | Wildcard `*.mclaude.richardmcsong.com` | Covers every local and preview subdomain from one cert; avoids per-env issuance |
| CA | Let's Encrypt **production** only (no staging step) | Free, universally trusted, 90-day renewal standard. Staging adds no value once the DO DNS-01 solver is proven — rate limits (50 certs/week/domain) aren't a concern for one wildcard |
| ACME challenge | DNS-01 | Required for wildcard certs. Works even though A records point at Tailscale private IPs |
| Cert issuer runtime | cert-manager in the k3d cluster | Standard K8s pattern; renews automatically; integrates with Traefik via Ingress annotations |
| DNS provider | **DigitalOcean** (zone `richardmcsong.com`) | cert-manager has built-in DigitalOcean DNS-01 solver support; user already has DO credentials/account |
| DNS resolution model | **Pure public A records** on `richardmcsong.com` (hosted at DigitalOcean) | Collapses to one DNS system. Tailnet IP visible in passive DNS is acceptable for a personal setup. No on-host CoreDNS, no Tailscale split DNS |
| DNS record management | **ExternalDNS** controller in-cluster, watching Ingresses | Each Ingress host (`dev.`, `preview-<slug>.`, `dev-nats.`, `nats-preview-<slug>.`) gets its own A record at DO, kept in sync with the cluster's Tailscale IP. Replaces a manual wildcard A record and handles preview create/delete automatically |
| Scope | Both `/deploy-local-preview` and CI preview (`deploy-preview.yml`) | One domain, one cert, one story — matches user's "don't want to manage self-signed anymore" goal |
| Old domains | `mclaude.local` and `mclaude.internal` removed completely in this change | Clean break; includes deleting `charts/coredns-preview/`, openssl step in skill, and all `.local`/`.internal` references |
| cert-manager + issuer packaging | **Ad-hoc from deploy skill / CI workflow** | `helm upgrade --install cert-manager jetstack/cert-manager` with `installCRDs=true --wait`, then `kubectl apply` Secret + ClusterIssuer + Certificate. Avoids Helm's CRD-ordering race. Same pattern for local and CI |
| Preview subdomain shape | **Single-label** `preview-<branch-slug>.mclaude.richardmcsong.com` | Matches today's layout; fully covered by one wildcard; NATS host follows `nats-preview-<slug>.mclaude.richardmcsong.com` |

## User Flow

### Local dev (replaces self-signed `.local`)

1. User runs `/deploy-local-preview`.
2. Skill creates k3d cluster (as today), reads `TS_IP=$(tailscale ip -4)`.
3. Skill pulls the DigitalOcean API token from Bitwarden and applies Secret
   `digitalocean-api-token` into both `cert-manager` and `external-dns`
   namespaces (idempotent).
4. Skill installs cert-manager: `helm upgrade --install cert-manager
   jetstack/cert-manager -n cert-manager --create-namespace
   --set installCRDs=true --wait`.
5. Skill installs ExternalDNS: `helm upgrade --install external-dns
   external-dns/external-dns -n external-dns --create-namespace` with
   `provider=digitalocean`, `domainFilters={mclaude.richardmcsong.com}`,
   `policy=sync`, `txtOwnerId=mclaude-k3d`, `sources={ingress}`, and
   `DO_TOKEN` populated from the Secret.
6. Skill applies `ClusterIssuer mclaude-letsencrypt-prod` pointing at LE prod
   with the DO DNS-01 solver referencing the `digitalocean-api-token` Secret
   in the `cert-manager` namespace.
7. Skill applies `Certificate mclaude-richardmcsong-wildcard` in
   `mclaude-system`, DNS names `*.mclaude.richardmcsong.com` +
   `mclaude.richardmcsong.com`, issuerRef → the ClusterIssuer above, secret
   name `mclaude-richardmcsong-tls`.
8. Skill `kubectl wait --for=condition=Ready
   certificate/mclaude-richardmcsong-wildcard -n mclaude-system --timeout=5m`.
9. Skill runs `helm upgrade --install mclaude charts/mclaude` with
   `ingress.host=dev.mclaude.richardmcsong.com`,
   `ingress.natsHost=dev-nats.mclaude.richardmcsong.com`,
   `ingress.tls[0].secretName=mclaude-richardmcsong-tls`, and
   `ingress.externalDnsTarget=${TS_IP}` (pinning ExternalDNS to emit the
   Tailscale IP as the A-record target regardless of what the Service's
   LoadBalancer reports).
10. ExternalDNS sees both Ingresses, writes A records
    `dev.mclaude.richardmcsong.com` and `dev-nats.mclaude.richardmcsong.com`
    → `${TS_IP}` at DigitalOcean.
11. User opens `https://dev.mclaude.richardmcsong.com` — browser shows a green
    lock immediately, no trust dialog, no keychain shenanigans.

### Preview (replaces plain HTTP `.mclaude.internal`)

CI runs on a self-hosted runner on the same Mac as the k3d cluster (as today),
so CI deploys into the same cluster that `/deploy-local-preview` already
bootstrapped cert-manager + ExternalDNS into.

1. CI runs `.github/workflows/deploy-preview.yml`; `BRANCH_SLUG` computed
   from `GITHUB_REF_NAME`.
2. Workflow resolves `TS_IP=$(tailscale ip -4)` on the runner.
3. Workflow runs `helm upgrade --install mclaude-preview-${BRANCH_SLUG} charts/mclaude`
   (existing release-name convention — do **not** change it, or
   `cleanup-preview.yml` stops finding preview releases to delete) with
   `ingress.host=preview-<slug>.mclaude.richardmcsong.com`,
   `ingress.natsHost=nats-preview-<slug>.mclaude.richardmcsong.com`,
   `ingress.tls[0].secretName=mclaude-richardmcsong-tls`, and
   `ingress.externalDnsTarget=${TS_IP}`.
4. ExternalDNS sees the two new Ingresses, writes their A records to DO.
5. The wildcard cert already issued by `/deploy-local-preview` covers both
   new hostnames — no per-preview cert issuance, no rate-limit pressure.
6. User on Tailnet visits the URL; green lock works the first time.

### Certificate renewal

- cert-manager watches the `Certificate` CR. ~30 days before expiry it triggers
  a DNS-01 challenge, writes a `_acme-challenge.mclaude.richardmcsong.com` TXT
  record via the DNS provider's API, Let's Encrypt validates it, new cert lands
  in the Secret, Traefik hot-reloads.
- If the DNS API token rotates, the user updates the Secret referenced by the
  ClusterIssuer and the next renewal picks it up.

## Component Changes

### `charts/mclaude/` (Helm chart)

- `templates/ingress.yaml`: add a **second** annotations block (after the
  existing `{{- with .Values.ingress.annotations }}` block) that emits the
  ExternalDNS annotations conditionally. Exact pattern:
  ```yaml
  metadata:
    annotations:
      {{- with .Values.ingress.annotations }}
      {{- toYaml . | nindent 4 }}
      {{- end }}
      {{- if .Values.ingress.externalDnsTarget }}
      external-dns.alpha.kubernetes.io/hostname: {{ .Values.ingress.host | quote }}
      external-dns.alpha.kubernetes.io/target: {{ .Values.ingress.externalDnsTarget | quote }}
      {{- end }}
  ```
  Without the `target` annotation, ExternalDNS would use the Service's
  LoadBalancer IP, which in k3d is `127.0.0.1` — wrong. The explicit target
  forces the Tailscale IP. The conditional keeps this template opt-in so
  non-k3d deployments (where ExternalDNS isn't installed) emit no ExternalDNS
  annotations.
- `templates/nats-ws-ingress.yaml`: same pattern, with
  `hostname: {{ .Values.ingress.natsHost | quote }}`.
- `values.yaml`: add one new key `ingress.externalDnsTarget: ""` (empty
  default — skill / CI sets it at install time). **Leave `ingress.tls`
  default as `[]`** so non-k3d environments (`values-aks.yaml`,
  `values-airgap.yaml`) are untouched — they don't reference the
  `mclaude-richardmcsong-tls` Secret, which only exists in the k3d cluster.
  The TLS stanza is set per-environment in `values-k3d-ghcr.yaml` and via
  `--set` flags in `deploy-preview.yml`.

  Note on workflow: the ADR + updated spec files (`spec-helm.md`,
  `spec-tls-certs.md`) are committed in this plan-feature commit and
  describe the post-implementation state. The matching `values.yaml` edit,
  the template edits to `ingress.yaml` / `nats-ws-ingress.yaml`, and the
  `values-k3d-ghcr.yaml` / `deploy-preview.yml` edits land in the
  **subsequent** `/feature-change` → dev-harness commits. Until then,
  `charts/mclaude/values.yaml` still lacks `ingress.externalDnsTarget` and
  still lists the old TLS stanza; this is expected.
- `values-k3d-ghcr.yaml`:
  - `ingress.host` → `dev.mclaude.richardmcsong.com`
  - `ingress.natsHost` → `dev-nats.mclaude.richardmcsong.com`
  - `ingress.tls[0].secretName` → `mclaude-richardmcsong-tls`
  - `ingress.tls[0].hosts` → `["dev.mclaude.richardmcsong.com"]`
  - `controlPlane.externalUrl` → `https://dev.mclaude.richardmcsong.com`
  - `controlPlane.config.natsWsUrl` → `wss://dev-nats.mclaude.richardmcsong.com`
  - Do **not** set `ingress.externalDnsTarget` in the values file — it's
    passed via `--set` at install time because the Tailscale IP is not
    known at file-edit time.
- `values-aks.yaml`, `values-airgap.yaml`, `values-dev.yaml`, `values-e2e.yaml`:
  **unchanged** — none of them reference `mclaude-richardmcsong-tls` or the
  new DNS. The change is scoped to k3d + preview only.

### cert-manager + ExternalDNS bootstrap (ad-hoc)

Both `/deploy-local-preview` and the CI preview workflow install the two
controllers ad-hoc via Helm, then apply the cluster-scoped config via
`kubectl apply`. No chart plumbing; CRD ordering is deterministic because
`--wait` blocks until cert-manager's CRDs are ready.

```bash
# cert-manager
helm repo add jetstack https://charts.jetstack.io
helm upgrade --install cert-manager jetstack/cert-manager \
  -n cert-manager --create-namespace \
  --set installCRDs=true \
  --wait

# ExternalDNS (DigitalOcean provider)
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

Then apply the DO-token Secret (in BOTH `cert-manager` and `external-dns`
namespaces — each controller needs its own copy), the ClusterIssuer, and the
wildcard Certificate CR from the skill / workflow.

Cluster objects:
- `cert-manager` deployment + webhook + cainjector (upstream chart)
- `external-dns` deployment (upstream chart)
- `ClusterIssuer mclaude-letsencrypt-prod` referencing LE prod + DO DNS-01 solver
- `Certificate mclaude-richardmcsong-wildcard` in `mclaude-system`
  (`dnsNames: ["*.mclaude.richardmcsong.com", "mclaude.richardmcsong.com"]`)
- `Secret digitalocean-api-token` in both `cert-manager` and `external-dns` (key: `access-token`)

### `.claude/skills/deploy-local-preview/SKILL.md`

- Step 1 (`k3d cluster create`): **remove** the `--port "53:30053/udp@server:0"`
  flag. That port was only for exposing cluster CoreDNS as a host-side
  NodePort; with public DNS at DigitalOcean, nothing in the cluster serves
  DNS. The `--port "80:80@loadbalancer"` and `--port "443:443@loadbalancer"`
  flags stay unchanged.
- Step 4 (TLS): replace the openssl self-signed block with `helm upgrade
  --install cert-manager` + `helm upgrade --install external-dns` (both with
  `--wait`) + `kubectl apply` of the DigitalOcean API-token Secret (in both
  `cert-manager` and `external-dns` namespaces), the `ClusterIssuer`, and the
  `Certificate` CR. Then `kubectl wait --for=condition=Ready
  certificate/mclaude-richardmcsong-wildcard -n mclaude-system --timeout=5m`.
- Remove the `sudo security add-trusted-cert` instruction — no longer needed.
- Step 5 (CoreDNS custom zone): **deleted entirely**. No custom cluster-internal
  DNS zone; DNS is fully public via DigitalOcean.
- Step 6 (Tailscale split DNS): **deleted entirely**. Same reason.
- Step 8 (Helm install): add `--set "ingress.externalDnsTarget=${TS_IP}"` to
  `HELM_ARGS` (where `TS_IP=$(tailscale ip -4)` is resolved at the top of the
  skill).
- New Prerequisites: `which bw` already exists; add bullet for a Bitwarden
  entry with the **exact name**
  `"DigitalOcean API token — richardmcsong.com zone edit"` holding the DO API
  token (scoped to write on the `richardmcsong.com` zone only). The skill
  reads it via `bw get password "DigitalOcean API token — richardmcsong.com zone edit"`.

### `.github/workflows/deploy-preview.yml`

The existing workflow runs `helm upgrade --install
"mclaude-preview-${BRANCH_SLUG}" ./charts/mclaude` and sets
`ingress.host` + `controlPlane.externalUrl` (with `https://` already). Keep
the release name `mclaude-preview-${BRANCH_SLUG}` unchanged — `cleanup-preview.yml`
uses the same prefix to delete releases on branch removal. Add the following
changes so every preview Ingress is fully addressable with TLS and DNS
automation:

- Compute `PREVIEW_HOST="preview-${BRANCH_SLUG}.mclaude.richardmcsong.com"`
  and `PREVIEW_NATS_HOST="nats-preview-${BRANCH_SLUG}.mclaude.richardmcsong.com"`.
- Resolve `TS_IP=$(tailscale ip -4)` on the self-hosted runner.
- Add these `--set` flags to the existing `helm upgrade --install` command:
  - `--set "ingress.natsHost=${PREVIEW_NATS_HOST}"`
  - `--set "ingress.tls[0].secretName=mclaude-richardmcsong-tls"`
  - `--set "ingress.tls[0].hosts[0]=${PREVIEW_HOST}"`
  - `--set "ingress.tls[0].hosts[1]=${PREVIEW_NATS_HOST}"`
  - `--set "ingress.externalDnsTarget=${TS_IP}"`
  - `--set "controlPlane.config.natsWsUrl=wss://${PREVIEW_NATS_HOST}"`
- Change the final "Preview URL" echo from `http://...` to `https://...`.
- No cert-manager / ExternalDNS install step is needed in this workflow —
  both controllers are already running in the cluster (bootstrapped by
  `/deploy-local-preview`). The workflow only consumes what's there.

### `charts/coredns-preview/` (preview DNS)

**Deleted.** Directory removed entirely: no more on-host CoreDNS container, no
Corefile, no zone file, no `deploy.sh`. DNS is now 100% public via DigitalOcean.

### Bitwarden entries (new)

- Add a **new Login item** named exactly
  `"DigitalOcean API token — richardmcsong.com zone edit"`. The "password"
  field holds the DO API token (scope: write on `richardmcsong.com` zone
  only, read other zones allowed, no account-root scope).
- The deploy skill and CI workflow retrieve the token via:
  ```bash
  DO_TOKEN=$(bw get password "DigitalOcean API token — richardmcsong.com zone edit")
  ```
- The token is then applied as a K8s Secret `digitalocean-api-token`
  (key: `access-token`) into **both** the `cert-manager` and `external-dns`
  namespaces (two identical copies — each controller reads its own
  namespace).

## Data Model

### Kubernetes resources

| Resource | Namespace | Purpose |
|----------|-----------|---------|
| `Deployment cert-manager`, `cert-manager-webhook`, `cert-manager-cainjector` | `cert-manager` | Controller + webhook (installed by upstream chart) |
| `Deployment external-dns` | `external-dns` | Watches Ingresses cluster-wide, upserts A/TXT records at DigitalOcean |
| `ClusterIssuer mclaude-letsencrypt-prod` | cluster-scoped | ACME account + DO DNS-01 solver config referencing `digitalocean-api-token` Secret |
| `Secret digitalocean-api-token` | `cert-manager` and `external-dns` (two copies) | Holds the DigitalOcean API token (key `access-token`) |
| `Certificate mclaude-richardmcsong-wildcard` | `mclaude-system` | Requests `*.mclaude.richardmcsong.com` + apex; issues into Secret `mclaude-richardmcsong-tls` |
| `Secret mclaude-richardmcsong-tls` | `mclaude-system` | `tls.crt` / `tls.key` populated by cert-manager; referenced by both Ingresses |

### DNS records

Under the `richardmcsong.com` zone at the chosen DNS provider:

| Record | Type | Target | Purpose |
|--------|------|--------|---------|
| `_acme-challenge.mclaude.richardmcsong.com` | TXT | (written/deleted by cert-manager during each ACME DNS-01 challenge) | ACME validation; TTL 60s |
| `dev.mclaude.richardmcsong.com` | A | k3d host's Tailscale IP | Written by ExternalDNS watching the main Ingress; TTL follows ExternalDNS default (300s) |
| `dev-nats.mclaude.richardmcsong.com` | A | k3d host's Tailscale IP | Written by ExternalDNS watching the nats-ws Ingress |
| `preview-<slug>.mclaude.richardmcsong.com` | A | k3d host's Tailscale IP (CI preview deploys into the same k3d) | Written by the same in-cluster ExternalDNS watching the preview Ingress |
| `nats-preview-<slug>.mclaude.richardmcsong.com` | A | k3d host's Tailscale IP | Written by the same in-cluster ExternalDNS watching the preview nats-ws Ingress |
| `mclaude.richardmcsong.com` (apex) | A | unused — reserved for now | Included in cert SAN so the apex also works if needed later; no Ingress binds to it |

## Error Handling

| Failure | Detection | Recovery |
|---------|-----------|----------|
| DO API token invalid / revoked | `Certificate` CR stuck in `Issuing`; `kubectl describe` shows solver error. ExternalDNS logs show 401 | User rotates token in Bitwarden, re-runs skill (applies new Secret); cert-manager and ExternalDNS retry |
| Let's Encrypt rate limit (50 certs/week/domain) | cert-manager logs show 429; Certificate stuck in `Issuing` | Point `ClusterIssuer` at LE staging temporarily while debugging; switch back once fixed |
| DO DNS propagation slow | `_acme-challenge` TXT not visible to LE validator within timeout | cert-manager retries with exponential backoff; if persistent, raise `solver.dns01.digitalocean.propagationTimeout` |
| Cert renewal fails and existing cert expires | Browser shows expired cert warning | `kubectl delete certificate mclaude-richardmcsong-wildcard && kubectl apply` the Certificate CR → immediate reissue |
| k3d cluster deleted and recreated | Certificate Secret gone | Skill re-applies `Certificate` CR; cert-manager re-issues on first apply (no ceremony — but counts against the 50/week LE limit, so back-to-back cluster-destroys in the same week may hit the rate limit) |
| Tailscale IP changes | All A records point at old IP; hostnames unreachable | Skill picks up new IP on next run (`tailscale ip -4`) and sets `ingress.externalDnsTarget=${NEW_IP}`; ExternalDNS updates records. Until then, user re-runs `/deploy-local-preview` |
| ExternalDNS `sync` policy deletes records unexpectedly | A records vanish after a `helm uninstall` or a bad manifest; `dig` returns NXDOMAIN | Re-run `/deploy-local-preview` (or `helm install` in CI); ExternalDNS re-creates records within its reconcile interval (~1 min). Records are recreatable — not a data-loss event |
| Corporate HTTPS-intercepting proxy MITMs `*.mclaude.richardmcsong.com` | Browser sees the corporate MITM cert, not LE | Out of scope for this ADR — user's Mac should bypass the proxy for this domain via PAC, or the proxy admin should allowlist the host. If DPI is unavoidable, the browser's TLS trust will show the corp CA cert instead of LE, which is cosmetically worse than today but no less secure |

## Security

- **DNS provider API token** — stored in a K8s Secret in the `cert-manager`
  namespace, readable only by cert-manager's ServiceAccount. Scope the token to
  the `richardmcsong.com` zone only (DNS edit) — not to the account root.
- **Token in Bitwarden** — source of truth; deploy script pulls it at install
  time rather than committing it.
- **Private key** — generated by cert-manager inside the cluster; never leaves
  the cluster; rotated automatically on renewal.
- **Wildcard exposure** — a wildcard cert means a compromise of the private key
  gives an attacker any `*.mclaude.richardmcsong.com` identity. Mitigated by
  90-day renewal (old key rotates out quickly) and scoped namespace RBAC.

## Impact

Specs updated in this commit:
- `docs/spec-tailscale-dns.md` — **deleted** (CoreDNS + Tailscale split DNS
  subsystem removed entirely; public DNS takes over).
- `docs/feature-list.md` § Infrastructure — add "Let's Encrypt wildcard TLS
  on `*.mclaude.richardmcsong.com`".
- New `docs/spec-tls-certs.md` (cross-cutting) — the **canonical contract**
  for this subsystem. Covers:
  - Domain + zone (`richardmcsong.com` at DigitalOcean), hostname table.
  - cert-manager chart (`jetstack/cert-manager`), namespace (`cert-manager`),
    exact install command.
  - `ClusterIssuer mclaude-letsencrypt-prod` — full YAML body with LE prod
    URL, the DO DNS-01 solver, and the token secret reference.
  - `Certificate mclaude-richardmcsong-wildcard` — full YAML body in
    `mclaude-system`, `commonName`, `dnsNames`, `duration`, `renewBefore`,
    `secretName: mclaude-richardmcsong-tls`.
  - ExternalDNS chart (`external-dns/external-dns`), namespace
    (`external-dns`), exact install command with `provider=digitalocean`,
    `domainFilters`, `policy=sync`, `txtOwnerId=mclaude-k3d`, `sources=[ingress]`.
  - Ingress annotations emitted by the Helm chart
    (`external-dns.alpha.kubernetes.io/hostname` +
    `external-dns.alpha.kubernetes.io/target`).
  - DigitalOcean API token: Bitwarden item name, required scope, how it's
    loaded into both namespaces.
  - Renewal timing, first-issuance expected duration (`kubectl wait` budget).
  - Component responsibilities table.
  - Failure modes table.

  Resource **names** listed in that spec are normative — components must use
  exactly these identifiers so Ingresses and the Certificate Secret line up:
  `cert-manager`, `external-dns`, `mclaude-letsencrypt-prod`,
  `mclaude-richardmcsong-wildcard`, `mclaude-richardmcsong-tls`,
  `digitalocean-api-token`.
- `docs/charts-mclaude/spec-helm.md` — Dependencies section lists cert-manager
  and ExternalDNS as required in-cluster controllers for HTTPS in k3d/CI
  preview (forward-reference to `docs/spec-tls-certs.md`). The `values-k3d-ghcr.yaml`
  row description updated. `controlPlane.externalUrl` example updated.
  `ingress.tls` default documented as `[]` with the per-env override pattern.
  New `ingress.externalDnsTarget` key documented.
- `docs/mclaude-control-plane/spec-control-plane.md` — `EXTERNAL_URL`
  example updated from `https://mclaude.internal` to
  `https://dev.mclaude.richardmcsong.com`.
- `docs/_sidebar.md` — replace `[Tailscale DNS](spec-tailscale-dns.md)` entry
  with `[TLS Certificates](spec-tls-certs.md)`.
- `docs/spec-doc-layout.md` — replace `spec-tailscale-dns.md` with
  `spec-tls-certs.md` in the Cross-cutting spec examples row.

Components touched:
- `charts/mclaude/` (values + two Ingress templates get ExternalDNS annotations)
- `charts/coredns-preview/` — **deleted entirely**
- `.claude/skills/deploy-local-preview/` (rewritten TLS step + new
  cert-manager/ExternalDNS install steps; CoreDNS + Tailscale split DNS steps
  removed)
- `.github/workflows/deploy-preview.yml` (hostname + ExternalDNS target)

## Scope

**In v1:**
- Wildcard Let's Encrypt cert for `*.mclaude.richardmcsong.com`
- cert-manager in k3d with DNS-01 solver
- `/deploy-local-preview` emits `https://dev.mclaude.richardmcsong.com`
- Preview deploys emit `https://preview-<slug>.mclaude.richardmcsong.com`
- Bitwarden entry for DNS API token

**Deferred:**
- Per-user subdomains (`<user>.mclaude.richardmcsong.com`)
- Non-wildcard per-env certs (only if rate limits become a problem)
- Extending the same cert to `richardmsong.com`-owned GitHub Pages, etc.
- Cert pinning / HSTS preloading

## Open questions

(All resolved — see Decisions table above.)

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| `charts/mclaude` values + two Ingress templates | ~60 | ~40k | Add ExternalDNS annotations (both Ingresses); swap secret name + default tls stanza; add `ingress.externalDnsTarget` |
| `/deploy-local-preview` skill rewrite | ~150 | ~70k | Remove Step 4 openssl block + Steps 5-6 (CoreDNS/split-DNS); insert cert-manager install + ExternalDNS install + DO Secret + ClusterIssuer + Certificate apply + wait |
| `.github/workflows/deploy-preview.yml` | ~20 | ~15k | Host/natsHost template + `ingress.externalDnsTarget=${TS_IP}` flag |
| `charts/coredns-preview/` removal | ~0 | ~5k | Delete directory |
| New `docs/spec-tls-certs.md` | ~100 | ~15k | Describe cert-manager + ExternalDNS + DO solver runtime, cert lifecycle, token rotation |
| Edit `docs/charts-mclaude/spec-helm.md`, `docs/feature-list.md`; delete `docs/spec-tailscale-dns.md` | ~40 | ~10k | Spec backfill for the new dependencies + feature, drop old DNS spec |

**Total estimated tokens:** ~155k
**Estimated wall-clock:** ~0.7h of 5h budget (14%)
