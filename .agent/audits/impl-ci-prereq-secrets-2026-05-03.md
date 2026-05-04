## Run: 2026-05-03T00:00:00Z

Component: `.github/workflows/deploy-main.yml`
ADR: `docs/adr-0082-fix-ci-prereq-secrets-bootstrap.md` (Status: accepted)

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| adr-0082:36 | Add a new step "Ensure prerequisite secrets" immediately before "Helm deploy control-plane" | deploy-main.yml:182-191 | IMPLEMENTED | — | Step named exactly "Ensure prerequisite secrets" at lines 182–191; "Helm deploy control-plane" follows at line 193 |
| adr-0082:39-46 | kubectl create secret generic mclaude-postgres --from-literal=postgres-password="dev-postgres-password-k3d" --namespace mclaude-system --dry-run=client -o yaml \| kubectl apply -f - | deploy-main.yml:184-187 | IMPLEMENTED | — | Command matches spec exactly including key name, value, namespace, and idempotent apply pattern |
| adr-0082:48-51 | kubectl create secret generic mclaude-control-plane --from-literal=admin-token="dev-admin-token" --namespace mclaude-system --dry-run=client -o yaml \| kubectl apply -f - | deploy-main.yml:188-191 | IMPLEMENTED | — | Command matches spec exactly including key name, value, namespace, and idempotent apply pattern |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| deploy-main.yml:1-21 | INFRA | Workflow trigger, name, and tag-computation job — necessary CI boilerplate |
| deploy-main.yml:22-173 | INFRA | Build jobs (control-plane, spa, session-agent, controller-k8s) — necessary CI build steps, not in scope of this ADR |
| deploy-main.yml:175-181 | INFRA | Deploy job declaration and checkout step — necessary plumbing |
| deploy-main.yml:193-237 | INFRA | "Helm deploy control-plane" and "Helm deploy worker" steps — pre-existing deployment steps, not changed by this ADR |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| adr-0082:36 | "Ensure prerequisite secrets" step before Helm deploy | — | — | UNTESTED |
| adr-0082:39-46 | mclaude-postgres secret creation | — | — | UNTESTED |
| adr-0082:48-51 | mclaude-control-plane secret creation | — | — | UNTESTED |

Note: The ADR explicitly states "No dedicated integration test — the observable outcome is that CI deploys succeed after namespace recreation." UNTESTED is expected and spec-acknowledged.

### Phase 4 — Bug Triage

No open bugs in `.agent/bugs/` reference this component.

### Summary

- Implemented: 3
- Gap: 0
- Partial: 0
- Infra: 4
- Unspec'd: 0
- Dead: 0
- Tested: 0
- Unit only: 0
- E2E only: 0
- Untested: 3 (all spec-acknowledged — ADR explicitly waives test requirement)
- Bugs fixed: 0
- Bugs open: 0
