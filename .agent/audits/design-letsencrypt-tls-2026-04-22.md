## Audit: 2026-04-22T09:00:00Z

**Document:** docs/adr-0033-letsencrypt-tls.md

### Round 1

**Gaps found: 8** (see `## Run: 2026-04-22T09:30:00Z` block below for full evaluator report).

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | k3d `--port "53:30053/udp@server:0"` orphaned | ADR Component Changes / skill: explicit bullet to remove that flag from Step 1 | factual |
| 2 | `values-k3d-ghcr.yaml` silent on `externalDnsTarget` | ADR clarifies: do NOT set it in the file; only passed via `--set` at install time because the Tailscale IP isn't known at file-edit time | factual |
| 3 | `txtOwnerId=mclaude-k3d` missing from ExternalDNS Helm snippet | Added `--set txtOwnerId=mclaude-k3d` to the snippet | factual |
| 4 | Bitwarden item name unspecified | Chosen: exact name `"DigitalOcean API token — richardmcsong.com zone edit"`, documented with full `bw get password` invocation. Both the skill prerequisites and the Bitwarden section now name the exact item. | decision |
| 5 | CI workflow change list underspecified | Expanded the `.github/workflows/deploy-preview.yml` section to list every `--set` flag (host, natsHost, tls[0].secretName, tls[0].hosts[0..1], externalDnsTarget, externalUrl, natsWsUrl) and note that cert-manager/ExternalDNS are NOT re-installed by CI | factual |
| 6 | Template annotation pattern ambiguous | Chosen: conditional second annotations block on `ingress.externalDnsTarget`. Exact YAML pattern added to ADR. Keeps the template opt-in for non-k3d deployments. | decision |
| 7 | `values.yaml` default change would break non-k3d envs | Chosen: leave `ingress.tls` default as `[]`; put TLS stanza in `values-k3d-ghcr.yaml` and as `--set` flags in CI workflow. Non-k3d values files unchanged. | decision |
| 8 | `spec-tls-certs.md` had no content outline in ADR | Impact section expanded to enumerate sections + normative resource names of the already-written spec file | factual |

### Round 2

**Gaps found: 3**

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | `spec-helm.md` documented `ingress.tls` default as non-empty; ADR says keep it `[]` | Reverted `spec-helm.md` default row to `[]` and added note about per-env override pattern (k3d / CI supplies it, others leave empty) | factual |
| 2 | Release name `mclaude-<slug>` in ADR vs `mclaude-preview-<slug>` in existing workflow + cleanup | Updated ADR User Flow and CI workflow section to use `mclaude-preview-${BRANCH_SLUG}` (existing convention; changing it would break `cleanup-preview.yml`) | factual |
| 3 | `values.yaml` lacks `ingress.externalDnsTarget`, which spec-helm.md already documents — timing of spec vs code unclear | Added explicit note in ADR Component Changes that spec files land in this plan-feature commit (post-impl state) and values.yaml / template edits land in the subsequent `/feature-change` commits | factual |

### Round 3

**Gaps found: 2**

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | `spec-tls-certs.md` Component Responsibilities table said `values.yaml` default `ingress.tls` references `mclaude-richardmcsong-tls` — contradicting the `[]` decision | Updated rows for `ingress.yaml` and `values.yaml` in the responsibilities table to match: default is `[]`, k3d/CI supply the wildcard secret via values-k3d-ghcr.yaml / --set | factual |
| 2 | `spec-tls-certs.md` still used the old `mclaude-<slug>` release name | Updated to `mclaude-preview-${BRANCH_SLUG}` to match ADR + existing workflow + cleanup-preview.yml | factual |

### Round 4

**Gaps found: 2**

#### Fixes applied

| # | Gap | Resolution | Type |
|---|-----|-----------|------|
| 1 | `docs/_sidebar.md` linked to deleted `spec-tailscale-dns.md` and had no entry for new `spec-tls-certs.md` | Swapped the sidebar entry: `[Tailscale DNS](spec-tailscale-dns.md)` → `[TLS Certificates](spec-tls-certs.md)`. ADR Impact section updated to mention the sidebar. | factual |
| 2 | `docs/spec-doc-layout.md` used `spec-tailscale-dns.md` as a canonical cross-cutting example | Replaced with `spec-tls-certs.md` in the Partitioning examples row. ADR Impact section updated. | factual |

### Round 5

CLEAN — no blocking gaps found.

### Result

**CLEAN** after 5 rounds, 15 total gaps resolved (12 factual fixes, 3 design decisions).

