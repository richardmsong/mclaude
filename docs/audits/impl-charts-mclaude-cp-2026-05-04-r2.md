## Run: 2026-05-04T00:00:00Z (Round 2)

Component: charts-mclaude-cp + .agent/skills/deploy-local-preview/SKILL.md
Specs: docs/charts-mclaude/spec-helm.md, docs/spec-tls-certs.md, docs/adr-0097-tls-cert-in-helm-chart.md
Round 1 gaps: all three fixed (commits 276ff48, 9e14c1c)

---

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0097:85-112 | `templates/certificate.yaml` rendered only when `tls.certManager.enabled` is `true`; carries `helm.sh/resource-policy: keep`; name `{release}-wildcard`; `secretName` from `ingress.tls[0].secretName`; `required` directives for issuerRef.name, commonName, dnsNames; duration/renewBefore with defaults | charts/mclaude-cp/templates/certificate.yaml:1-23 | IMPLEMENTED | — | Template matches ADR verbatim; all required calls present; keep annotation present; gating condition present |
| adr-0097:117-131 | `values.yaml` `tls.certManager` block: `enabled: false`, `issuerRef.kind: ClusterIssuer`, `issuerRef.name: ""`, `commonName: ""`, `dnsNames: []`, `duration: 2160h`, `renewBefore: 720h` | charts/mclaude-cp/values.yaml:251-260 | IMPLEMENTED | — | All seven keys present with exact defaults |
| adr-0097:133-154 | `values-k3d-ghcr.yaml` adds `tls.certManager.enabled: true`, `issuerRef.kind: ClusterIssuer`, `issuerRef.name: mclaude-letsencrypt-prod`, `commonName: "*.mclaude.richardmcsong.com"`, `dnsNames: ["*.mclaude.richardmcsong.com","mclaude.richardmcsong.com"]`; does NOT duplicate `secretName` | charts/mclaude-cp/values-k3d-ghcr.yaml:63-73 | IMPLEMENTED | — | All five cert sub-keys present; comment at line 60-62 explicitly notes no secretName duplication |
| adr-0097:156-159 | `values-aks.yaml` and `values-airgap.yaml` need no new content — `tls.certManager.enabled` defaults to `false` | charts/mclaude-cp/values-aks.yaml (no tls: key); charts/mclaude-cp/values-airgap.yaml (no tls: key) | IMPLEMENTED | — | Both files have no tls: section, correctly inheriting enabled:false from values.yaml |
| adr-0097:162-168 | `/deploy-local-preview` skill: remove `kubectl apply` Certificate CR step; keep `kubectl wait` after `helm install mclaude-cp`; wait resource is `certificate/mclaude-cp-wildcard`; skill keeps cert-manager + ClusterIssuer + DO token + ExternalDNS | SKILL.md:186 (ADR-0097 note removing Certificate kubectl apply); SKILL.md:334-336 (`kubectl wait certificate/mclaude-cp-wildcard`); SKILL.md:157-207 (cert-manager + ClusterIssuer kept); SKILL.md:169-182 (ExternalDNS kept) | IMPLEMENTED | — | No Certificate kubectl apply anywhere in skill; wait uses correct resource name; cluster bootstrap concerns all retained |
| spec-helm.md:51-55 | TLS Certificate table: `Certificate (cert-manager.io/v1)` named `{release}-wildcard`; rendered when `tls.certManager.enabled=true`; carries `helm.sh/resource-policy: keep`; derived Secret persists naturally; external path skips template | charts/mclaude-cp/templates/certificate.yaml:1-23; values.yaml:251-260 | IMPLEMENTED | — | Table entry in spec matches code |
| spec-helm.md:129 | `tls.certManager.enabled` default `false`; values-k3d-ghcr.yaml flips to `true`; AKS/airgap leave it `false` | values.yaml:253; values-k3d-ghcr.yaml:65; values-aks.yaml (no override); values-airgap.yaml (no override) | IMPLEMENTED | — | |
| spec-helm.md:130-135 | Six sub-keys: `issuerRef.kind`, `issuerRef.name`, `commonName`, `dnsNames`, `duration`, `renewBefore` with defaults | values.yaml:254-260 | IMPLEMENTED | — | |
| spec-tls-certs.md:84-106 | Certificate CR block: gate on `tls.certManager.enabled`; name `{release}-wildcard`; keep annotation; secretName from `ingress.tls[0].secretName`; issuerRef.kind default ClusterIssuer; required directives for name/commonName/dnsNames; duration/renewBefore defaults | charts/mclaude-cp/templates/certificate.yaml:1-23 | IMPLEMENTED | — | Template in spec and code are identical |
| spec-tls-certs.md:110 | Resource name `{release}-wildcard` (mclaude-cp-wildcard for default release); legacy name `mclaude-richardmcsong-wildcard` no longer in use | SKILL.md:335 (`certificate/mclaude-cp-wildcard`); troubleshooting table SKILL.md:377 (`mclaude-cp-wildcard`) | IMPLEMENTED | — | |
| spec-tls-certs.md:114 | Adoption semantics: helm checks if `mclaude-cp-wildcard` exists; if yes, adopts as no-op; if no, creates and cert-manager issues fresh | charts/mclaude-cp/templates/certificate.yaml:1 (`{{- if .Values.tls.certManager.enabled -}}`); helm.sh/resource-policy: keep at line 8 | IMPLEMENTED | — | keep annotation is the mechanism that enables adoption semantics |
| spec-tls-certs.md:116 | Externally-managed TLS path: `enabled=false` → chart skips Certificate entirely | charts/mclaude-cp/templates/certificate.yaml:1 (if gate) and :23 (end) | IMPLEMENTED | — | |
| spec-tls-certs.md:188-193 | `helm install --wait` does NOT wait for cert-manager; explicit `kubectl wait --for=condition=Ready certificate/mclaude-cp-wildcard -n mclaude-system --timeout=5m` required; placed after `helm install mclaude-cp` | SKILL.md:331-336 | IMPLEMENTED | — | |
| spec-tls-certs.md:199 | Component Responsibilities: `/deploy-local-preview` bootstraps cert-manager, ExternalDNS, DO-token Secret, ClusterIssuer; Certificate CR owned by `mclaude-cp` chart via keep template | SKILL.md:157-207 (cert-manager + ClusterIssuer + DO token + ExternalDNS); SKILL.md:186 (Certificate removed from skill); values-k3d-ghcr.yaml:63-73 (Certificate now in chart) | IMPLEMENTED | — | |
| spec-tls-certs.md:208 | Failure mode: `Certificate stuck in Issuing` (any cause) — `kubectl describe certificate mclaude-cp-wildcard -n mclaude-system`; walk Order/Challenge CRs | SKILL.md:377 (troubleshooting row: "Certificate stuck in Issuing", fix includes `kubectl describe certificate mclaude-cp-wildcard`) | IMPLEMENTED | — | Round 1 PARTIAL now resolved: spec-tls-certs.md:208 has the dedicated bullet; skill troubleshooting table also has the row |

