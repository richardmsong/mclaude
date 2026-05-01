# NATS Data Taxonomy

Canonical naming conventions for all NATS subjects, JetStream resources, and KV keys. Every name in the system derives from a single hierarchical scheme.

Depends on: `adr-0054-nats-jetstream-permission-tightening.md` (permission model), `spec-nats-activity.md` (runtime walkthrough).

---

## Design Principle

One naming convention. KV keys use the same dotted hierarchy as NATS subjects. The user slug is encoded in the resource name (bucket or stream), not repeated in the key. Reading a KV key or a stream subject should convey the same structural information without translation.

**Derivation rule:** Given a NATS application subject:

```
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.events
├─────── user prefix ──────┤├──────────── resource path ──────────────┤├ suffix ┤
```

- **Stream subject**: Full subject. Captured by `MCLAUDE_SESSIONS_{uslug}`.
- **KV key**: Resource path only. The user prefix is in the bucket name (`KV_mclaude-sessions-{uslug}`), and the suffix (`.events`, `.input`, etc.) is not part of the state key.
- **KV NATS subject**: `$KV.<bucket>.<key>` = `$KV.mclaude-sessions-{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}`

---

## Resource Hierarchy

```
mclaude
├── users.{uslug}                          ← user boundary (hard, NATS-enforced)
│   └── hosts.{hslug}                      ← host within user scope
│       └── projects.{pslug}               ← project within host
│           ├── sessions.{sslug}           ← session within project
│           │   ├── .events                ← stream suffix: assistant output
│           │   ├── .input                 ← stream suffix: user input
│           │   ├── .delete                ← stream suffix: deletion command
│           │   ├── .config                ← stream suffix: config update (model, permissions)
│           │   ├── .control.interrupt     ← stream suffix: interrupt signal
│           │   ├── .control.restart       ← stream suffix: restart signal
│           │   ├── .lifecycle.started     ← stream suffix: lifecycle event
│           │   ├── .lifecycle.stopped     ← stream suffix: lifecycle event
│           │   └── .lifecycle.error       ← stream suffix: lifecycle event
│           ├── sessions.create            ← stream: session creation command
│           └── import.complete            ← core subject: import signal
│       └── manage.                        ← host lifecycle management (owner only)
│           ├── update                     ← rename, change type
│           ├── grant                      ← grant host access to another user
│           ├── revoke-access              ← revoke host access from a user
│           ├── rekey                      ← rotate NKey public key (SSH known_hosts model)
│           ├── deregister                 ← drain sessions + cleanup
│           └── revoke                     ← emergency credential revocation
│   ├── quota                              ← core subject: Anthropic API quota status (core NATS, no JetStream)
│   └── hosts._.register                   ← register a new host ("_" sentinel — can't collide with slugs, which are [a-z0-9-]+)
│
├── hosts.{hslug}                          ← host boundary (host controller scope)
│   ├── users.{uslug}.projects.{pslug}.create   ← CP publishes after HTTP validation, host controller receives
│   ├── users.{uslug}.projects.{pslug}.delete   ← CP publishes, host controller receives
│   └── api.agents.register                ← core subject: host registers agent public key
```

---

## JetStream Resources

### KV Buckets

