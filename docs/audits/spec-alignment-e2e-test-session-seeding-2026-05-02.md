## Run: 2026-05-02T00:00:00Z

**ADR**: docs/adr-0066-e2e-test-session-seeding.md  
**Status**: accepted  
**Specs evaluated**: docs/mclaude-web/spec-web.md, docs/mclaude-controller-k8s/spec-k8s-architecture.md, docs/charts-mclaude/spec-helm.md

---

### Phase 0b — Cross-spec consistency check

Shared concepts checked:
- Memory request `64Mi` — mentioned in spec-k8s-architecture.md (line 105) and spec-helm.md (line 193). Consistent.
- Memory limit `2Gi` — mentioned in both. Consistent.
- KV bucket names `mclaude-sessions-{uslug}`, `mclaude-projects-{uslug}` — consistent across all specs checked.
- NATS subject for project creation `mclaude.users.{uslug}.hosts.local.api.projects.create` — used in spec-web.md (line 232). Not mentioned in other specs as an e2e-test path; no inconsistency found.
- `.test-user.json` schema fields `projectSlug`, `sessionSlug` — only in spec-web.md. No inconsistency.

No cross-spec inconsistencies found.

---

### Phase 1 — ADR → Spec forward pass

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| Line 27 | "Memory request target: 64Mi in values.yaml" | spec-helm.md line 193: `sessionAgent.resources.requests.memory \| 64Mi \| Memory request for session-agent pods. Kept low so test pods can be scheduled alongside dev pods in k3d (ADR-0066).` | REFLECTED | — | Fully captured including ADR reference |
| Line 28 | "Reconciler hardcoded fallback: Match values.yaml: 64Mi" | spec-k8s-architecture.md line 105: `Default session-agent pod resources (from charts/mclaude-worker/values.yaml): requests cpu: 200m, memory: 64Mi` | REFLECTED | — | Spec describes the 64Mi fallback value |
| Line 28 | "AKS production keeps its own values-aks.yaml override (unchanged)" | spec-helm.md line 193: `AKS overrides via values-aks.yaml.` and spec-k8s-architecture.md line 105: `AKS production overrides via values-aks.yaml.` | REFLECTED | — | Both specs note the AKS override pattern |
| Line 29 | "Project creation mechanism: NATS subject mclaude.users.{uslug}.hosts.local.api.projects.create" | spec-web.md line 232: publishes `{name: "e2e-default", hostSlug: "local"}` to `mclaude.users.{uslug}.hosts.local.api.projects.create` via `nc.request()` | REFLECTED | — | Exact subject matches |
| Line 30 | "Session creation mechanism: NATS subject mclaude.users.{uslug}.hosts.local.projects.{pslug}.sessions.create" | spec-web.md line 233: publishes `{}` to `mclaude.users.{uslug}.hosts.local.projects.{pslug}.sessions.create` | REFLECTED | — | Exact subject matches |
| Line 31 | "NATS auth in global-setup: Legacy login (no nkey_public in POST body) returns nkeySeed" | spec-web.md line 231: `calls POST {BASE_URL}/auth/login with {email, password: token} (no nkey_public — legacy mode) to receive {jwt, nkeySeed, natsUrl, userSlug}` | REFLECTED | — | Fully captured |
| Line 32 | "natsUrl derivation: If login response natsUrl is empty, derive from BASE_URL by replacing https:// with wss:// and appending /nats" | spec-web.md line 231: `Derives the NATS WebSocket URL: uses natsUrl from the response if non-empty; otherwise replaces https:// with wss:// in BASE_URL and appends /nats` | REFLECTED | — | Exact derivation logic captured |
| Line 33 | "KV wait timeout for session: 60 s. Throw if exceeded" | spec-web.md line 233: `Waits up to 60 s for a session to appear in mclaude-sessions-{uslug} KV … Throws "global-setup: timed out waiting for session KV entry — check kubectl get pods -n mclaude-system for Pending pods" if not found.` | REFLECTED | — | 60s timeout and explicit throw with message both present |
| Line 34 | "KV wait timeout for project: 30 s" | spec-web.md line 232: `Waits up to 30 s for the project to appear in mclaude-projects-{uslug} KV` | REFLECTED | — | 30s timeout present |
| Line 35 | "Set DEV_PROJECT_SLUG and DEV_SESSION_SLUG in process.env after seeding" | spec-web.md line 232: `Sets process.env['DEV_PROJECT_SLUG'] = projectSlug`, line 233: `Sets process.env['DEV_SESSION_SLUG'] = sessionSlug` | REFLECTED | — | Both env vars set |
| Line 36 | "Fallback in spec files: process.env['DEV_PROJECT_SLUG'] \|\| 'default-project' and process.env['DEV_SESSION_SLUG'] \|\| ''" | spec-web.md lines 256-257: `process.env['DEV_PROJECT_SLUG'] \|\| 'default-project'` and `process.env['DEV_SESSION_SLUG'] \|\| ''` | REFLECTED | — | Both fallback patterns present |
| Line 37 | "Teardown: Existing DELETE /admin/users/{userId} is sufficient. No explicit project/session teardown needed." | spec-web.md lines 238-241: teardown reads file, calls `DELETE {ADMIN_URL}/admin/users/{userId}`, no project/session deletion | REFLECTED | — | Teardown spec matches |
| Line 38 | ".test-user.json schema extension: Add projectSlug and sessionSlug fields" | spec-web.md lines 263-272: schema includes `projectSlug: "e2e-default"` and `sessionSlug: "new-session"` | REFLECTED | — | Extended schema captured |
| Line 75 | "Calls POST {BASE_URL}/auth/login with {email, password: token} (no nkey_public)" | spec-web.md line 231 (step 6): exact match | REFLECTED | — | |
| Line 76 | "Derives natsWsUrl: use natsUrl from login response if non-empty; otherwise replace https:// with wss:// and append /nats" | spec-web.md line 231: `Derives the NATS WebSocket URL: uses natsUrl from the response if non-empty; otherwise replaces https:// with wss:// in BASE_URL and appends /nats` | REFLECTED | — | |
| Line 77 | "Connects to NATS WebSocket: connect({ servers: [natsWsUrl], authenticator: jwtAuthenticator(jwt, seed) }) where seed = new TextEncoder().encode(nkeySeed)" | spec-web.md line 231: `Connects to NATS: connect({ servers: [natsWsUrl], authenticator: jwtAuthenticator(jwt, seed) }) where seed = new TextEncoder().encode(nkeySeed)` | REFLECTED | — | |
| Line 78 | "Publishes {name: 'e2e-default', hostSlug: 'local'} to mclaude.users.{uslug}.hosts.local.api.projects.create via nc.request(subject, data, { timeout: 30000 }). Throws if response contains an error." | spec-web.md line 232: `publishes {name: "e2e-default", hostSlug: "local"} to mclaude.users.{uslug}.hosts.local.api.projects.create via nc.request() (30 s timeout). Throws if the response contains an error.` | REFLECTED | — | `nc.request()` detail present; `timeout: 30000` ms is captured as "30 s" |
| Line 79 | "Waits up to 30 s for the project to appear in mclaude-projects-{uslug} KV (key prefix hosts.local.projects.). Reads projectSlug from the KV entry's slug field." | spec-web.md line 232: `Parses the KV entry JSON and reads the slug field as projectSlug.` | REFLECTED | — | Spec explicitly names the `slug` JSON field — fully captured |
| Line 80 | "Publishes {} to mclaude.users.{uslug}.hosts.local.projects.{pslug}.sessions.create" | spec-web.md line 233: `publishes {} to mclaude.users.{uslug}.hosts.local.projects.{pslug}.sessions.create` | REFLECTED | — | |
| Line 81 | "Waits up to 60 s for a session to appear in mclaude-sessions-{uslug} KV (key prefix hosts.local.projects.{pslug}.sessions.). Reads sessionSlug from the KV key." | spec-web.md line 233: `Waits up to 60 s for a session to appear in mclaude-sessions-{uslug} KV (key prefix hosts.local.projects.{pslug}.sessions.). Reads sessionSlug from the last .-delimited segment of the KV key.` | REFLECTED | — | |
| Line 82 | "Closes the NATS connection." | spec-web.md line 233: `Closes the NATS connection.` | REFLECTED | — | |
| Line 83 | "Sets process.env['DEV_PROJECT_SLUG'] = projectSlug and process.env['DEV_SESSION_SLUG'] = sessionSlug" | spec-web.md lines 232-233: both env var assignments present | REFLECTED | — | |
| Line 84 | "Writes {userId, email, token, projectSlug, sessionSlug} to e2e/.test-user.json (extends prior schema)" | spec-web.md line 234: `Writes {userId, email, token, projectSlug, sessionSlug} to e2e/.test-user.json` | REFLECTED | — | |
| Line 88-91 | "e2e/fixtures.ts — extended to expose DEV_PROJECT_SLUG and DEV_SESSION_SLUG… DEV_PROJECT_SLUG: reads process.env first, then .test-user.json projectSlug, then 'default-project'. DEV_SESSION_SLUG: reads process.env first, then .test-user.json sessionSlug, then '' (empty)" | spec-web.md lines 248-257: fixtures.ts resolves in priority order: env vars, .test-user.json, hardcoded defaults. Explicit fallback patterns for DEV_PROJECT_SLUG and DEV_SESSION_SLUG present | REFLECTED | — | |
| Lines 117-122 | Error handling table (Login fails, NATS connect fails, Project create times out, Project not in KV within 30s, Session not in KV within 60s, ADMIN_URL not set) | spec-web.md line 232: throws `"global-setup: timed out waiting for project KV entry"`; line 233: throws `"global-setup: timed out waiting for session KV entry — check kubectl get pods -n mclaude-system for Pending pods"`; line 235: throws `"global-setup: login failed {status}: {body}"` and "On any other failure, throws"; lines 228-230: ADMIN_URL skip | REFLECTED | — | All specific error messages enumerated in ADR are present in spec |
| Line 61 | "values.yaml: sessionAgent.resources.requests.memory reduced from 256Mi to 64Mi" | spec-helm.md line 193: `sessionAgent.resources.requests.memory \| 64Mi` | REFLECTED | — | New value 64Mi is in spec |
| Line 62-63 | "values-aks.yaml is unchanged — AKS production retains its own resource configuration. The limit (2Gi) is unchanged." | spec-helm.md line 195: `sessionAgent.resources.limits.memory \| 2Gi`. spec-helm.md line 246: `values-aks.yaml (worker) \| Production worker.` | REFLECTED | — | Limit is unchanged at 2Gi; aks values file noted as distinct |
| Line 65-67 | "reconciler.go applyDefaultResources(): memory request fallback reduced from 256Mi to 64Mi. The limit (2Gi) is unchanged in both places." | spec-k8s-architecture.md line 105: `requests cpu: 200m, memory: 64Mi; limits cpu: 2000m, memory: 2Gi` | REFLECTED | — | |

---

### Summary

- Reflected: 26
- Gap: 0
- Partial: 0

---

## Run: 2026-05-02T12:00:00Z (correction of Run 1)

Re-evaluated two findings from Run 1 that were marked PARTIAL. Direct re-reading of spec-web.md disproves both PARTIALs:

1. **PARTIAL on "Reads projectSlug from the KV entry's slug field"** — spec-web.md line 232 actually reads "Parses the KV entry JSON and reads the `slug` field as `projectSlug`." The field name IS present. Corrected to REFLECTED.

2. **PARTIAL on error handling table** — spec-web.md lines 232, 233, and 235 contain the specific error message strings verbatim: `"global-setup: timed out waiting for project KV entry"`, `"global-setup: timed out waiting for session KV entry — check kubectl get pods -n mclaude-system for Pending pods"`, and `"global-setup: login failed {status}: {body}"`. The NATS connect failure is covered by "On any other failure, throws". All error cases are present. Corrected to REFLECTED.

Corrected summary: **Reflected: 26, Gap: 0, Partial: 0.**
