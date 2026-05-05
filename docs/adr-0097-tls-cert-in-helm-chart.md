# ADR: TLS Certificate CR in mclaude-cp Helm chart with helm.sh/resource-policy: keep

**Status**: implemented
**Status history**:
- 2026-05-05: draft
- 2026-05-05: accepted — paired with docs/spec-tls-certs.md, docs/charts-mclaude/spec-helm.md, docs/adr-0033-letsencrypt-tls.md (supersede-in-part note)
- 2026-05-05: implemented — all scope CLEAN (charts/mclaude-cp/templates/certificate.yaml + values + deploy-local-preview skill)

## Overview

Move the cert-manager `Certificate` CR (`mclaude-richardmcsong-wildcard`) from manual bootstrap (currently owned by the `/deploy-local-preview` skill, per ADR-0033) into the `mclaude-cp` Helm chart as a templated resource, gated by `tls.certManager.enabled` (default `false` in `values.yaml`; per-environment overlays like `values-k3d-ghcr.yaml` flip it to `true`). The template carries `helm.sh/resource-policy: keep` so a `helm uninstall` does **not** delete the Certificate or its derived TLS Secret — preserving the cert across wipes and not burning the Let's Encrypt rate limit. Environments that bring their own TLS (AKS Application Gateway, air-gapped corporate CA) leave `tls.certManager.enabled` at its default `false` and reference an externally-managed Secret via the existing `ingress.tls[0].secretName` knob.

## Motivation

Today's behavior, surfaced when the `/deploy-local-preview` skill's bootstrap step is forgotten or its output is wiped:

1. `mclaude-cp` Ingress declares `tls.secretName: mclaude-richardmcsong-tls`.
2. That Secret is populated by cert-manager from a `Certificate mclaude-richardmcsong-wildcard` CR.
3. The CR is **not** in any Helm chart — `/deploy-local-preview` applies it via `kubectl apply`.
4. If anything wipes the CR (namespace teardown, missed bootstrap step, drift), Traefik falls back to its self-signed default cert (`CN=TRAEFIK DEFAULT CERT`) — TLS still terminates, but browsers reject it.