Each per-user bucket uses the resource path as the key (no user prefix in the key — it's in the bucket name).

| Bucket | Scope | Key format | Example key | Example NATS subject |
|--------|-------|------------|-------------|---------------------|
| `KV_mclaude-sessions-{uslug}` | Per-user | `hosts.{hslug}.projects.{pslug}.sessions.{sslug}` | `hosts.laptop-a.projects.myapp.sessions.sess-001` | `$KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.sess-001` |
| `KV_mclaude-projects-{uslug}` | Per-user | `hosts.{hslug}.projects.{pslug}` | `hosts.laptop-a.projects.myapp` | `$KV.mclaude-projects-alice.hosts.laptop-a.projects.myapp` |

| `KV_mclaude-hosts` | Shared | `{hslug}` | `laptop-a` | `$KV.mclaude-hosts.laptop-a` |

**Why hosts KV keys are flat:** The bucket name already scopes to hosts. Adding `hosts.` would be redundant (`KV_mclaude-hosts.hosts.laptop-a`). There is no deeper hierarchy — each key is one host.

### Streams

| Stream | Scope | Subject filter | Captures |
|--------|-------|----------------|----------|
| `MCLAUDE_SESSIONS_{uslug}` | Per-user | `mclaude.users.{uslug}.hosts.*.projects.*.sessions.>` | All session activity: events, input, commands, lifecycle |

**Consumer patterns on `MCLAUDE_SESSIONS_{uslug}`:**

All consumers are ordered push consumers (ephemeral, client-generated NUID name, auto-reset on sequence gap).

| Consumer | Created by | Filter | Deliver policy | Lifetime |
|----------|-----------|--------|---------------|----------|
| Dashboard notifications | SPA | `*.lifecycle.>` (all sessions) | `DeliverNew` | SPA connection lifetime |
| Chat view | SPA | One session: `sessions.{sslug}.>` | `DeliverAll` | While user has session open |
| Agent commands | Session-agent | This project: `hosts.{hslug}.projects.{pslug}.sessions.>` | `DeliverNew` | Agent process lifetime |

The dashboard consumer is lightweight (lifecycle events only, no replay). The chat view consumer replays the full conversation on open and then delivers live updates. The agent consumer processes incoming commands as they arrive (input, delete, control signals) with no history replay.

### Binary Data (S3, not NATS)

No NATS Object Store. All binary data (imports, attachments) flows through S3 with pre-signed URLs (ADR-0053). NATS messages carry lightweight `AttachmentRef` references only. This eliminates Object Store stream proliferation and one-shot JWT complexity.

| Data type | S3 key pattern | Upload | Download |
|-----------|---------------|--------|----------|
| Import archives | `{uslug}/{hslug}/{pslug}/imports/{id}.tar.gz` | CLI → S3 (pre-signed) | Agent → S3 (pre-signed) |
| Attachments | `{uslug}/{hslug}/{pslug}/attachments/{id}` | SPA/Agent → S3 (pre-signed) | Agent/SPA → S3 (pre-signed) |

CP signs all URLs after validating project ownership. Pre-signed URLs are time-limited (5 min). No NATS subjects, streams, or permissions involved.

---

## Subject Namespaces

Four top-level namespaces. Everything in the system falls into one of these.

### 1. Application subjects (`mclaude.`)

Human-meaningful, hierarchical. Used for commands, events, and inter-component messaging.

```
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.events
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.sessions.create
mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.import.complete
mclaude.users.{uslug}.hosts.{hslug}.manage.update
mclaude.users.{uslug}.hosts.{hslug}.manage.grant
mclaude.users.{uslug}.hosts.{hslug}.manage.revoke-access
mclaude.users.{uslug}.hosts.{hslug}.manage.rekey
mclaude.users.{uslug}.hosts.{hslug}.manage.deregister
mclaude.users.{uslug}.hosts.{hslug}.manage.revoke
mclaude.users.{uslug}.quota
mclaude.users.{uslug}.hosts._.register
mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.create
mclaude.hosts.{hslug}.users.{uslug}.projects.{pslug}.delete
mclaude.hosts.{hslug}.api.agents.register
```

Two subtrees:
- `mclaude.users.{uslug}.*` — user-scoped. Published by SPA/CLI/agents, captured by per-user streams.
- `mclaude.hosts.{hslug}.*` — host-scoped. Published by CP and host controllers. Never captured by streams (ephemeral request/reply).

### 2. KV subjects (`$KV.`)

State storage. Keys mirror the application subject hierarchy (minus the user prefix, which is in the bucket name).

```
$KV.mclaude-sessions-{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}
$KV.mclaude-projects-{uslug}.hosts.{hslug}.projects.{pslug}

$KV.mclaude-hosts.{hslug}
```

### 3. JetStream API subjects (`$JS.API.`)

NATS infrastructure. Used for KV operations (direct-get, consumer-create, stream-info), not application logic. Permission entries reference these, but application code uses the NATS client SDK which abstracts them.

```
$JS.API.DIRECT.GET.KV_mclaude-sessions-{uslug}.$KV.mclaude-sessions-{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}
$JS.API.CONSUMER.CREATE.KV_mclaude-sessions-{uslug}.{consumer-name}.$KV.mclaude-sessions-{uslug}.hosts.{hslug}.projects.{pslug}.sessions.>
$JS.API.STREAM.INFO.KV_mclaude-sessions-{uslug}
$JS.API.CONSUMER.INFO.KV_mclaude-sessions-{uslug}.{consumer-name}
```

**Consumer names**: The `{consumer-name}` token is a client-generated NUID (NATS Unique Identifier) — a 22-character base62 random string (e.g., `dkzJpDSRQqqMSGe8mBGKBF`). Generated client-side with no server coordination. For ordered push consumers, the client generates a fresh NUID on every creation and on every reset (when a sequence gap is detected). These consumers are ephemeral — no durable name, no server-side persistence after disconnect. JWT permission entries use `*` in the consumer name position to allow any client-generated name.

### 4. System subjects (`$SYS.`)

NATS server internals. Only CP subscribes.

```
$SYS.ACCOUNT.{account_id}.CONNECT
$SYS.ACCOUNT.{account_id}.DISCONNECT
```

---

## Permission Scoping by Identity

Each identity type sees a specific slice of the taxonomy, enforced by JWT Pub/Sub.Allow lists.

| Namespace | User (SPA/CLI) | Agent | Host controller | CP |
|-----------|---------------|-------|----------------|-----|
| `mclaude.users.{uslug}.*` | Own uslug, all hosts | Own uslug, one host, one project | None | Subscribe all (wildcard) |
| `mclaude.hosts.{hslug}.*` | Publish to own hosts | None | Own hslug only | Publish to all hosts |
| `$KV.mclaude-sessions-{uslug}.*` | Read all (own uslug) | Read/write one project | None | Write via agents |
| `$KV.mclaude-projects-{uslug}.*` | Read all (own uslug) | Read/write one project | None | Write on provisioning |

| `$KV.mclaude-hosts.*` | Per-host entries (JWT-scoped) | Own host only | None | Write ($SYS events) |
| Binary data (S3) | Upload via pre-signed URL | Download via pre-signed URL | None | Signs URLs, validates ownership |
| `$JS.API.*` | Own buckets/streams | Own buckets/streams, one project filter | None | All |
| `$SYS.*` | None | None | None | Subscribe |

---

## Slug Constraints

All slugs used in NATS subjects must be valid NATS subject tokens.

| Slug | Source | Constraints |
|------|--------|-------------|
| `{uslug}` | User slug, assigned at registration | `[a-z0-9-]+`, no dots, max 64 chars |
| `{hslug}` | Host slug, assigned at registration | `[a-z0-9-]+`, no dots, max 64 chars |
| `{pslug}` | Project slug, derived from project name | `[a-z0-9-]+`, no dots, max 64 chars |
| `{sslug}` | Session slug, generated at creation | `[a-z0-9-]+`, no dots, max 64 chars |
| `{jobid}` | Job ID, generated at creation | `[a-z0-9-]+`, no dots, max 64 chars |

**Why no dots:** NATS uses `.` as the subject token delimiter. A slug containing a dot would create an extra hierarchy level, breaking wildcard matching and permission scoping. Slugs must be atomic subject tokens.

---

## Cross-Reference: KV Key ↔ Stream Subject ↔ Application Subject

For a session `sess-001` in project `myapp` on host `laptop-a` for user `alice`:

| Aspect | Value |
|--------|-------|
| **KV key** (sessions bucket) | `hosts.laptop-a.projects.myapp.sessions.sess-001` |
| **KV NATS subject** | `$KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.sess-001` |
| **Stream subject** (events) | `mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sess-001.events` |
| **Stream subject** (input) | `mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sess-001.input` |
| **Stream subject** (lifecycle) | `mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sess-001.lifecycle.started` |
| **KV key** (projects bucket) | `hosts.laptop-a.projects.myapp` |
| **Host KV key** | `laptop-a` |

The pattern: strip the user prefix from the stream subject, drop the event suffix, and you have the KV key.
