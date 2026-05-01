## Audit: 2026-04-30T12:00:00Z

**Document:** docs/adr-0054-nats-jetstream-permission-tightening.md

### Round 1

**Gaps found: 4**

1. **Agent JWT TTL contradiction** — Decisions says 5 min, Error Handling says 24h.
2. **Agent credential refresh NKey problem** — No mechanism to obtain the agent's NKey public key for re-signing the refresh JWT. ADR assumed CP generates the keypair, but the correct design is client-generated keypairs.
3. **Missing pull consumer permission** — `$JS.API.CONSUMER.MSG.NEXT` not in agent Pub.Allow; current code uses pull consumers.
4. **No group deletion endpoint** — ADR mentions groups "can be deleted" but specifies no DELETE API.

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | Agent JWT TTL contradiction | Error handling table had stale "24h" example from before TTL was decided | Changed to "5 min" matching the Decisions table | factual |
| 2 | Agent NKey refresh | ADR assumed CP generates NKey pairs and returns seeds; correct design is client generates keypair, sends public key to CP | Rewrote all credential issuance/refresh wire formats: client sends `nkey_public` in request, CP returns only JWT. Updated host refresh, agent refresh, and agent issuance. Updated controller component changes. | factual |
| 3 | Pull consumer permission | ADR permission spec was designed for push consumers but didn't state the change from current pull consumer code | User decided: switch to ordered push consumers (better ordering guarantees for chat interface). Added explicit note in session-agent component changes. No MSG.NEXT permission needed. | decision |
| 4 | No group deletion endpoint | ADR defined group CRUD but omitted DELETE | User decided: defer group deletion to a dedicated groups ADR. Updated re-binding text to say old group is left in place. | decision |

### Round 2

**Gaps found: 3**

1. **`SessionAgentSubjectPermissions()` parameter list incomplete** — missing `uslug` parameter; every permission entry references `{uslug}`.
2. **Credential issuance response missing import JWT** — one-shot import JWT section describes "host controller receives both JWTs" but wire format only has one `jwt` field.
3. **`nkeys.go` JWT issuance functions under-scoped** — Component Changes only listed SubjectPermissions rewrites, not the fundamental change to IssueHostJWT/IssueSessionAgentJWT (accept external public key, return only JWT).

#### Fixes applied

| # | Gap | Cause | Resolution | Type |
|---|-----|-------|-----------|------|
| 1 | SessionAgentSubjectPermissions params | Component Changes text was abbreviated; "project slug + host slug" omitted the user slug that appears in every permission entry | Changed to `SessionAgentSubjectPermissions(uslug, hslug, pslug string)` with all three parameters listed | factual |
| 2 | Missing import JWT in response | Credential issuance wire format was written before one-shot import JWT section; the two were never reconciled | Added `import_jwt` field to issuance response with explanation of when it's present (project has pending import) | factual |
| 3 | nkeys.go Issue functions | Component Changes focused on SubjectPermissions helpers but didn't mention the Issue functions needed the same NKey externalization change | Added explicit `IssueHostJWT(publicKey, hslug)` and `IssueSessionAgentJWT(publicKey, uslug, hslug, pslug)` rewrite note — accept external public key, return only JWT | factual |

### Round 3

CLEAN — no blocking gaps found.

### Result

**CLEAN** after 3 rounds, 7 total gaps resolved (5 factual fixes, 2 design decisions).
