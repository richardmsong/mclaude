## Run: 2026-05-04T00:00:00Z

Components: `charts-mclaude-cp` (charts/mclaude-cp/) + `deploy-local-preview` skill (.agent/skills/deploy-local-preview/SKILL.md)
ADR: docs/adr-0097-tls-cert-in-helm-chart.md (accepted)
Specs: docs/charts-mclaude/spec-helm.md, docs/spec-tls-certs.md

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0097:85-113 | `templates/certificate.yaml` (NEW): gated by `{{- if .Values.tls.certManager.enabled -}}`, `apiVersion: cert-manager.io/v1`, `kind: Certificate` | charts/mclaude-cp/templates/certificate.yaml:1-23 | IMPLEMENTED | — | Template exists, correct apiVersion/kind, correct if-gate |
| adr-0097:96-97 | `helm.sh/resource-policy: keep` annotation on Certificate template | charts/mclaude-cp/templates/certificate.yaml:8 | IMPLEMENTED | — | Annotation present at correct location |
| adr-0097:101 | `secretName: {{ (index .Values.ingress.tls 0).secretName }}` — sourced from ingress.tls[0].secretName | charts/mclaude-cp/templates/certificate.yaml:12 | IMPLEMENTED | — | Exact expression matches spec |
| adr-0097:103-104 | `issuerRef.kind: default "ClusterIssuer"` + `required` directive for issuerRef.name | charts/mclaude-cp/templates/certificate.yaml:14-15 | IMPLEMENTED | — | Default and required match spec |
| adr-0097:105 | `required "tls.certManager.commonName is required..."` | charts/mclaude-cp/templates/certificate.yaml:16 | IMPLEMENTED | — | Required directive for commonName present |
| adr-0097:107-109 | `required "tls.certManager.dnsNames is required..."` + range | charts/mclaude-cp/templates/certificate.yaml:17-20 | IMPLEMENTED | — | Required directive for dnsNames + range present |
| adr-0097:110-111 | `duration: default "2160h"` + `renewBefore: default "720h"` | charts/mclaude-cp/templates/certificate.yaml:21-22 | IMPLEMENTED | — | Defaults match spec |
| adr-0097:117-131 | `values.yaml`: new `tls.certManager.*` block with `enabled: false`, `issuerRef.kind: ClusterIssuer`, `issuerRef.name: ""`, `commonName: ""`, `dnsNames: []`, `duration: 2160h`, `renewBefore: 720h` | charts/mclaude-cp/values.yaml:251-260 | IMPLEMENTED | — | All fields and defaults match spec exactly |
| adr-0097:34 | `tls.certManager.enabled` default `true` (spec Decision table says "Default-true so dev/k3d works out of the box") vs values.yaml says `false` | charts/mclaude-cp/values.yaml:253 | GAP | SPEC→FIX | Decision table row says "default true" but also says "Default-false in values.yaml" — the body text at line 34 is internally contradictory with the explanation at line 37. The code implements `false` (matches ADR clarification in lines 37 and 36). Spec text at line 34 is inconsistent; values.yaml is correct. |
| adr-0097:134-155 | `values-k3d-ghcr.yaml`: new `tls:` block with `certManager.enabled: true`, `issuerRef.name: mclaude-letsencrypt-prod`, `commonName: "*.mclaude.richardmcsong.com"`, `dnsNames: ["*.mclaude.richardmcsong.com", "mclaude.richardmcsong.com"]` | charts/mclaude-cp/values-k3d-ghcr.yaml:63-72 | IMPLEMENTED | — | Block present with all required fields and correct values |
| adr-0097:157-159 | `values-aks.yaml` leaves `tls.certManager.enabled` at default `false` — no new content needed | charts/mclaude-cp/values-aks.yaml | IMPLEMENTED | — | No tls block in values-aks.yaml; inherits false from values.yaml |
| adr-0097:157-159 | `values-airgap.yaml` leaves `tls.certManager.enabled` at default `false` — no new content needed | charts/mclaude-cp/values-airgap.yaml | IMPLEMENTED | — | No tls block in values-airgap.yaml; inherits false from values.yaml |
| adr-0097:163 | `/deploy-local-preview` skill: Remove the `kubectl apply -f -` step that creates the Certificate CR | .agent/skills/deploy-local-preview/SKILL.md:186-207 | IMPLEMENTED | — | Step 4 has a note removing Certificate apply; only ClusterIssuer kubectl apply remains. No Certificate CR is applied. |
| adr-0097:163 | Keep the wait step but move it after `helm install mclaude-cp` and update resource name to `mclaude-cp-wildcard` | .agent/skills/deploy-local-preview/SKILL.md:320-326 | IMPLEMENTED | — | Wait step is after helm install, uses certificate/mclaude-cp-wildcard |
| adr-0097:163 | `kubectl wait --for=condition=Ready certificate/mclaude-cp-wildcard -n mclaude-system --timeout=5m` | .agent/skills/deploy-local-preview/SKILL.md:323-325 | IMPLEMENTED | — | Exact command matches spec |
| adr-0097:168-169 | Skill: `helm install mclaude-postgres [...] helm install mclaude-cp charts/mclaude-cp [...]` — install uses mclaude-cp chart | .agent/skills/deploy-local-preview/SKILL.md:288,313 | GAP | CODE→FIX | Skill still uses `charts/mclaude/values-k3d-ghcr.yaml` (line 288) and `./charts/mclaude` (line 313) — that directory does not exist. ADR specifies `charts/mclaude-cp`. The skill would fail to install at runtime. |
| spec-helm.md:51-55 | `mclaude-cp` chart: Certificate `{release}-wildcard`, rendered only when `tls.certManager.enabled=true`, `helm.sh/resource-policy: keep` | charts/mclaude-cp/templates/certificate.yaml:1-23 | IMPLEMENTED | — | Matches spec-helm.md TLS Certificate table entry |
| spec-helm.md:129-135 | Configuration knobs for `tls.certManager.*`: enabled, issuerRef.kind, issuerRef.name, commonName, dnsNames, duration, renewBefore — defaults and descriptions | charts/mclaude-cp/values.yaml:251-260 | IMPLEMENTED | — | All knobs present with matching defaults |
| spec-helm.md:361 | `values-k3d-ghcr.yaml` sets `tls.certManager.enabled: true` with `issuerRef.name: mclaude-letsencrypt-prod`, `commonName`, `dnsNames` covering wildcard + apex | charts/mclaude-cp/values-k3d-ghcr.yaml:63-72 | IMPLEMENTED | — | Correct |
| spec-helm.md:362 | `values-airgap.yaml` leaves `tls.certManager.enabled` at default `false` | charts/mclaude-cp/values-airgap.yaml | IMPLEMENTED | — | No tls block; inherits false |
| spec-helm.md:363 | `values-aks.yaml` leaves `tls.certManager.enabled` at default `false` | charts/mclaude-cp/values-aks.yaml | IMPLEMENTED | — | No tls block; inherits false |
| spec-tls-certs.md:83-116 | Certificate CR: owned by `mclaude-cp` chart, rendered when `tls.certManager.enabled=true`, `helm.sh/resource-policy: keep`, name `{release}-wildcard`, namespace `mclaude-system`, secretName from `ingress.tls[0]`, required directives for issuerRef.name/commonName/dnsNames | charts/mclaude-cp/templates/certificate.yaml:1-23 | IMPLEMENTED | — | All fields match spec-tls-certs.md Certificate CR section |
| spec-tls-certs.md:188-193 | `kubectl wait --for=condition=Ready certificate/mclaude-cp-wildcard -n mclaude-system --timeout=5m` must be explicit after helm install; skill keeps this wait | .agent/skills/deploy-local-preview/SKILL.md:320-326 | IMPLEMENTED | — | Matches |
| spec-tls-certs.md:199 | `/deploy-local-preview` skill: Certificate CR not owned by skill; skill runs `helm install mclaude-cp` with values overlay that opts in (`values-k3d-ghcr.yaml`) | .agent/skills/deploy-local-preview/SKILL.md:288,313 | GAP | CODE→FIX | Skill invokes `helm upgrade --install mclaude ./charts/mclaude` (non-existent path) instead of `helm upgrade --install mclaude-cp ./charts/mclaude-cp`. The release name is also `mclaude` not `mclaude-cp` — this means the Certificate name would render as `mclaude-wildcard` not `mclaude-cp-wildcard` (since Certificate name is `{release}-wildcard`). `kubectl wait certificate/mclaude-cp-wildcard` at line 323 would then wait for the wrong resource name. |
| spec-tls-certs.md:200-203 | Component responsibilities still list `charts/mclaude/templates/ingress.yaml`, `charts/mclaude/templates/nats-ws-ingress.yaml`, `charts/mclaude/values.yaml` as separate rows — these are the old pre-ADR-0035 chart paths | spec-tls-certs.md:200-203 (spec only) | GAP | SPEC→FIX | These rows in spec-tls-certs.md Component Responsibilities reference the old monolithic `charts/mclaude` chart. The actual chart is `charts/mclaude-cp`. Spec should be updated to reflect current chart names. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| charts/mclaude-cp/templates/certificate.yaml:1-23 | INFRA | All lines covered by Phase 1 spec rows; correct if-gate, annotations, required directives |
| charts/mclaude-cp/values.yaml:251-260 | INFRA | tls.certManager block — all fields reviewed against spec |
| charts/mclaude-cp/values-k3d-ghcr.yaml:60-72 | INFRA | ADR-0097 comment + tls block reviewed against spec |
| charts/mclaude-cp/values-aks.yaml (tls omission) | INFRA | Intentional omission per spec; default false inherited |
| charts/mclaude-cp/values-airgap.yaml (tls omission) | INFRA | Intentional omission per spec; default false inherited |
| .agent/skills/deploy-local-preview/SKILL.md:286-313 | UNSPEC'd | Helm args use `charts/mclaude` (non-existent path) and release name `mclaude` instead of `charts/mclaude-cp` and release `mclaude-cp`. This is a functional bug producing the wrong Certificate name at runtime and referencing a non-existent chart path. Covered by GAP finding above — not dead code, but incorrect code. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0097:85-113 | certificate.yaml template rendered when tls.certManager.enabled=true | None found | None found | UNTESTED |
| adr-0097:96-97 | helm.sh/resource-policy: keep annotation present | None found | None found | UNTESTED |
| adr-0097:101 | secretName sourced from ingress.tls[0].secretName | None found | None found | UNTESTED |
| adr-0097:103-115 | required directives fail-fast on missing fields | None found | None found | UNTESTED |
| adr-0097:117-131 | values.yaml tls.certManager defaults | None found | None found | UNTESTED |
| adr-0097:134-155 | values-k3d-ghcr.yaml tls block enabled | None found | None found | UNTESTED |
| adr-0097:163/skill | kubectl wait uses mclaude-cp-wildcard after helm install | None found | None found | UNTESTED |

### Phase 4 — Bug Triage

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| (none) | No bugs in .agent/bugs/ match this component | — | — |

### Summary

- Implemented: 19
- Gap: 3
- Partial: 0
- Infra: 6
- Unspec'd: 1
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 0
- Untested: 7
- Bugs fixed: 0
- Bugs open: 0