The original separation argument (ADR-0033) was Let's Encrypt rate-limit risk: helm-owned Certificate + `helm uninstall` cycles → repeated cert reissues → 50/week limit hit. But that hasn't held up in practice — the Certificate goes missing through other paths anyway, and the LE rate-limit risk dissolves entirely once the Certificate carries `helm.sh/resource-policy: keep` (helm doesn't delete it on uninstall, so the Secret persists, so reinstall reuses the existing cert without a fresh issuance).

Putting the Certificate in the chart with the keep annotation gives:
- **Fresh install = working TLS.** No manual bootstrap step.
- **Wipe-resistant.** `helm uninstall mclaude-cp` leaves the Certificate (and its Secret) in place.
- **Opt-out path.** Environments with externally-managed TLS set `tls.certManager.enabled=false` and the chart skips the Certificate template entirely; the Ingress references whatever Secret name the operator supplies.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Chart that owns the Certificate | `mclaude-cp` | The Certificate's Secret lives in `mclaude-system` and is consumed by every Ingress in that namespace (main + NATS WS + MinIO). `mclaude-cp` is the only chart guaranteed to be installed there. `mclaude-postgres` is optional (managed-DB skips it); `mclaude-worker` lives in worker clusters. |
| Helm annotation | `helm.sh/resource-policy: keep` on the Certificate template | Tells Helm not to delete the Certificate on `helm uninstall`. Helm still **creates** it on `helm install` if absent, and **adopts** it on subsequent installs (no-op). The derived Secret (cert-manager-managed) persists naturally because nothing references it for deletion. |
| Opt-out gate | `tls.certManager.enabled` boolean values flag, default `false` in `values.yaml`; `values-k3d-ghcr.yaml` opts in with `true` | Default-false makes the opt-in explicit per environment — every overlay declares its own intent (so AKS/airgap don't silently inherit cert-manager when the operator forgot to override). The dev overlay flips it on with the cluster's wildcard config. |
| Source of `secretName` | Reused from `ingress.tls[0].secretName` (already required) | Single source of truth for the Secret name. The Certificate template references it via `{{ (index .Values.ingress.tls 0).secretName }}` so cert-manager populates exactly the Secret the Ingress expects. |
| Source of cert details | New `tls.certManager.{issuerRef, commonName, dnsNames, duration, renewBefore}` values, set per-environment in `values-k3d-ghcr.yaml` and `values-aks.yaml` | Hostnames are environment-specific; can't be hardcoded in `values.yaml`. Default `values.yaml` sets `enabled: false` to make the opt-in explicit (you must declare your environment's domain in your overlay). |
| Default in `values.yaml` | `tls.certManager.enabled: false` | Forces every environment overlay to make a deliberate choice. `values-k3d-ghcr.yaml` flips it to `true` with the dev hostnames; `values-aks.yaml` keeps it `false` (Azure-managed certs); air-gapped values keep it `false` (corporate CA Secret pre-created). Default-false in values.yaml + default-true in dev overlay = matches the user-spec "default true for dev" while staying explicit per-env. |
| `/deploy-local-preview` skill | Stop applying the Certificate CR. Skill keeps managing cert-manager + ClusterIssuer + DO token + ExternalDNS (cluster bootstrap), but the Certificate is now helm's responsibility. | Skill becomes simpler. Cluster bootstrap = "infrastructure" (one-time per cluster); Certificate = "application TLS" (every helm install). Cleaner mental model. |
| Pre-existing wildcard cert | New install: chart creates it. Existing installs (none currently — dev cluster's Certificate is missing): chart will create on next install since the resource is absent. No PVC-style migration. | The Certificate either exists (chart adopts via the annotation, no-op) or doesn't (chart creates it). Either path lands a working state. |

## User Flow

### Local dev (k3d) — fresh install

```bash
# Cluster bootstrap (one-time per cluster, via /deploy-local-preview skill):
#   - cert-manager helm chart
#   - DigitalOcean DNS-01 token Secret
#   - mclaude-letsencrypt-prod ClusterIssuer
#   - ExternalDNS
# (No more manual `kubectl apply` of the Certificate CR.)

helm install mclaude-postgres charts/mclaude-postgres -n mclaude-system [...]
helm install mclaude-cp        charts/mclaude-cp        -n mclaude-system \
  -f charts/mclaude-cp/values-k3d-ghcr.yaml \
  --set [...]
# values-k3d-ghcr.yaml has tls.certManager.enabled=true and the cluster's
# wildcard hostnames. The chart renders the Certificate; cert-manager picks
# it up; DNS-01 challenge runs; Secret is populated within ~2 min.

# To wipe and reinstall:
helm uninstall mclaude-cp -n mclaude-system
# → Certificate CR + tls Secret persist (helm.sh/resource-policy: keep)

helm install mclaude-cp [...]
# → chart adopts existing Certificate (no-op, no LE re-issuance, no rate-limit cost)
```

### AKS production — externally-managed TLS

```bash
# Operator pre-creates the TLS Secret from Azure-managed cert material:
kubectl create secret tls mclaude-aks-tls --cert=tls.crt --key=tls.key -n mclaude-system

helm install mclaude-cp charts/mclaude-cp -n mclaude-system \
  -f charts/mclaude-cp/values-aks.yaml \
  --set ingress.tls[0].secretName=mclaude-aks-tls \
  [...]
# values-aks.yaml has tls.certManager.enabled=false → chart skips Certificate
# template entirely. Ingress references mclaude-aks-tls directly.
```

## Component Changes

### `charts/mclaude-cp/templates/certificate.yaml` (NEW)

Templated `Certificate` CR rendered only when `tls.certManager.enabled` is `true`:

```yaml
{{- if .Values.tls.certManager.enabled -}}
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: {{ include "mclaude-cp.fullname" . }}-wildcard
  namespace: {{ .Release.Namespace }}
  annotations:
    helm.sh/resource-policy: keep
  labels:
    {{- include "mclaude-cp.labels" . | nindent 4 }}
spec:
  secretName: {{ (index .Values.ingress.tls 0).secretName }}
  issuerRef:
    kind: {{ .Values.tls.certManager.issuerRef.kind | default "ClusterIssuer" }}
    name: {{ required "tls.certManager.issuerRef.name is required when tls.certManager.enabled" .Values.tls.certManager.issuerRef.name }}
  commonName: {{ required "tls.certManager.commonName is required when tls.certManager.enabled" .Values.tls.certManager.commonName | quote }}
  dnsNames:
    {{- range (required "tls.certManager.dnsNames is required when tls.certManager.enabled" .Values.tls.certManager.dnsNames) }}
    - {{ . | quote }}
    {{- end }}
  duration: {{ .Values.tls.certManager.duration | default "2160h" }}
  renewBefore: {{ .Values.tls.certManager.renewBefore | default "720h" }}
{{- end }}
```

`required` calls fail helm template/install fast if `enabled: true` but configuration is incomplete.

### `charts/mclaude-cp/values.yaml`

Add the new `tls.certManager.*` block, default disabled:

```yaml
tls:
  certManager:
    enabled: false
    issuerRef:
      kind: ClusterIssuer
      name: ""           # required when enabled
    commonName: ""       # required when enabled
    dnsNames: []         # required when enabled
    duration: 2160h      # 90 days (LE default)
    renewBefore: 720h    # 30 days before expiry
```

### `charts/mclaude-cp/values-k3d-ghcr.yaml`

Add a new top-level `tls:` key (the file currently has no such key). Existing keys (`ingress`, `controlPlane`, `nats`, etc.) are left in place; only the new `tls:` block is added:

```yaml
# (existing keys above, untouched)

tls:
  certManager:
    enabled: true
    issuerRef:
      kind: ClusterIssuer
      name: mclaude-letsencrypt-prod
    commonName: "*.mclaude.richardmcsong.com"
    dnsNames:
      - "*.mclaude.richardmcsong.com"
      - "mclaude.richardmcsong.com"

# ingress.tls[0].secretName already exists in this file as
# `mclaude-richardmcsong-tls` and is the source of truth for both the
# Ingress and the new Certificate template — do NOT duplicate it.
```

### `charts/mclaude-cp/values-aks.yaml` (and `values-airgap.yaml`)

No new content needed — `tls.certManager.enabled` defaults to `false` from `values.yaml`. The operator pre-creates the TLS Secret out-of-band and references it via `ingress.tls[0].secretName`.

### `/deploy-local-preview` skill

Remove the `kubectl apply -f -` step that creates the Certificate CR (helm renders it now). **Keep** the wait step but move it after `helm install mclaude-cp` and update the resource name:

```bash
# Old (removed): kubectl apply Certificate; kubectl wait --for=condition=Ready certificate/mclaude-richardmcsong-wildcard ...
# New:
helm install mclaude-postgres charts/mclaude-postgres -n mclaude-system [...]
helm install mclaude-cp        charts/mclaude-cp        -n mclaude-system [...]
kubectl wait --for=condition=Ready certificate/mclaude-cp-wildcard \
  -n mclaude-system --timeout=5m
```

`helm install --wait` only observes Pod/Service readiness; it does NOT wait for cert-manager to fulfill a Certificate (that's a separate controller's reconcile loop). The explicit `kubectl wait` on the Certificate fills the gap — it blocks until cert-manager has run the DNS-01 challenge and populated the TLS Secret. Without this step the skill could return while Ingress is still serving Traefik's self-signed default. The wait is a no-op on subsequent installs (Certificate already Ready, kept across uninstalls per the `helm.sh/resource-policy: keep` annotation).

The skill keeps:
- Installing cert-manager (helm chart in `cert-manager` namespace)
- Creating the `digitalocean-api-token` Secret in both `cert-manager` and `mclaude-system`
- Creating the `mclaude-letsencrypt-prod` ClusterIssuer
- Installing ExternalDNS

These remain genuine cluster-bootstrap concerns (one-time per cluster, not per helm install). The Certificate template is now helm's responsibility; the wait stays in the skill because helm `--wait` semantics don't cover cert-manager.

## Data Model

No tables, KV entries, or NATS subjects change. K8s resources change:

| Resource | Before | After |
|----------|--------|-------|
| `Certificate mclaude-richardmcsong-wildcard` | Manually `kubectl apply`'d by `/deploy-local-preview` skill (when remembered) | Templated by `mclaude-cp` chart when `tls.certManager.enabled=true`. Carries `helm.sh/resource-policy: keep`. |
| Secret `mclaude-richardmcsong-tls` | Created by cert-manager from the Certificate. Not helm-managed. | Same — created by cert-manager. Not helm-managed. Persists across helm uninstalls naturally (no helm release tracks it). |
| Certificate name | `mclaude-richardmcsong-wildcard` (hardcoded) | `{{ include "mclaude-cp.fullname" . }}-wildcard` → `mclaude-cp-wildcard` for the default release name. Drifts from current cluster state on first install of the new chart, which adopts the new name. |

**Migration runbook** (only relevant for clusters that already have the legacy `mclaude-richardmcsong-wildcard` Certificate — the current dev cluster has neither, so no migration is needed today):

cert-manager refuses to manage a Secret already owned by another Certificate (the `cert-manager.io/certificate-name` annotation is exclusive). The legacy and new Certificates therefore CANNOT coexist with the same `secretName: mclaude-richardmcsong-tls`. Required order:

```bash
# 1. Delete the legacy Certificate FIRST (releases its claim on the Secret).
kubectl delete certificate mclaude-richardmcsong-wildcard -n mclaude-system
# The Secret persists; nothing references it for deletion.

# 2. helm install/upgrade mclaude-cp with tls.certManager.enabled=true.
#    Chart renders `mclaude-cp-wildcard`. cert-manager picks it up, claims
#    the existing (un-owned) Secret, and reissues only if the spec differs
#    materially from what's already in the Secret.
helm upgrade --install mclaude-cp [...]
```

A brief TLS gap (~seconds) is possible between step 1 and the new Certificate becoming `Ready: True`. Step 1 does not delete the Secret, so Traefik continues serving the existing cert until cert-manager updates it.

## Error Handling

- **`tls.certManager.enabled=true` with missing required fields**: `helm install` / `helm template` fails fast on the `required` template directive. Operator gets a clear error pointing at the missing values key.
- **Certificate stuck in `Issuing` state**: cert-manager surfaces detailed status on the Certificate object (DNS-01 challenge log, Order, Challenge resources). Operator runs `kubectl describe certificate mclaude-cp-wildcard -n mclaude-system` to debug. Same diagnostics as today.
- **DNS-01 fails (DO token expired, hostname not in cluster zone)**: same as today — cert-manager retries with backoff. The Ingress falls back to Traefik's self-signed cert until the Secret materializes.
- **`helm uninstall mclaude-cp` while Certificate is in use**: `helm.sh/resource-policy: keep` instructs helm to skip the Certificate. It survives. The derived Secret (not helm-tracked) survives. Next `helm install mclaude-cp` adopts the existing Certificate as a no-op.
- **`helm upgrade` with Certificate config changes** (e.g., adding a new dnsName): helm tries to update the kept resource; cert-manager re-issues if the spec changed. LE re-issue burns a rate-limit slot — the keep annotation only prevents delete, not update, so this is the same risk as a non-helm-managed Certificate update would be.

## Security

- LE wildcard cert covers `*.mclaude.richardmcsong.com` and `mclaude.richardmcsong.com`. Same scope as today.
- DNS-01 challenge keys remain in the `digitalocean-api-token` Secret (cert-manager namespace + cp namespace). Not affected by this ADR.
- TLS Secret (`mclaude-richardmcsong-tls`) lives in `mclaude-system`. Read by Traefik IngressClass via the Ingress `tls` block. Not affected by this ADR.
- Externally-managed TLS path (`enabled: false`): chart never creates a Certificate, never touches the TLS Secret. Operator owns the cert lifecycle.

## Impact

- `docs/spec-tls-certs.md`: rewrite the "Certificate CR" + "Bootstrap" sections — Certificate moves from skill bootstrap into the chart; describe the `helm.sh/resource-policy: keep` semantics; describe the `tls.certManager.enabled` opt-in/opt-out.
- `docs/charts-mclaude/spec-helm.md`: add the new Certificate template to the `mclaude-cp` chart resource table; document the `tls.certManager.*` knobs; note the `helm.sh/resource-policy: keep` annotation.
- ADR-0033 (`docs/adr-0033-letsencrypt-tls.md`): add a one-line `> Superseded in part by adr-0097-tls-cert-in-helm-chart.md` note near the section describing Certificate ownership. ADR-0033's overall TLS architecture (cert-manager + DNS-01 + ExternalDNS) stays in force; only the Certificate-CR-ownership decision is superseded.
- `.agent/skills/deploy-local-preview/SKILL.md`: remove the `kubectl apply Certificate` step. (Code change owned by dev-harness, not this ADR's spec edits.)

## Scope

**In v1:**
- New `templates/certificate.yaml` in `charts/mclaude-cp/`, gated by `tls.certManager.enabled`, with `helm.sh/resource-policy: keep`.
- New `tls.certManager.*` values keys in `values.yaml` (defaults), `values-k3d-ghcr.yaml` (enabled, dev hostnames).
- `/deploy-local-preview` skill loses its Certificate-apply step.
- spec-tls-certs.md, spec-helm.md, ADR-0033 supersede note.

**Deferred:**
- Multi-issuer support (e.g., LE staging + LE prod side-by-side).
- Rotation of the Certificate's `dnsNames` without LE re-issuance (cert-manager handles this naturally; no chart-level work needed unless we hit a real case).
- `cert-manager.io/cluster-issuer` annotation on the Ingress (cert-manager's "ingress-shim" auto-Certificate path) as an alternative implementation. Considered and rejected: explicit Certificate template is more readable, version-controlled, and supports the `keep` annotation directly.

## Open questions

_(none — all resolved. See Decisions table.)_

## Integration Test Cases

| Test case | What it verifies | Setup/teardown | Components exercised |
|-----------|------------------|----------------|----------------------|
| Fresh install issues a Certificate | `helm install mclaude-cp` with `tls.certManager.enabled=true` → Certificate CR appears, cert-manager issues, Secret materializes within the chart's wait window, `helm install --wait` succeeds | Wipe `mclaude-system` Certificate + Secret; install fresh | mclaude-cp chart, cert-manager, ExternalDNS, ClusterIssuer, DO API |
| `helm uninstall` preserves Certificate | After fresh install above, `helm uninstall mclaude-cp` then `kubectl get certificate -n mclaude-system` → still present, still `Ready: True` | Run uninstall after Certificate is Ready | mclaude-cp chart |
| Re-install adopts existing Certificate | After uninstall, `helm install mclaude-cp` again — cert-manager logs no new issuance, Secret unchanged, Certificate unchanged. No LE rate-limit hit. | Run install on top of preserved Certificate | mclaude-cp chart, cert-manager |
| Disabled mode skips Certificate | `helm install mclaude-cp --set tls.certManager.enabled=false` with operator-pre-created TLS Secret → no Certificate CR rendered, Ingress uses operator's Secret, TLS works | Pre-create a self-signed Secret as a stand-in | mclaude-cp chart |
| Required fields missing | `helm install mclaude-cp --set tls.certManager.enabled=true` with `tls.certManager.commonName=""` → install fails with template error citing the missing field | No setup | mclaude-cp chart (template-time only) |
| Cluster cert verification | After fresh install + Certificate Ready, `openssl s_client -connect dev.mclaude.richardmcsong.com:443` returns a valid LE-issued cert (issuer `R3` or `E1`, CN `*.mclaude.richardmcsong.com`) | Live cluster with public DNS | mclaude-cp ingress, cert-manager Secret, Traefik |

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| `charts/mclaude-cp/templates/certificate.yaml` (new) | ~25 lines | 25k | One template, gated on a values flag, with `required` directives |
| `charts/mclaude-cp/values.yaml` + `values-k3d-ghcr.yaml` | ~20 lines | 15k | Add `tls.certManager.*` defaults; flip `enabled: true` in dev overlay with hostnames |
| `.agent/skills/deploy-local-preview/SKILL.md` | ~-15 lines | 10k | Remove the Certificate-apply step; keep cert-manager + ClusterIssuer + DO token + ExternalDNS bootstrap |
| Spec edits | ~50 lines | 15k | spec-tls-certs.md rewrites, spec-helm.md additions, ADR-0033 supersede note |

**Total estimated tokens:** 65k
**Estimated wall-clock:** 1–1.5h