## Run: 2026-04-22T09:30:00Z

**Gaps found: 8**

1. **k3d cluster creation: `--port "53:30053/udp@server:0"` is no longer needed but the ADR never says to remove it** — The current skill creates the k3d cluster with `--port "53:30053/udp@server:0"` for the CoreDNS NodePort. The ADR deletes Step 5 (CoreDNS zone) and Step 6 (Tailscale split DNS) but never mentions updating the `k3d cluster create` command in Step 1 to drop this port flag. A developer implementing the skill rewrite would have to guess whether to keep or remove it.
   - **Doc**: "Step 5 (CoreDNS custom zone): **deleted entirely**." and "Step 6 (Tailscale split DNS): **deleted entirely**." — no mention of the `k3d cluster create` port flag.
   - **Code**: `.claude/skills/deploy-local-preview/SKILL.md` line 75: `--port "53:30053/udp@server:0"` inside the `k3d cluster create` call.

2. **`values-k3d-ghcr.yaml` change list is silent on whether `ingress.externalDnsTarget` should appear in that file** — The ADR says `values.yaml` gets `ingress.externalDnsTarget: ""` as a new default, and the skill passes it at runtime via `--set`. The `values-k3d-ghcr.yaml` Component Changes section lists five specific overrides to update (`ingress.host`, `ingress.natsHost`, `ingress.tls[0].secretName`, `controlPlane.externalUrl`, `controlPlane.config.natsWsUrl`) but says nothing about `ingress.externalDnsTarget`. A developer updating `values-k3d-ghcr.yaml` cannot tell whether to add the key (even as `""`) or omit it.
   - **Doc**: Component Changes `charts/mclaude/` → `values-k3d-ghcr.yaml`: lists five keys, no mention of `ingress.externalDnsTarget`.
   - **Code**: `charts/mclaude/values-k3d-ghcr.yaml` has no `externalDnsTarget` key today.

3. **ExternalDNS `txtOwnerId` is specified in the narrative but absent from the installable Helm snippet** — User Flow step 5 says install ExternalDNS with `txtOwnerId=mclaude-k3d`, but the Component Changes Helm snippet does not include `--set txtOwnerId=mclaude-k3d`. A developer copying the snippet to implement the skill or CI workflow would deploy ExternalDNS without a txt owner ID, causing ownership conflicts if the local k3d ExternalDNS and a CI instance manage the same DigitalOcean zone simultaneously.
   - **Doc**: User Flow step 5: "...`txtOwnerId=mclaude-k3d`..." vs Component Changes ExternalDNS Helm snippet: no `--set txtOwnerId` line.
   - **Code**: Not yet implemented; the contradiction is between two sections of the ADR itself.

4. **No Bitwarden item name or ID is specified for the DigitalOcean API token** — The skill must call `bw get password "<item>"` to retrieve the DO token at deploy time. The Prerequisites section says to "add entry for 'DigitalOcean API token with write access to `richardmcsong.com` zone' and the Bitwarden entry name that holds it" but that entry name is never supplied. A developer cannot write a working `bw get` command without it.
   - **Doc**: "New Prerequisites: ...add entry for 'DigitalOcean API token...' and the Bitwarden entry name that holds it." — entry name is stated as a to-do but never resolved in the ADR.
   - **Code**: `.claude/skills/deploy-local-preview/SKILL.md` uses `bw get password "YOUR_BITWARDEN_GITHUB_OAUTH_ITEM_ID"` as the existing pattern; no DO token lookup exists yet.

5. **CI preview workflow changes are underspecified — `ingress.natsHost`, `ingress.externalDnsTarget`, `ingress.tls`, and `controlPlane.config.natsWsUrl` are not mentioned** — The Component Changes section for `.github/workflows/deploy-preview.yml` says only "`PREVIEW_HOST` changes... No other workflow changes." But the ADR's own User Flow preview step 3 requires the Helm install to pass `ingress.natsHost=nats-preview-<slug>.mclaude.richardmcsong.com`, `ingress.tls[0].secretName=mclaude-richardmcsong-tls`, and `ingress.externalDnsTarget=${TS_IP}`. The DNS records table also lists `nats-preview-<slug>` A records, which can only exist if `natsHost` is set. "No other workflow changes" directly contradicts the user flow spec.
   - **Doc**: "Component Changes / `.github/workflows/deploy-preview.yml`": "`PREVIEW_HOST` changes... No other workflow changes." vs User Flow preview step 3: `ingress.natsHost=nats-preview-<slug>...`, `ingress.tls[0].secretName=mclaude-richardmcsong-tls`, `ingress.externalDnsTarget=${TS_IP}`.
   - **Code**: Current `.github/workflows/deploy-preview.yml` line 220 sets only `ingress.host`; no `ingress.natsHost`, `ingress.tls`, `ingress.externalDnsTarget`, or `controlPlane.config.natsWsUrl`.

