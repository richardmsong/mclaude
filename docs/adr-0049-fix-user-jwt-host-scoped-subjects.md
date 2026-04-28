# ADR: Fix User JWT Missing Host-Scoped Subject Permissions

**Status**: implemented
**Status history**:
- 2026-04-28: accepted
- 2026-04-28: implemented — all scope CLEAN

## Overview

The SPA uses ADR-0035 host-scoped subjects (`mclaude.users.{uslug}.hosts.{hslug}.projects.*.events.*`) but the user JWT `UserSubjectPermissions` only allows `mclaude.{userID}.>` (the pre-ADR-0035 UUID-prefixed namespace). NATS rejects every host-scoped subscription the SPA attempts — including session event subscriptions required to open a session detail screen.

## Motivation

User clicked "Default Project" and received:

```
NatsError: 'Permissions Violation for Subscription to
"mclaude.users.dev.hosts.local.projects.74fe6c90-39ce-41cd-885c-8f971e756c2a.events._api"'
```

The SPA constructs this subject via `subjEventsApi(uslug, hslug, pslug)` which produces `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.events._api`. The JWT's PubAllow/SubAllow only contains `mclaude.{UUID}.>`, which does not match `mclaude.users.*`. All ADR-0035 subjects are therefore blocked at the broker.

ADR-0047 updated `spec-control-plane.md` to document the current (UUID-format) JWT permissions, inadvertently overriding the ADR-0035 intended format. This ADR corrects both the code and the spec.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Add `mclaude.users.{uslug}.hosts.*.>` to PubAllow | `fmt.Sprintf("mclaude.users.%s.hosts.*.>", userSlug)` added to PubAllow in `UserSubjectPermissions`. Wildcard `*` at the host level so the SPA can access all of the user's hosts. | SPA publishes to host-scoped subjects (session create, session input, session control). JWT must permit `mclaude.users.{uslug}.hosts.*.>`. |
| Add `mclaude.users.{uslug}.hosts.*.>` to SubAllow | Same pattern added to SubAllow. | SPA subscribes to session events, lifecycle, and `_api` error subjects — all under `mclaude.users.{uslug}.hosts.{hslug}.projects.*.…`. |
| Keep `mclaude.{userID}.>` in both lists | Retain the existing UUID-prefixed wildcard. | Backward compat while project/session KV keys and any remaining old-format subjects haven't been migrated. |
| Spec correction | Revert `spec-control-plane.md` auth section to list both the old and new subject prefixes, replacing the ADR-0047 regression. | ADR-0047 inadvertently replaced the ADR-0035 target format with the current UUID format. The spec should document both the interim UUID prefix (present until full migration) and the ADR-0035 slug-based prefix. |

## Impact

**Specs updated in this commit:**
- `docs/mclaude-control-plane/spec-control-plane.md` — Authentication JWT permissions: add `mclaude.users.{uslug}.hosts.*.>` to both PubAllow and SubAllow alongside the existing `mclaude.{userID}.>`.

**Components implementing the change:**
- `mclaude-control-plane`: `nkeys.go` (`UserSubjectPermissions` adds host-scoped prefix to PubAllow + SubAllow).

## Scope

**In v1:** `mclaude.users.{uslug}.hosts.*.>` added to user JWT PubAllow and SubAllow.

**Explicitly deferred:** Remove the legacy `mclaude.{userID}.>` prefix (requires full project/session KV + subject migration, separate ADR).
