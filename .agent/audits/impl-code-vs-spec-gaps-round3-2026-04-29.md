# Audit: Code-vs-Spec Gaps — Round 3

**Date:** 2026-04-29
**Scope:** All 6 spec files cross-referenced against all listed code files
**Prior rounds:** Round 1 (26 gaps, all fixed), Round 2 (19 gaps, all fixed)

## Findings: 6 NEW gaps

---

### GAP 1: LoginResponse shape does not match spec-state-schema

**Code:** `mclaude-control-plane/auth.go` — `LoginResponse` struct (lines ~15-30)
**Spec:** `docs/spec-state-schema.md` § Login Response Shape; `docs/mclaude-control-plane/spec-control-plane.md` § HTTP Endpoints
**Detail:** The spec documents the login response as `{user: {id, slug}, jwt, nkeySeed, hubUrl, hosts[], projects[]}`. The code returns a flat struct `{natsUrl, jwt, nkeySeed, userId, userSlug, expiresAt}`. Missing from code:
- Nested `user` object (code uses flat `userId`/`userSlug` instead)
- `hubUrl` field (code has `natsUrl` which is different — it's the WS URL, not the hub URL)
- `hosts[]` array (the full host inventory)
- `projects[]` array (the user's project list with hostSlug/hostType/jsDomain/directNatsUrl)
- `expiresAt` is in the code but not in the spec schema

The spec should either be updated to match the current flat response, or the code should be updated to return the full shape. The SPA currently works without `hosts[]`/`projects[]` by deriving data from KV watches, so the spec is aspirational.

---

### GAP 2: `IssueHostJWT` called with UUID instead of user slug in host registration endpoints

**Code:** `mclaude-control-plane/hosts.go` — `handleHostRegister()` and `handleCreateHost()`
**Spec:** `docs/mclaude-control-plane/spec-control-plane.md` § Known bugs (only documents this bug for `adminGrantCluster`)
**Detail:** The spec flags `IssueHostJWT(user.ID, ...)` as a known bug only for `POST /admin/clusters/{cslug}/grants`. The identical bug exists in two other handlers:
- `handleHostRegister()`: `IssueHostJWT(entry.UserID, slug, s.accountKP)` — `entry.UserID` is a UUID
- `handleCreateHost()`: `IssueHostJWT(userID, req.Slug, s.accountKP)` — `userID` from auth context is a UUID

Both produce host JWTs scoped to `mclaude.users.{UUID}.hosts.{hslug}.>` instead of the correct `mclaude.users.{user-slug}.hosts.{hslug}.>`. The spec should flag these two additional occurrences alongside the existing known bug for the grant endpoint.

---

### GAP 3: `OPERATOR_KEYS_PATH` fallback not implemented

**Code:** `mclaude-control-plane/main.go` — `loadOrGenerateAccountKey()`
**Spec:** `docs/mclaude-control-plane/spec-control-plane.md` § Environment Variables, § Internal Behavior / Startup Sequence
**Detail:** The spec says: "Loads the account signing key from `NATS_ACCOUNT_SEED` env var (preferred) or from `OPERATOR_KEYS_PATH` file mount (fallback). If not set, falls back to `OPERATOR_KEYS_PATH`." The code implements only: `NATS_ACCOUNT_SEED` → ephemeral key. There is no code path that reads from `OPERATOR_KEYS_PATH` (`/etc/mclaude/operator-keys`). The spec documents the file-mount fallback as a real feature but the code skips directly to generating an ephemeral key. Either the spec should document this as "not yet implemented" or the code should implement the fallback.

---

### GAP 4: MCProject CRD does not mark `userSlug`/`projectSlug` as required

**Code:** `charts/mclaude-worker/templates/mcproject-crd.yaml` — `spec.required` list
**Spec:** `docs/spec-state-schema.md` § CRD: MCProject
**Detail:** The spec documents MCProject spec fields as:
```yaml
userSlug: string   # required — typed slug (ADR-0050)
projectSlug: string # required — typed slug (ADR-0050)
```
But the CRD YAML's `required` list only includes `userId` and `projectId`. The `userSlug` and `projectSlug` fields are defined as properties but not marked required. The Kubernetes API will accept MCProject CRs without these fields, which could cause runtime errors in the reconciler when building NATS subjects.

---

### GAP 5: `mclaude.users.{uslug}.api.projects.updated` subject never published

**Code:** All control-plane files (`projects.go`, `main.go`, `hosts.go`, `admin.go`)
**Spec:** `docs/spec-state-schema.md` § NATS Subjects (Core Pub/Sub) — User-level subjects
**Detail:** The spec lists `mclaude.users.{uslug}.api.projects.updated` with Publisher: `control-plane`, Subscriber: `SPA`, and Description: "Broadcast notification that project state has changed (project created, updated, or deleted). SPA invalidates its project list cache on receipt." The SPA has the helper `subjProjectsUpdated()` in `src/lib/subj.ts`. But no control-plane code publishes to this subject. The SPA currently discovers project changes through KV watches instead. The spec should either flag this as "not yet implemented" or the code should publish this subject on project mutations.

---

### GAP 6: `oauth_connections` table missing from spec-state-schema Postgres section

**Code:** `mclaude-control-plane/db.go` — `oauth_connections` CREATE TABLE, full CRUD methods
**Spec:** `docs/spec-state-schema.md` § Postgres (control-plane)
**Detail:** The Postgres section of spec-state-schema.md documents three tables: `users`, `projects`, `hosts`. The `oauth_connections` table is a core persistence table with 12 columns, unique constraints, and full CRUD operations (CreateOAuthConnection, GetOAuthConnectionByID, GetOAuthConnectionsByUser, DeleteOAuthConnection, GetExpiringGitLabConnections, UpdateTokenExpiry). It is referenced throughout spec-control-plane.md (OAuth integration, GitLab refresh, PAT connections, credential rotation) but its schema is not documented in the canonical state schema. The spec should include the `oauth_connections` table definition alongside the other three tables.

---

## Borderline items considered but excluded

1. **mclaude-cp values.yaml `adminPort: 9090` vs code default `9091`**: The Helm values also has a separate `metricsPort: 9091`. The code serves admin+metrics on a single port. This is a Helm-vs-code config mismatch but doesn't affect correctness since the Helm templates define how env vars map. Excluded because it's a deployment-config detail, not a behavioral gap.

2. **`MCLAUDE_LIFECYCLE` stream not created by session-agent**: Already flagged in spec-state-schema.md as "known gap".

3. **`readyz` returning 200 unconditionally**: Already flagged in spec-control-plane.md as "known bug".

4. **`handleRefresh` returns `natsURL` instead of `natsWsURL`**: Already flagged as "known bug".

5. **`adminGrantCluster` calls `GetUserByEmail(req.UserSlug)` instead of `GetUserBySlug()`**: Already flagged as "known bug".

6. **`adminRegisterCluster` response missing `accountJwt`/`operatorJwt` values**: Already flagged as "known bug".

7. **Controller env vars `JS_DOMAIN`, `LOG_LEVEL`, `LEADER_ELECTION` injected but not read**: Already flagged in spec-controller.md.

8. **Dead code in subj.ts (`subjClusterProvision`, `subjClusterStatus`, `kvKeyUserClusters`)**: Already flagged in spec-web.md.

## Confidence: HIGH

All 6 spec files and all listed code files were fully read and cross-referenced. Subject patterns, KV key formats, struct fields, env var handling, NATS permission sets, CRD fields, and Helm values were systematically compared. The 6 gaps above are new findings not flagged in any prior audit or as known bugs/gaps in the specs. 8 borderline items were considered and excluded because they are already documented.