6. **`ingress.yaml` template currently uses a single `toYaml` annotations block; the ADR's per-field conditional approach is incompatible without specifying the implementation pattern** — The ADR says to "emit annotations `external-dns.alpha.kubernetes.io/hostname` and `external-dns.alpha.kubernetes.io/target` when `ingress.externalDnsTarget` is non-empty." The existing template renders `{{- with .Values.ingress.annotations }} {{- toYaml . | nindent 4 }} {{- end }}` as a single dict dump. A developer must choose: (a) add a second conditional block after the existing annotations block, or (b) fold these annotations into `ingress.annotations` at values level (no template change needed). The ADR description implies (a) but the current template pattern supports (b) at lower cost. The ADR gives no guidance; the choice affects whether ExternalDNS annotations appear conditionally in the template or unconditionally in environment-specific values files. Same ambiguity applies to `nats-ws-ingress.yaml`.
   - **Doc**: Component Changes `charts/mclaude/` → `templates/ingress.yaml`: "emit annotations `external-dns.alpha.kubernetes.io/hostname` and `external-dns.alpha.kubernetes.io/target` when `ingress.externalDnsTarget` is non-empty."
   - **Code**: `charts/mclaude/templates/ingress.yaml` lines 10-13: single `{{- with .Values.ingress.annotations }}` block with `toYaml`; no per-value conditionals.

7. **Changing `values.yaml` default `ingress.tls` to a non-empty cert Secret breaks non-k3d environments; the ADR does not specify which other values files need to be updated** — The ADR proposes setting the `values.yaml` default for `ingress.tls` to `[{secretName: mclaude-richardmcsong-tls, hosts: []}]`. The current default is `tls: []`. With this change, any deployment using `values-aks.yaml` or `values-airgap.yaml` that does not explicitly override `ingress.tls` will reference `mclaude-richardmcsong-tls`, a Secret that does not exist outside k3d. The ADR does not say to update `values-aks.yaml`, `values-airgap.yaml`, or any other environment values file.
   - **Doc**: "`values.yaml`: ...Set `ingress.tls` default to `[{secretName: mclaude-richardmcsong-tls, hosts: []}]` so every deploy references the shared Secret without per-env plumbing."
   - **Code**: `charts/mclaude/values-aks.yaml` and `charts/mclaude/values-airgap.yaml` both exist and currently inherit `tls: []` from `values.yaml`; neither is mentioned in the ADR.

8. **`docs/spec-tls-certs.md` is listed as a deliverable with no content outline, yet `spec-helm.md` already contains a forward reference to it** — The Impact section lists "New `docs/spec-tls-certs.md` (cross-cutting)" and the already-updated `docs/charts-mclaude/spec-helm.md` Dependencies section contains `See docs/spec-tls-certs.md`. The file does not exist. The ADR describes the cert-manager objects in prose but provides no normative YAML, no required field values, and no heading structure for the spec. A developer creating this file cannot determine what it must establish as canonical contract (exact ClusterIssuer name, exact Certificate CR name, exact Secret name) vs what is informational.
   - **Doc**: Impact: "New `docs/spec-tls-certs.md` (cross-cutting) — describes cert-manager runtime, ClusterIssuer, DigitalOcean DNS-01 solver, renewal cadence, the wildcard Certificate CR, and token rotation." — one sentence, no outline.
   - **Code**: `docs/charts-mclaude/spec-helm.md` line 192: "See `docs/spec-tls-certs.md`." — file does not exist.

## Run: 2026-04-22T11:00:00Z

**Round 2 audit — re-evaluation after round 1 fixes**

**Gaps found: 3**

