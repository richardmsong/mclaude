## Audit: 2026-04-14T00:00:00Z
**Document:** docs/plan-k8s-integration.md

### Round 1 — Manual review

#### Factual inconsistencies found and fixed

1. **Nix PVC name: `nix-{projectId}` vs `nix-store` (line 571)**
   Reconcile step 8 says "Ensure nix PVC (nix-{projectId})" implying per-project.
   But line 726 says "shared Nix store (per-namespace)", line 839 says "one per namespace",
   and the PVC YAML (line 1502) names it `nix-store`. Fixed step 8 to say `nix-store`.

2. **`--bare` flag contradiction (line 62 vs 65-82)**
   Line 62 says session agent spawns Claude with `--bare` (skips hooks, LSP, memory).
   But the CLI invocation at lines 65-79 omits `--bare`, and line 82 explicitly says
   "Hooks, LSP, auto-memory, and plugin discovery all run normally." Fixed line 62
   to remove the `--bare` reference (the actual invocations are correct).

3. **Postgres `projects` table reference (line 685)**
   Project provisioning step 3 says "INSERT into Postgres projects table" but the
   Postgres schema only defines `users` and `nats_credentials`. The doc repeatedly
   states "Postgres: users table only." Fixed to note that project state lives in
   MCProject CR + NATS KV, not Postgres.

4. **Cost estimate resource mismatch (line 1283 vs 1427-1432)**
   Cost table says "350m CPU, 900Mi" per pod. Deployment spec says requests: 200m/512Mi.
   Fixed cost table to match deployment spec requests.

5. **Missing `_INBOX.>` in session-agent credentials (lines 224-229)**
   Per-user JWT includes `_INBOX.>` for request/reply. Session-agent credentials omit it.
   Line 233 says it's "required on all clients." Session agents handle request/reply
   subjects. Fixed to add `_INBOX.>` to session-agent credential permissions.

6. **ClusterRole missing CRD/MCProject permissions (lines 1540-1560)**
   The reconciler manages MCProject CRs (`mclaude.io/v1alpha1`) but the ClusterRole
   has no rules for `apiextensions.k8s.io` or `mclaude.io`. Fixed to add both.

7. **`pendingControl` singular typo (line 1202)**
   Says "pendingControl" but everywhere else uses "pendingControls". Fixed to plural.

8. **Stale "Framework TBD" (lines 951, 1608, 1735)**
   The SPA already uses React (mclaude-web/package.json). Removed "TBD" references
   and updated to reflect React is chosen.

9. **Broken unicode on line 976**
   "No stale cache ��— client" — corrupted character. Fixed to em dash.

10. **`set_max_thinking_tokens` undocumented in protocol (line 968)**
    Referenced in SPA section but not in Stream-JSON Input section. Added to input examples.

11. **`clear` and `compact_boundary` undocumented (line 296)**
    Core loop handles these event types but they're not documented in the protocol
    output section. Added note in protocol section.

12. **Critical Files: `provisioner.go` stale (line 1686)**
    Doc uses reconciler pattern (kubebuilder) but Critical Files lists `provisioner.go`.
    Updated to `reconciler.go` and added CRD reference.

#### Design decisions (NOT fixed — require user input)

**DECISION NEEDED: Laptop NATS subject structure**
Laptop subjects use `mclaude.{userId}.laptop.{hostname}.{projectId}.api.>` (line 394)
while K8s subjects use `mclaude.{userId}.{projectId}.api.>` (line 139). The browser
subscription for laptop events uses yet another pattern:
`mclaude.{userId}.laptop.{hostname}.events.>` (line 402) — missing `{projectId}` and
`{sessionId}` segments that K8s subjects have.

Options:
A. **Unify subjects** — laptop uses the same `mclaude.{userId}.{projectId}.*` structure as K8s, with the projectId encoding laptop identity (e.g. `laptop-{hostname}-{projectId}`). Simpler client code, single subscription pattern.
B. **Keep separate** — laptop has a different subject hierarchy. Document the difference explicitly and fix the browser subscription to include `{projectId}` and `{sessionId}` segments. More complexity but cleaner namespace separation.
C. **Hybrid** — K8s-style subjects but add a `location` segment: `mclaude.{userId}.{location}.{projectId}.*` where location is `k8s` or `laptop.{hostname}`. Uniform structure, explicit location.

### Round 1 — Status

- 12 factual inconsistencies fixed directly in the document
- 1 design decision remains (laptop NATS subject structure) — requires user input
- All fixes are self-contained edits; no structural reorganization needed
