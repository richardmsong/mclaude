## Run: 2026-05-04T00:00:00Z (Round 1 — initial)

CLEAN — no blocking gaps found.

---

## Run: 2026-05-04T01:00:00Z (Round 2 — post-gap-fixes)

**Gaps found: 2**

1. **`helm --wait` does not wait for cert-manager to issue the TLS Secret** — The Component Changes section states "helm's `--wait` blocks until all resources, including cert-manager-issued Secrets referenced by Ingress, are Ready." This is factually incorrect. Helm's `--wait` blocks until Pods are Running/Ready and Services have endpoints; it does NOT wait for cert-manager to complete the DNS-01 challenge and populate the TLS Secret. The Secret is created by cert-manager as a side-effect of the Certificate becoming Ready, and helm has no knowledge of it. A developer implementing this would remove the `kubectl wait --for=condition=Ready certificate/...` step (as the ADR instructs) and replace it with `helm install --wait`, believing that covers cert readiness — but it does not. On a fresh install the TLS Secret may not exist when helm returns, causing the Ingress to serve Traefik's self-signed cert. The ADR needs to either (a) keep the `kubectl wait` for the Certificate after `helm install`, or (b) explicitly acknowledge that `--wait` does not cover cert issuance and that the operator must poll separately, or (c) describe how the skill should wait for the Certificate separately. As written, a developer cannot implement this correctly.
   - **Doc**: "The wait moves into `helm install mclaude-cp --wait` (which the skill must pass when invoking helm if not already): helm's `--wait` blocks until all resources, including cert-manager-issued Secrets referenced by Ingress, are Ready." (Component Changes / `/deploy-local-preview` skill section)
   - **Code**: The skill at `.agent/skills/deploy-local-preview/SKILL.md` line 229–233 has `kubectl wait --for=condition=Ready certificate/mclaude-richardmcsong-wildcard -n mclaude-system --timeout=5m` — this is the step being removed, but the replacement (`helm --wait`) does not provide equivalent coverage. Helm's `--wait` documentation and behavior confirm it only observes Pod/Service readiness, not custom resource conditions.

2. **Skill still applies the Certificate CR and `kubectl wait` — no instruction on what replaces them in the skill body** — The ADR instructs: "Remove both the `kubectl apply -f -` step that creates the Certificate CR and the subsequent `kubectl wait --for=condition=Ready certificate/...` step." The existing skill (`.agent/skills/deploy-local-preview/SKILL.md`, Step 4, lines 206–233) still has both the `kubectl apply` of `Certificate mclaude-richardmcsong-wildcard` and the `kubectl wait`. The ADR says these are removed but does not specify what the skill's Step 4 block looks like after removal — specifically, whether anything waits for cert issuance before proceeding to Step 5 (image build) and Step 6 (helm install). The ADR implies `helm install --wait` in Step 6 covers it, but as noted in Gap 1, that is incorrect. A developer implementing the skill edit would be left without a correct wait mechanism and without any instruction about what to substitute for the removed `kubectl wait` line.
   - **Doc**: "Remove **both** the `kubectl apply -f -` step that creates the Certificate CR and the subsequent `kubectl wait --for=condition=Ready certificate/...` step." (Component Changes / `/deploy-local-preview` skill section). No substitute wait mechanism is specified.
   - **Code**: `.agent/skills/deploy-local-preview/SKILL.md` lines 206–233 — both steps being removed are present; no alternative wait is mentioned in the ADR's description of the post-change skill.

---

## Run: 2026-05-04T02:00:00Z (Round 3 — post-round-2-fixes)

CLEAN — no blocking gaps found.

Both Round 2 gaps are resolved:

- ADR Component Changes (`/deploy-local-preview` skill section, lines 162–181) now shows the explicit `kubectl wait --for=condition=Ready certificate/mclaude-cp-wildcard -n mclaude-system --timeout=5m` placed after `helm install mclaude-cp`, with an unambiguous explanation that `helm install --wait` only observes Pod/Service readiness and does NOT wait for cert-manager's reconcile loop.
- `docs/spec-tls-certs.md` "First-time issuance expected duration" section (lines 188–193) drops the incorrect claim and includes the same explicit `kubectl wait` with the same explanation and the correct post-ADR-0097 certificate name (`mclaude-cp-wildcard`).

All other design elements verified against the codebase:
- `charts/mclaude-cp/templates/certificate.yaml` does not yet exist (expected — this is the implementation target).
- `charts/mclaude-cp/values.yaml` has no `tls.certManager` block yet (expected — implementation target); `tls: []` exists only under `ingress`.
- `charts/mclaude-cp/values-k3d-ghcr.yaml` has no `tls:` top-level key yet (expected — implementation target); `ingress.tls[0].secretName` is already `mclaude-richardmcsong-tls`, which the new template references via `{{ (index .Values.ingress.tls 0).secretName }}`.
- `mclaude-cp.fullname` helper: release name `mclaude-cp` contains chart name `mclaude-cp`, so `fullname` resolves to `mclaude-cp`; Certificate name `mclaude-cp-wildcard` is correct.
- `docs/adr-0033-letsencrypt-tls.md` already carries the supersede note at line 9.
- `docs/charts-mclaude/spec-helm.md` already documents the `tls.certManager.*` values keys and the Certificate template per ADR-0097.
- `.agent/skills/deploy-local-preview/SKILL.md` still has the old Certificate apply + wait (expected — skill update is an implementation task described by the ADR, not yet executed).