1. **`spec-helm.md` already states `ingress.tls` default is non-empty, directly contradicting the ADR's own instruction to leave `values.yaml` default as `[]`** — The ADR's Component Changes section says "**Leave `ingress.tls` default as `[]`** so non-k3d environments (`values-aks.yaml`, `values-airgap.yaml`) are untouched." The Impact section says `spec-helm.md` is updated in this commit. The already-updated `docs/charts-mclaude/spec-helm.md` (line 172) now documents the `ingress.tls` default as `[{secretName: mclaude-richardmcsong-tls, hosts: []}]`. The actual `charts/mclaude/values.yaml` still has `tls: []`. A developer implementing this change sees two authoritative sources giving opposite instructions: the ADR says leave it as `[]`; the already-committed spec-helm.md says the default is the non-empty wildcard entry. They cannot implement without stopping to ask which is correct.
   - **Doc**: Component Changes `charts/mclaude/` → `values.yaml`: "**Leave `ingress.tls` default as `[]`**"
   - **Code**: `docs/charts-mclaude/spec-helm.md` line 172: "`ingress.tls` | `[{secretName: mclaude-richardmcsong-tls, hosts: []}]` | TLS configuration…" vs `charts/mclaude/values.yaml` line 199: `tls: []`

2. **CI workflow release name is ambiguous — ADR says `mclaude-<slug>` but the existing workflow uses `mclaude-preview-${BRANCH_SLUG}`** — The ADR User Flow preview section step 3 says: "Workflow runs `helm upgrade --install mclaude-<slug> charts/mclaude`". The existing `deploy-preview.yml` uses release name `mclaude-preview-${BRANCH_SLUG}` (line 210), and the companion `cleanup-preview.yml` also uses `mclaude-preview-${slug}` (line 22). If a developer takes the ADR literally and renames the release format to `mclaude-<slug>`, the cleanup workflow breaks (it would never find the release to uninstall). If the developer preserves the existing `mclaude-preview-` prefix, the ADR's stated release name is wrong. The ADR does not say whether to change the release name format.
   - **Doc**: User Flow preview step 3: "`helm upgrade --install mclaude-<slug> charts/mclaude`"
   - **Code**: `.github/workflows/deploy-preview.yml` line 210: `helm upgrade --install "mclaude-preview-${BRANCH_SLUG}"` and `.github/workflows/cleanup-preview.yml` line 22: `RELEASE="mclaude-preview-${slug}"`

3. **`ingress.externalDnsTarget` is not added to `values.yaml` despite the ADR requiring it as a new key** — The ADR states under Component Changes `charts/mclaude/` → `values.yaml`: "add `ingress.externalDnsTarget: \"\"` (empty default — skill / CI sets it at install time)." The current `charts/mclaude/values.yaml` has no `ingress.externalDnsTarget` key. The `spec-helm.md` already documents this key with its description and default. Without the key in `values.yaml`, the Helm template's `{{- if .Values.ingress.externalDnsTarget }}` conditional works (absent key evaluates falsy) but the chart is incomplete per its own spec, and `helm lint` / `values.yaml` review will show a missing documented key. More importantly, a developer reading the spec-helm.md will find the key documented as a real value with a default, and read the ADR saying to add it — the question "do I add it or did I miss something?" is a genuine stop.
   - **Doc**: Component Changes `charts/mclaude/` → `values.yaml`: "add `ingress.externalDnsTarget: \"\"` (empty default)"
   - **Code**: `charts/mclaude/values.yaml` has no `ingress.externalDnsTarget` key; `docs/charts-mclaude/spec-helm.md` line 173 documents it as an existing key with description

## Run: 2026-04-22T13:00:00Z

**Round 3 audit — re-evaluation after round 2 fixes**

**Gaps found: 2**

1. **`spec-tls-certs.md` Component Responsibilities table contradicts the ADR and `spec-helm.md` on the `values.yaml` `ingress.tls` default** — The ADR (Component Changes, `values.yaml`) says "**Leave `ingress.tls` default as `[]`**". `spec-helm.md` line 172 agrees: default is `[]`. But `docs/spec-tls-certs.md` line 190 says "Default `ingress.tls` **references `mclaude-richardmcsong-tls`**". Line 188 also says the ingress template "References `ingress.tls[0].secretName` (default `mclaude-richardmcsong-tls`)" — implying the default is non-empty. A developer implementing `values.yaml` reads the ADR and `spec-helm.md` saying leave it `[]`, then reads the canonical TLS spec saying the default references the wildcard secret. These are direct contradictions; the developer must stop and ask which is authoritative.
   - **Doc**: ADR Component Changes `values.yaml`: "**Leave `ingress.tls` default as `[]`**". `docs/charts-mclaude/spec-helm.md` line 172: `ingress.tls | [] | ...`
   - **Code**: `docs/spec-tls-certs.md` line 188: "References `ingress.tls[0].secretName` (default `mclaude-richardmcsong-tls`)"; line 190: "Default `ingress.tls` references `mclaude-richardmcsong-tls`"