---

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| charts/mclaude-cp/templates/certificate.yaml:9-10 | INFRA | `{{- include "mclaude-cp.labels" . | nindent 4 }}` standard label injection; required helm boilerplate, not individually specced but implied by all helm templates |
| charts/mclaude-cp/values.yaml:245-261 | INFRA | Comment header and `tls.certManager` block; directly implements ADR-0097:117-131 and spec-helm.md:129-135 |
| charts/mclaude-cp/values-k3d-ghcr.yaml:60-62 | INFRA | Comment explaining no secretName duplication; documentation of the single-source-of-truth pattern from ADR-0097:34-35 |
| SKILL.md:93-94 | INFRA | `NS="mclaude-system"` and `RELEASE="mclaude-cp"` variable declarations; implementation of ADR-0097 release naming |
| SKILL.md:283-289 | INFRA | `mclaude-postgres` helm install block; implements ADR-0094/spec-helm.md postgres-first install order |
| SKILL.md:292-324 | INFRA | `mclaude-cp` helm install block with HELM_ARGS array; implements skill algorithm per spec-helm.md install pattern |
| SKILL.md:331-337 | INFRA | `kubectl wait` Certificate-Ready block; implements ADR-0097:162-168 wait semantics |

---

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0097:85-112 | certificate.yaml template structure | None found | None found | UNTESTED |
| adr-0097:117-131 | values.yaml tls.certManager defaults | None found | None found | UNTESTED |
| adr-0097:133-154 | values-k3d-ghcr.yaml tls.certManager enabled with dev hostnames | None found | None found | UNTESTED |
| adr-0097:162-168 | skill: kubectl wait after helm install mclaude-cp | None found | None found | UNTESTED |
| spec-tls-certs.md:84-106 | Certificate CR rendered when enabled=true | None found | None found | UNTESTED |
| spec-tls-certs.md:116 | enabled=false skips Certificate template | None found | None found | UNTESTED |

Note: Integration test cases for the Certificate lifecycle are documented in ADR-0097 (lines 252-260) but no automated test files exist yet. These are operational (require a live cluster with cert-manager + DO API token) and are consistent with the pattern for other cluster-bootstrap resources in this codebase.

---

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` whose `**Component**:` matches `charts-mclaude-cp` or `deploy-local-preview`.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| — | — | — | No matching open bugs |

---

### Summary

- Implemented: 14
- Gap: 0
- Partial: 0
- Infra: 7
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 0
- Untested: 6 (Certificate lifecycle — live-cluster integration tests; no automated test infra for helm chart templates in this repo)
- Bugs fixed: 0
- Bugs open: 0