2. **`spec-tls-certs.md` still uses the old `mclaude-<slug>` release name format that was corrected in the ADR (round 2 fix)** — The round 2 fix updated the ADR to use `mclaude-preview-${BRANCH_SLUG}` throughout (matching the existing workflow and `cleanup-preview.yml`). But `docs/spec-tls-certs.md` line 187 was not updated: it still says "`helm upgrade --install mclaude-<slug>`". The spec file is the canonical contract per the ADR's Impact section. A developer reading the spec sees a different release name than the ADR and the existing workflow, creating an ambiguity about whether to use `mclaude-<slug>` (spec) or `mclaude-preview-${BRANCH_SLUG}` (ADR + existing workflow).
   - **Doc**: ADR User Flow preview step 3: "`helm upgrade --install mclaude-preview-${BRANCH_SLUG} charts/mclaude`"
   - **Code**: `docs/spec-tls-certs.md` line 187: "Only runs `helm upgrade --install mclaude-<slug>`"; `.github/workflows/cleanup-preview.yml` line 22: `RELEASE="mclaude-preview-${slug}"`

## Run: 2026-04-22T15:00:00Z

**Round 4 audit — re-evaluation after round 3 fixes**

**Gaps found: 2**

1. **`docs/_sidebar.md` still links to `spec-tailscale-dns.md`, which the ADR deletes — broken link after implementation, and the new `spec-tls-certs.md` has no sidebar entry** — The ADR Impact section says `docs/spec-tailscale-dns.md` is deleted entirely. `docs/_sidebar.md` line 6 has an active navigation link `[Tailscale DNS](spec-tailscale-dns.md)`. After the deletion this is a broken sidebar link. Simultaneously, the new `docs/spec-tls-certs.md` (created by this ADR) has no corresponding sidebar entry. The Impact section does not mention `docs/_sidebar.md` at all. A developer implementing the file deletion would encounter an inconsistent sidebar and must stop to decide whether to update it, and if so, what the new entry should say.
   - **Doc**: Impact section lists files to delete, edit, or create; `docs/_sidebar.md` is absent.
   - **Code**: `docs/_sidebar.md` line 6: `[Tailscale DNS](spec-tailscale-dns.md)` — live link to the file being deleted. No entry for `spec-tls-certs.md`.

2. **`docs/spec-doc-layout.md` uses `spec-tailscale-dns.md` as a canonical example of a cross-cutting spec, which becomes stale when that file is deleted** — `docs/spec-doc-layout.md` line 15 has `docs/spec-tailscale-dns.md` in the "Cross-cutting spec" examples column. After the ADR's deletion, this example points to a non-existent file. The Impact section does not list `spec-doc-layout.md` as a file to update. A developer must either leave a stale example in the authoritative layout doc or stop and ask whether to update it (and if so, to what — `spec-tls-certs.md` would be the natural replacement example).
   - **Doc**: ADR Impact section: does not mention `docs/spec-doc-layout.md`.
   - **Code**: `docs/spec-doc-layout.md` line 15: `| Cross-cutting spec | \`docs/spec-<concern>.md\` | \`docs/spec-state-schema.md\`, \`docs/spec-tailscale-dns.md\` |`

## Run: 2026-04-22T17:00:00Z

**Round 5 audit — re-evaluation after round 4 fixes**

CLEAN — no blocking gaps found.

Verification:
- `docs/_sidebar.md` line 7: `[TLS Certificates](spec-tls-certs.md)` — `spec-tailscale-dns.md` reference removed, new entry present.
- `docs/spec-doc-layout.md` line 15: `docs/spec-tls-certs.md` in the cross-cutting examples column — `spec-tailscale-dns.md` reference replaced.
- `docs/spec-tailscale-dns.md` — file does not exist (correctly deleted).
- `docs/spec-tls-certs.md` — exists, fully populated with normative YAML, exact resource names, ExternalDNS install snippet with `txtOwnerId`, and component responsibilities table consistent with the ADR.
- Residual references to `spec-tailscale-dns.md` exist in ADR-0020, ADR-0021, ADR-0026, and ADR-0027. These are in immutable, already-implemented ADR bodies. Per ADR-0018, broken cross-references in accepted ADR bodies are mechanical fixes — not blocking implementation gaps for ADR-0033.
- Pre-implementation state of `charts/`, `deploy-preview.yml`, and the `deploy-local-preview` skill (still using old domains and missing ExternalDNS plumbing) is expected: the ADR explicitly states code changes land in the subsequent `/feature-change` commits, not this plan-feature commit.
