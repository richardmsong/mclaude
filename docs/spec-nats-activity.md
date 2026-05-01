# NATS Activity Specification

End-to-end walkthrough of every NATS interaction in the system. Uses **alice** as the persona. Every subject, payload, publisher, subscriber, and JetStream resource is enumerated.

Depends on: `adr-0054-nats-jetstream-permission-tightening.md` (permission model), `adr-0058-byoh-architecture-redesign.md` (BYOH agent model).

---

## Identities

| Identity | NATS credential | Scope |
|----------|----------------|-------|
| **Alice's SPA** | User JWT (`alice`) | Per-user buckets + per-host KV reads + session stream |
| **Alice's CLI** | User JWT (`alice`) | Same as SPA |
| **laptop-a controller** | Host JWT (`laptop-a`) | `mclaude.hosts.laptop-a.>` only. Zero JetStream. |
| **Agent: alice/myapp on laptop-a** | Agent JWT (`alice`, `laptop-a`, `myapp`) | Per-project KV keys + per-project session subjects |
| **Control plane (CP)** | Account signing key | Full access — trust root |

## JetStream Resources

| Resource | Type | Owner | Key format |
|----------|------|-------|------------|
| `KV_mclaude-sessions-alice` | KV bucket | alice | `hosts.{hslug}.projects.{pslug}.sessions.{sslug}` |
| `KV_mclaude-projects-alice` | KV bucket | alice | `hosts.{hslug}.projects.{pslug}` |
| `KV_mclaude-hosts` | KV bucket | shared | `{hslug}` |
| `MCLAUDE_SESSIONS_alice` | Stream | alice | subjects: `mclaude.users.alice.hosts.*.projects.*.sessions.>` |
| *(Binary data in S3, not NATS — see ADR-0053)* | | | |

---

## 1. Alice Logs In

**HTTP, not NATS.** Alice authenticates via the web UI.

```
SPA → CP:  POST /api/auth/login  {email, password, nkey_public: "UABC..."}
CP  → SPA: {user_slug: "alice", nats_jwt: "<jwt>", hosts: ["laptop-a", "cluster-a"]}
```

SPA generates alice's NKey pair in the browser (stored in `sessionStorage`), sends the public key in the login request. CP signs a user JWT for that public key and returns only the JWT — no seed. CP stores `(uslug) → nkey_public` for JWT refresh. Same pattern as hosts and agents: CP never handles private key material.

The user JWT's Pub.Allow includes:
- `mclaude.users.alice.hosts.*.>` (send commands to own hosts — session input, control, config, host management)
- `$JS.API.DIRECT.GET.KV_mclaude-hosts.$KV.mclaude-hosts.laptop-a` (per-host)
- `$JS.API.DIRECT.GET.KV_mclaude-hosts.$KV.mclaude-hosts.cluster-a` (per-host)
- Per-user bucket read subjects (sessions, projects)
- No `$JS.API.STREAM.INFO.KV_mclaude-hosts` (removed — prevents host enumeration)

---

## 2. SPA Connects to NATS

```
SPA → NATS server: CONNECT {jwt: "<alice-jwt>", nkey: "<proof>"}
NATS server validates: JWT signed by account key, NKey proof matches JWT subject claim.
Connection established.
```

SPA immediately sets up watches and consumers.

> **`<ephemeral>` consumer names**: The NATS client generates a random NUID (22-char base62 string, e.g. `dkzJpDSRQqqMSGe8mBGKBF`) client-side with no server coordination. For ordered push consumers, the client generates a fresh NUID on every creation and on every reset (sequence gap detected). These consumers are ephemeral — no durable state survives disconnect. JWT permission entries use `*` in this position to allow any client-generated name.

> **JetStream wire protocol**: Each KV watch or stream consumer involves multiple JetStream API calls. The NATS client SDK issues these automatically — they are listed here because each requires an explicit JWT permission entry.
>
> | Phase | Subject pattern | Purpose |
> |-------|----------------|---------|
> | **Bucket/stream init** | `$JS.API.STREAM.INFO.<stream>` | Client queries stream metadata (config, state) before first operation. Required for KV client initialization. |
> | **Consumer create** | `$JS.API.CONSUMER.CREATE.<stream>.<name>.<filter>` | Creates ordered push consumer with filter subject. |
> | **Consumer info** | `$JS.API.CONSUMER.INFO.<stream>.<name>` | Client queries consumer delivery state (ack floor, pending count). Called on reconnect and periodically. |
> | **Message delivery** | `$KV.<bucket>.<key>` or `mclaude.<subject>` | Server pushes messages to client on the consumer's deliver subject (Sub.Allow). |
> | **Acknowledgment** | `$JS.ACK.<stream>.>` | Client acknowledges received messages. Ordered push consumers auto-ack, but the wire protocol still uses this subject. |
> | **Flow control** | `$JS.FC.<stream>.>` | Server sends flow control requests under backpressure; client replies to resume delivery. Both Pub.Allow and Sub.Allow needed. |

### 2a. Watch sessions KV

```
SPA → NATS:  PUB $JS.API.STREAM.INFO.KV_mclaude-sessions-alice
             (bucket init — queries stream config + state)
NATS → SPA:  (reply on _INBOX) stream metadata: {config, state}
SPA → NATS:  PUB $JS.API.CONSUMER.CREATE.KV_mclaude-sessions-alice.<ephemeral>.$KV.mclaude-sessions-alice.hosts.>
             (creates ordered push consumer filtered to all session keys)
NATS → SPA:  Messages arrive on $KV.mclaude-sessions-alice.hosts.{hslug}.projects.{pslug}.sessions.{sslug}
SPA → NATS:  PUB $JS.ACK.KV_mclaude-sessions-alice.>  (auto-ack per message)
```

On reconnect or sequence gap, client calls `$JS.API.CONSUMER.INFO.KV_mclaude-sessions-alice.<name>` to check delivery state before recreating the consumer. Under backpressure, server sends flow control on `$JS.FC.KV_mclaude-sessions-alice.>` (Sub.Allow); client replies on the same prefix (Pub.Allow).

### 2a-direct. Direct-get for specific session (on-demand)

SPA can point-read a specific session KV entry without a full watch:

```
SPA → NATS:  PUB $JS.API.DIRECT.GET.KV_mclaude-sessions-alice.$KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.sess-001
NATS → SPA:  (reply) session KV value
```

Used when the SPA needs a single session's state (e.g., deep-linking to a session URL).

### 2b. Watch projects KV

```
SPA → NATS:  PUB $JS.API.STREAM.INFO.KV_mclaude-projects-alice
             (bucket init)
SPA → NATS:  PUB $JS.API.CONSUMER.CREATE.KV_mclaude-projects-alice.<ephemeral>.$KV.mclaude-projects-alice.hosts.>
NATS → SPA:  Messages arrive on $KV.mclaude-projects-alice.hosts.{hslug}.projects.{pslug}
```

Same wire protocol as 2a: ACK on `$JS.ACK.KV_mclaude-projects-alice.>`, consumer info on `$JS.API.CONSUMER.INFO.KV_mclaude-projects-alice.*`, flow control on `$JS.FC.KV_mclaude-projects-alice.>`.

### 2b-direct. Direct-get for specific project (on-demand)

```
SPA → NATS:  PUB $JS.API.DIRECT.GET.KV_mclaude-projects-alice.$KV.mclaude-projects-alice.hosts.laptop-a.projects.myapp
NATS → SPA:  (reply) project KV value
```

### 2c. Watch hosts KV (per-host scoped)

```
SPA → NATS:  PUB $JS.API.CONSUMER.CREATE.KV_mclaude-hosts.<ephemeral>.$KV.mclaude-hosts.laptop-a
             (one consumer per host in alice's JWT)
SPA → NATS:  PUB $JS.API.CONSUMER.CREATE.KV_mclaude-hosts.<ephemeral>.$KV.mclaude-hosts.cluster-a
NATS → SPA:  Messages arrive on $KV.mclaude-hosts.laptop-a, $KV.mclaude-hosts.cluster-a
```

No `$JS.API.STREAM.INFO.KV_mclaude-hosts` call — SPA uses raw NATS for the hosts bucket (direct-get + filtered consumer), not the high-level KV client. ACK on `$JS.ACK.KV_mclaude-hosts.>`, flow control on `$JS.FC.KV_mclaude-hosts.>`.

### 2c-direct. Direct-get for specific host (on-demand)

```
SPA → NATS:  PUB $JS.API.DIRECT.GET.KV_mclaude-hosts.$KV.mclaude-hosts.laptop-a
NATS → SPA:  (reply) host KV value
```

### 2d. Subscribe to session lifecycle notifications (dashboard)

```
SPA → NATS:  PUB $JS.API.STREAM.INFO.MCLAUDE_SESSIONS_alice
             (stream init)
SPA → NATS:  PUB $JS.API.CONSUMER.CREATE.MCLAUDE_SESSIONS_alice.<ephemeral>.mclaude.users.alice.hosts.*.projects.*.sessions.*.lifecycle.>
             (ordered push consumer, DeliverNew — only messages published after creation)
NATS → SPA:  Messages arrive on mclaude.users.alice.hosts.{hslug}.projects.{pslug}.sessions.{sslug}.lifecycle.{started|stopped|error}
```

ACK on `$JS.ACK.MCLAUDE_SESSIONS_alice.>`, consumer info on `$JS.API.CONSUMER.INFO.MCLAUDE_SESSIONS_alice.*`, flow control on `$JS.FC.MCLAUDE_SESSIONS_alice.>`.

This consumer is lightweight — lifecycle events are small and infrequent. It provides real-time "session started/stopped/error" notifications for the dashboard across all hosts and projects. Historical session state comes from the sessions KV bucket (step 2a), not stream replay.

### 2e. Open a session chat view (on-demand)

When alice clicks into a specific session to chat:

```
SPA → NATS:  PUB $JS.API.CONSUMER.CREATE.MCLAUDE_SESSIONS_alice.<ephemeral>.mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sess-001.>
             (ordered push consumer, DeliverAll — replays full conversation from the beginning)
NATS → SPA:  Replays: .events, .input, .lifecycle messages for sess-001 in order, then continues with live delivery
```

This consumer replays the full conversation history (`.events` = assistant output, `.input` = user messages, `.lifecycle` = start/stop) and then switches to live delivery for new messages. Destroyed when alice navigates away from the session.

---

### 2f. CLI Login and NATS Connection

Alice runs `mclaude login` from the CLI. This is a device-code flow via HTTP — distinct from the SPA's web login.

```
CLI:         Generates NKey pair (seed stored in ~/.mclaude/auth.json alongside JWT and userSlug).
CLI → CP:    POST /api/auth/device-code  {"nkey_public":"UDEF..."}
CP → CLI:    {"deviceCode":"ABCD-1234","verificationUrl":"https://mclaude.app/device","expiresIn":300}
CLI:         Displays "Open https://mclaude.app/device and enter code ABCD-1234"
Alice:       Opens URL in browser, enters code, approves.
CLI → CP:    POST /api/auth/device-code/poll  {"deviceCode":"ABCD-1234"}
CP → CLI:    {"jwt":"<user-jwt>","userSlug":"alice"}
CLI → NATS:  CONNECT {jwt: "<user-jwt>", nkey: "<proof-of-UDEF>"}
```

CLI now has a NATS connection with the same user JWT permissions as the SPA. The CLI can register hosts, create projects, manage hosts, etc. The NKey is persistent on disk — subsequent CLI invocations reuse the key and refresh the JWT via HTTP challenge-response.

---

## 3. Host Registration (one-time, BYOH)

Alice registers her laptop as a BYOH host.

```
CLI → NATS: PUB mclaude.users.alice.hosts._.register
            {"name":"My MacBook","type":"machine","nkey_public":"<host-public-key>"}
            (reply-to: _INBOX.xyz)
CP:         Validates alice's identity from NATS connection.
            Creates host row in Postgres (slug: "laptop-a", owner_id: alice, nkey_public: stored).
CP → NATS:  PUB _INBOX.xyz  {"ok":true,"slug":"laptop-a"}
```

The CLI doesn't write any credentials — it only registered the public key. The host controller authenticates itself directly via HTTP.

Host JWT Pub/Sub.Allow: `mclaude.hosts.laptop-a.>` only. Zero JetStream subjects.

---

## 4. Host Goes Online

Host controller starts, authenticates via HTTP, and connects to NATS.

```
laptop-a controller → CP:   POST /api/auth/challenge  {"nkey_public":"<host-public-key>"}
CP → controller:             {"challenge":"<nonce>"}
laptop-a controller → CP:   POST /api/auth/verify  {"nkey_public":"<host-public-key>","challenge":"<nonce>","signature":"<sig>"}
CP → controller:             {"ok":true,"jwt":"<host-jwt>"}
laptop-a controller → NATS: CONNECT {jwt: "<host-jwt>", nkey: "<proof>"}
```

NATS server emits a `$SYS.ACCOUNT.*.CONNECT` event. CP subscribes to `$SYS`:

```
NATS → CP:  $SYS.ACCOUNT.<acct>.CONNECT  {client: {nkey: "<host-public-key>", ...}}
CP:         Looks up host by public key in Postgres → laptop-a
            Writes to hosts KV:
CP → NATS:  PUB $KV.mclaude-hosts.laptop-a  {"slug":"laptop-a","name":"My MacBook","type":"machine","online":true,"lastSeenAt":"2026-04-30T12:00:00Z"}
```

Alice's SPA receives the KV watch update (step 2c) and shows laptop-a as online.

Host controller subscribes to its provisioning subject:

```
laptop-a controller → NATS: SUB mclaude.hosts.laptop-a.>
```

---

## 5. Host Credential Refresh

Host controller runs a refresh loop (before 5-min TTL expiry). Same HTTP challenge-response as bootstrap:

```
laptop-a controller → CP:  POST /api/auth/challenge  {"nkey_public":"<host-public-key>"}
CP → controller:            {"challenge":"<nonce>"}
laptop-a controller → CP:  POST /api/auth/verify  {"nkey_public":"<host-public-key>","challenge":"<nonce>","signature":"<sig>"}
CP → controller:            {"ok":true,"jwt":"<new-host-jwt>"}
laptop-a controller:        Updates NATS credentials in-flight (no reconnect needed)
```

If host access changes (e.g., owner revokes access for a user), CP revokes affected user JWTs. SPA reconnects and gets an updated JWT with the new host list.

---

## 6. Alice Creates a Project

Alice creates project `myapp` on `laptop-a`.

```
SPA → CP:   POST /api/users/alice/projects
            {"slug":"myapp","hostSlug":"laptop-a",
             "projectPath":"/home/alice/projects/myapp","backend":"claude_code"}
CP:         Validates: alice has access to laptop-a ✓, slug "myapp" is unique ✓
            Creates project in Postgres. Writes project KV entry.
CP → SPA:   HTTP 201 {"slug":"myapp","hostSlug":"laptop-a",...}
CP → NATS:  PUB mclaude.hosts.laptop-a.users.alice.projects.myapp.create
            {"id":"01JTRK...","ts":1714470085000,
             "projectPath":"/home/alice/projects/myapp","backend":"claude_code"}
```

The SPA sends an HTTP POST to CP. CP validates the request (authorization, slug uniqueness, host assignment), creates the Postgres record, writes the project KV entry, and then **CP itself publishes** the NATS fan-out message. The host controller receives this via its `mclaude.hosts.laptop-a.>` subscription and starts the agent subprocess for alice/myapp.

Error feedback flows through the HTTP response — if CP rejects the project (slug collision, invalid host, authorization failure), the SPA receives an HTTP error (409, 404, 403) with a JSON error body. Users do not publish directly to host-scoped subjects per ADR-0054.

The host controller starts the agent, which generates an NKey and the host controller registers it with CP via `mclaude.hosts.laptop-a.api.agents.register` (see scenario 7b).

---

## 7. Agent Startup and Credential Issuance

### 7a. Agent generates NKey

Inside the agent process (started by host controller):

```
Agent: kp, _ := nkeys.CreateUser()
       publicKey, _ := kp.PublicKey()  // "UABC..."
       seed, _ := kp.Seed()           // "SUABC..." (stays in agent memory)
       // Pass publicKey to host controller via local IPC (stdout/file/unix socket)
```

### 7b. Host controller registers agent public key with CP

```
laptop-a controller → NATS: PUB mclaude.hosts.laptop-a.api.agents.register
                             {"user_slug":"alice","project_slug":"myapp","nkey_public":"UABC..."}
                             (reply-to: _INBOX.abc)
CP:  Validates: alice has access to laptop-a ✓, project myapp exists ✓, assigned to laptop-a ✓
     Stores (alice, laptop-a, myapp) → "UABC..."
CP → NATS: PUB _INBOX.abc  {"ok":true,"quotaPublisher":true}
                           (quotaPublisher: true if this agent is designated as the quota publisher for this user,
                            false otherwise. Host controller forwards this field to the agent via local IPC.)

If CP hasn't processed the project create yet (race condition — fan-out processing order
is not guaranteed), CP returns {"ok":false,"error":"project not found","code":"NOT_FOUND"}.
Host controller retries with exponential backoff (100ms, 200ms, 400ms, ..., max 5s, max 10 attempts).
```

### 7c. Agent authenticates via HTTP

```
Agent → CP:  POST /api/auth/challenge  {"nkey_public":"UABC..."}
CP → Agent:  {"challenge":"<nonce>"}
Agent → CP:  POST /api/auth/verify  {"nkey_public":"UABC...","challenge":"<nonce>","signature":"<sig>"}
CP → Agent:  {"ok":true,"jwt":"<agent-jwt>"}
```

CP looks up the public key → finds the agent registration → signs JWT with per-project permissions, 5-min TTL.

### 7d. Agent connects to NATS

```
Agent → NATS: CONNECT {jwt: "<agent-jwt>", nkey: "<proof-of-UABC>"}
```

Agent JWT Pub.Allow includes:
- `mclaude.users.alice.hosts.laptop-a.projects.myapp.>` (lifecycle, session events)
- `$KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.>` (write session state)
- `$KV.mclaude-projects-alice.hosts.laptop-a.projects.myapp` (write project state)
- Per-project consumer-create, direct-get, ACK, flow control subjects

Agent JWT Sub.Allow includes:
- `mclaude.users.alice.quota` (receive quota updates — see section 8e)
- Per-project KV delivery subjects, session stream delivery subjects

Agent JWT Pub.Allow also includes (for designated quota publisher only):
- `mclaude.users.alice.quota` (publish quota updates — see section 9g)

Agent JWT does NOT include:
- `$JS.API.STREAM.INFO.KV_mclaude-hosts` (no host enumeration)
- `$JS.API.CONSUMER.MSG.NEXT.*` (uses ordered push consumers, not pull)
- Any subject for other projects or other hosts

---

## 8. Agent Sets Up Watchers

Agent JetStream calls follow the same wire protocol as the SPA (see section 2 table): STREAM.INFO for bucket/stream init, CONSUMER.CREATE, ACK, CONSUMER.INFO on reconnect, flow control under backpressure. All scoped to this agent's project.

### 8a. Watch own session keys

```
Agent → NATS: PUB $JS.API.STREAM.INFO.KV_mclaude-sessions-alice
              (bucket init)
Agent → NATS: PUB $JS.API.CONSUMER.CREATE.KV_mclaude-sessions-alice.<ephemeral>.$KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.>
              (ordered push consumer filtered to this project's sessions)
NATS → Agent: Messages on $KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.{sslug}
Agent → NATS: PUB $JS.ACK.KV_mclaude-sessions-alice.>  (auto-ack per message)
```

Direct-get for a specific session: `$JS.API.DIRECT.GET.KV_mclaude-sessions-alice.$KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.{sslug}`

### 8b. Watch own project state

```
Agent → NATS: PUB $JS.API.STREAM.INFO.KV_mclaude-projects-alice
              (bucket init)
Agent → NATS: PUB $JS.API.CONSUMER.CREATE.KV_mclaude-projects-alice.<ephemeral>.$KV.mclaude-projects-alice.hosts.laptop-a.projects.myapp
NATS → Agent: Messages on $KV.mclaude-projects-alice.hosts.laptop-a.projects.myapp
```

Direct-get: `$JS.API.DIRECT.GET.KV_mclaude-projects-alice.$KV.mclaude-projects-alice.hosts.laptop-a.projects.myapp`

### 8c. Subscribe to session commands (ordered push consumer on sessions stream)

```
Agent → NATS: PUB $JS.API.STREAM.INFO.MCLAUDE_SESSIONS_alice
              (stream init)
Agent → NATS: PUB $JS.API.CONSUMER.CREATE.MCLAUDE_SESSIONS_alice.<ephemeral>.mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.>
              (ordered push consumer, DeliverNew — agent processes commands as they arrive, no replay)
NATS → Agent: Messages on mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.{sslug}.{suffix}
```

### 8d. Read + watch own host config

Initial read (one-time direct-get):
```
Agent → NATS: PUB $JS.API.DIRECT.GET.KV_mclaude-hosts.$KV.mclaude-hosts.laptop-a
NATS → Agent: (reply) {"slug":"laptop-a","name":"My MacBook","type":"machine","online":true,...}
```

Then watch for live updates (host renamed, goes offline/online):
```
Agent → NATS: PUB $JS.API.CONSUMER.CREATE.KV_mclaude-hosts.<ephemeral>.$KV.mclaude-hosts.laptop-a
NATS → Agent: Messages on $KV.mclaude-hosts.laptop-a (push delivery on host KV changes)
```

### 8e. Subscribe to quota updates (core NATS)

```
Agent → NATS: SUB mclaude.users.alice.quota
```

> **JWT requirement:** The agent JWT must include `mclaude.users.alice.quota` in Sub.Allow (all agents) and Pub.Allow (designated quota publisher). This subject is at the user level, not under the per-project `hosts.{hslug}.projects.{pslug}` prefix. See section 7d for the full agent JWT permission summary.

One subscription per agent (not per session). The agent fans out each `QuotaStatus` message to all active QuotaMonitor goroutines. Only quota-managed sessions (those created with `softThreshold > 0`) have a QuotaMonitor. Interactive sessions ignore quota updates entirely.

**Quota publisher designation:** CP designates exactly one agent per user as the quota publisher. On agent registration (section 7b), CP returns `{"ok":true,"quotaPublisher":true}` in the reply if this agent is designated; the host controller forwards the `quotaPublisher` field to the agent via local IPC (same channel used for the NKey public key). The designated agent runs `runQuotaPublisher` (polls Anthropic API every 60s, publishes to this subject). Non-designated agents only subscribe. If the designated agent disconnects (CP detects via `$SYS.ACCOUNT.*.DISCONNECT`), CP re-designates the next online agent by publishing to that agent's project-level manage subject (see section 9i). This prevents duplicate API calls and inconsistent quota decisions across hosts.

### 8f. Subscribe to quota publisher designation (core NATS)

```
Agent → NATS: SUB mclaude.users.alice.hosts.laptop-a.projects.myapp.manage.designate-quota-publisher
```

This subject is within the agent's existing `mclaude.users.alice.hosts.laptop-a.projects.myapp.>` wildcard in Sub.Allow — no additional JWT permission needed. When received, the agent starts `runQuotaPublisher` (see section 9i). Agents that are already the designated quota publisher (from registration reply, section 7b) ignore this message.

---

## 9. Session Lifecycle (Chat)

### 9a. Interactive session — SPA sends create

Alice clicks "New Session" in the SPA. The agent for alice/myapp is already running (started in step 7).

```
SPA → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.create
            {"id":"01JTRK...","ts":1714470060000,"sessionSlug":"sess-001",
             "backend":"claude_code","model":"sonnet","permissionMode":"managed"}
```

Captured by `MCLAUDE_SESSIONS_alice` stream. Agent's ordered push consumer delivers it. Agent starts the CLI process.

### 9b. Agent publishes session started

```
Agent → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sess-001.lifecycle.started
              {"id":"01JTRK...","ts":1714470061000,"sessionSlug":"sess-001",
               "backend":"claude_code","startedAt":"2026-04-30T12:01:00Z",
               "capabilities":{"hasThinking":true,"hasSubagents":true,...}}
```

Captured by session stream. SPA receives via dashboard consumer (lifecycle filter).

### 9c. Agent writes session KV state

```
Agent → NATS: PUB $KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.sess-001
              {"slug":"sess-001","status":"running","backend":"claude_code",
               "startedAt":"2026-04-30T12:01:00Z","lastActivityAt":"2026-04-30T12:01:00Z"}
```

SPA receives via sessions KV watch (step 2a). Dashboard renders session as "running."

### 9d. Alice sends input

See 9e-input below for all input types. Simple chat message:

```
SPA → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sess-001.input
            {"id":"01JTRK...","ts":1714470062000,"type":"message",
             "text":"Hello, can you help me with...","attachments":[]}
```

Agent receives via sessions stream consumer. Also captured by stream for chat replay.

### 9e. Agent publishes session events (streaming response)

All events are published on `sessions.{sslug}.events`. The canonical event types (ADR-0005):

#### init (first event after CLI startup)
```
Agent → NATS: PUB ...sessions.sess-001.events
              {"id":"01JTRK...","ts":1714470061500,"type":"init","sessionId":"sess-001",
               "init":{"backend":"claude_code","model":"sonnet",
                       "tools":["Read","Write","Edit","Glob","Grep","Bash"],
                       "skills":[],"agents":[],"capabilities":{"hasThinking":true,"hasSubagents":true}}}
```

#### text_delta (streamed assistant text)
```
Agent → NATS: PUB ...sessions.sess-001.events
              {"id":"01JTRK...","ts":1714470063000,"type":"text_delta","sessionId":"sess-001",
               "textDelta":{"messageId":"msg-001","blockIndex":0,"text":"Sure! Let me..."}}
```

#### thinking_delta (extended thinking / chain-of-thought)
```
Agent → NATS: PUB ...sessions.sess-001.events
              {"id":"01JTRK...","ts":1714470062500,"type":"thinking_delta","sessionId":"sess-001",
               "thinkingDelta":{"messageId":"msg-001","text":"I need to check the auth module..."}}
```

#### tool_call (tool invocation starts)
```
Agent → NATS: PUB ...sessions.sess-001.events
              {"id":"01JTRK...","ts":1714470063500,"type":"tool_call","sessionId":"sess-001",
               "toolCall":{"toolUseId":"tc-001","toolName":"Read","input":{"file_path":"/src/auth.go"}}}
```

#### tool_progress (streaming tool output)
```
Agent → NATS: PUB ...sessions.sess-001.events
              {"id":"01JTRK...","ts":1714470063600,"type":"tool_progress","sessionId":"sess-001",
               "toolProgress":{"toolUseId":"tc-001","toolName":"Read","content":"package auth\n\nimport..."}}
```

#### tool_result (tool execution completes)
```
Agent → NATS: PUB ...sessions.sess-001.events
              {"id":"01JTRK...","ts":1714470063700,"type":"tool_result","sessionId":"sess-001",
               "toolResult":{"toolUseId":"tc-001","toolName":"Read","content":"...","isError":false}}
```

#### permission (request — waiting for user decision)
```
Agent → NATS: PUB ...sessions.sess-001.events
              {"id":"01JTRK...","ts":1714470064000,"type":"permission","sessionId":"sess-001",
               "permission":{"requestId":"perm-001","toolName":"Write",
                             "toolInput":"/src/auth.go",
                             "resolved":false}}
```

SPA shows permission prompt. Session KV → `status: requires_action`.

#### permission (resolved — user granted or denied)

After user responds (see 9e-perm-response below), agent publishes the resolution:
```
Agent → NATS: PUB ...sessions.sess-001.events
              {"id":"01JTRK...","ts":1714470064500,"type":"permission","sessionId":"sess-001",
               "permission":{"requestId":"perm-001","toolName":"Write",
                             "resolved":true,"allowed":true}}
```

Session KV → `status: running`.

#### turn_complete (turn ends with token stats)
```
Agent → NATS: PUB ...sessions.sess-001.events
              {"id":"01JTRK...","ts":1714470065000,"type":"turn_complete","sessionId":"sess-001",
               "turnComplete":{"inputTokens":1500,"outputTokens":800,
                               "costUsd":0.012,"durationMs":4500}}
```

Agent updates cumulative token counts in session KV.

#### error (non-fatal error during session)
```
Agent → NATS: PUB ...sessions.sess-001.events
              {"id":"01JTRK...","ts":1714470065000,"type":"error","sessionId":"sess-001",
               "error":{"message":"MCP server connection lost","code":"MCP_DISCONNECT"}}
```

Distinct from `lifecycle.error` (which means the session itself died). This is an in-session error that the SPA can display inline.

#### backend_specific (pass-through for backend-unique events)
```
Agent → NATS: PUB ...sessions.sess-001.events
              {"id":"01JTRK...","ts":1714470065000,"type":"backend_specific","sessionId":"sess-001",
               "backendSpecific":{"kind":"droid_mission_progress","data":{...}}}
```

SPA receives all events via per-session chat consumer. Ordered push consumer guarantees delivery order.

### 9e-input. SPA sends input types

#### message (chat)
```
SPA → NATS: PUB ...sessions.sess-001.input
            {"id":"01JTRK...","ts":1714470062000,"type":"message",
             "text":"Hello, can you help me with...","attachments":[]}
```

#### message with attachments
```
SPA → NATS: PUB ...sessions.sess-001.input
            {"id":"01JTRK...","ts":1714470062000,"type":"message",
             "text":"What's in this screenshot?",
             "attachments":[{"id":"att-001","filename":"screenshot.png",
                             "mimeType":"image/png","sizeBytes":245000}]}
```

Agent resolves the attachment by requesting a pre-signed download URL from CP via NATS request/reply, downloads from S3, and includes the content in the Claude message.

#### skill_invoke (SPA triggers a skill)
```
SPA → NATS: PUB ...sessions.sess-001.input
            {"id":"01JTRK...","ts":1714470062000,"type":"skill_invoke",
             "skillName":"deploy-server","args":{}}
```

Agent injects the skill invocation as a user message to Claude.

#### permission_response (user grants/denies a permission)
```
SPA → NATS: PUB ...sessions.sess-001.input
            {"id":"01JTRK...","ts":1714470064200,"type":"permission_response",
             "requestId":"perm-001","allowed":true}
```

Agent sends the tool approval/denial to the CLI subprocess. Publishes the resolved `permission` event (see above).

### 9f. Alice interrupts a session

```
SPA → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sess-001.control.interrupt
            {}
```

Agent sends SIGINT to the Claude subprocess. Current turn ends mid-response. SPA receives a `turn_complete` event with partial token counts.

### 9f-restart. Alice restarts a session

```
SPA → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sess-001.control.restart
            {}
```

Agent kills the current CLI subprocess, spawns a new one. New `init` event is published. Conversation context is lost — the new subprocess starts fresh (or uses `--resume` if `claudeSessionID` is available in session KV). Session KV → `status: running` with updated `startedAt`.

---

### 9g. Quota-managed session (ADR-0044)

SPA (or a skill) publishes `sessions.create` with quota fields. The agent manages the full lifecycle — no separate dispatcher. A single agent may run N quota-managed sessions concurrently, each with its own QuotaMonitor goroutine. The agent subscribes to `mclaude.users.{uslug}.quota` once (step 8e) and fans out each update to all active monitors.

#### Startup (immediate)

```
SPA → NATS:  PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.create
             {"id":"01JTRK...","ts":1714470070000,"sessionSlug":"sched-001",
              "backend":"claude_code","prompt":"Refactor the auth middleware to use JWT validation",
              "softThreshold":75,"hardHeadroomTokens":50000,
              "autoContinue":true,"branchSlug":"refactor-auth",
              "permPolicy":"strict-allowlist",
              "allowedTools":["Read","Write","Edit","Glob","Grep","Bash"]}
Agent:       Ensures worktree schedule/refactor-auth exists. Spawns CLI subprocess immediately.
             Extracts claudeSessionID from init event. Session KV → status: pending.
             Starts QuotaMonitor goroutine for this session (receives quota updates from agent's fan-out).
```

#### Prompt delivery (gated by quota)

```
Agent:       Quota update arrives (fan-out from 8e subscription). u5=40% < softThreshold(75%).
             QuotaMonitor for sched-001 delivers prompt.
Agent → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sched-001.input
              {"id":"01JTRK...","ts":1714470071000,"type":"message",
               "text":"Refactor the auth middleware to use JWT validation"}
Agent:       Session KV → status: running, claudeSessionID.
```

If quota is tight (`u5 >= softThreshold`) on the first update, the monitor holds the prompt. CLI process is warm and idle on stdin — no tokens consumed. When a subsequent update shows `u5 < softThreshold`, the prompt is sent.

#### Soft pause (quota pressure)

Each QuotaMonitor independently evaluates its own `softThreshold`. No cross-session coordination.

```
Designated agent → NATS: PUB mclaude.users.alice.quota
                         {"u5":76,"r5":"2026-04-30T17:00:00Z","hasData":true,...}
All agents:  Receive update. Fan out to their QuotaMonitors.
             sched-001 (softThreshold=75): u5(76%) >= 75% → pauses.
             sched-002 (softThreshold=90): u5(76%) < 90% → keeps running.
Agent → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sched-001.input
              {"id":"01JTRK...","ts":1714470080000,"type":"message",
               "text":"MCLAUDE_STOP: quota_soft"}
             QuotaMonitor sets stopReason="quota_soft", starts counting output tokens.
Claude:      Wraps up, commits work, ends turn.
Agent → NATS: PUB ...lifecycle.session_job_paused
              {"type":"session_job_paused","sessionId":"sched-001",
               "pausedVia":"quota_soft","u5":76,"r5":"2026-04-30T17:00:00Z"}
Agent:       Session KV → status: paused, pausedVia: quota_soft,
             resumeAt: "2026-04-30T17:00:00Z" (autoContinue=true → set to r5).
```

CLI subprocess stays alive — conversation context intact in memory.

#### Hard interrupt (token budget exceeded)

If Claude ignores the soft marker and keeps generating output:

```
Agent:       QuotaMonitor counts outputTokensSinceSoftMark via onRawOutput callback.
             At 50000 tokens: sends control_request interrupt directly on sess.stdinCh.
Claude:      Turn ends mid-response (interrupted). Stop hook NOT fired.
Agent → NATS: PUB ...lifecycle.session_job_paused
              {"type":"session_job_paused","sessionId":"sched-001",
               "pausedVia":"quota_hard","u5":78,"r5":"2026-04-30T17:00:00Z",
               "outputTokensSinceSoftMark":50000}
Agent:       Session KV → status: paused, pausedVia: quota_hard.
```

#### Resume (quota recovered)

```
Designated agent → NATS: PUB mclaude.users.alice.quota
                         {"u5":30,...}
All agents:  Receive update. Each paused QuotaMonitor re-evaluates independently.
             sched-001 (softThreshold=75): u5(30%) < 75% → resumes.
             Checks resumeAt (if set, waits until that time).
             Verifies CLI subprocess is alive.
Agent → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sched-001.input
              {"id":"01JTRK...","ts":1714470090000,"type":"message",
               "text":"Resuming — continue where you left off."}
Agent:       Session KV → status: running, pausedVia: "".
```

Same subprocess, same conversation — no restart, no context loss.

#### Completion

```
Claude:      Ends turn naturally. Stop hook allows stop.
Agent → NATS: PUB ...lifecycle.session_job_complete
              {"type":"session_job_complete","sessionId":"sched-001",
               "branch":"schedule/refactor-auth"}
Agent:       Session KV → status: completed. CLI subprocess exits.
             Session record persists — user reviews conversation in SPA, checks branch.
```

#### Permission denied (out-of-allowlist tool)

```
Claude:      Requests tool "mcp__gmail__send_email" (not in allowedTools).
Agent:       Auto-denies. Publishes session_permission_denied. Sends graceful stop.
Agent → NATS: PUB ...lifecycle.session_permission_denied
              {"type":"session_permission_denied","sessionId":"sched-001",
               "tool":"mcp__gmail__send_email"}
Agent:       Session KV → status: needs_spec_fix, failedTool: "mcp__gmail__send_email".
```

Session stays in KV. Will not auto-resume. User must cancel and re-create after fixing the tool allowlist.

#### Cancellation (user deletes a quota-managed session)

```
SPA → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sched-001.delete
            {}
Agent:       Sends control_request interrupt on sess.stdinCh. Stop hook NOT fired.
             CLI subprocess exits.
Agent → NATS: PUB ...lifecycle.session_job_cancelled
              {"type":"session_job_cancelled","sessionId":"sched-001"}
Agent → NATS: PUB $KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.sched-001
              KV-Operation: DEL  (tombstone)
```

Unlike completion, cancellation tombstones the session KV entry — the user explicitly chose to remove it.

#### Unrecoverable failure

```
Agent:       CLI subprocess crashes on startup (bad binary, permission error, OOM).
             Or: `claude --resume <claudeSessionID>` fails after session loss.
Agent → NATS: PUB ...lifecycle.session_job_failed
              {"type":"session_job_failed","sessionId":"sched-001",
               "error":"subprocess exited without turn-end signal"}
Agent:       Session KV → status: failed, error: "subprocess exited without turn-end signal".
```

Session stays in KV for user review. Will not auto-resume.

### 9h. Mid-session configuration change

SPA sends a config update to a running session (e.g., switch model, change permission mode):

```
SPA → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sess-001.config
            {"id":"01JTRK...","ts":1714470090000,
             "model":"opus",
             "permissionMode":"manual"}
```

Agent receives via sessions stream consumer. Updates the session's runtime config:
- **Model change**: takes effect on the next Claude turn (current turn completes with old model).
- **Permission mode change**: immediate — next permission prompt uses the new mode.

Agent writes updated config to session KV so the SPA reflects the change.

```
Agent → NATS: PUB $KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.sess-001
              {..., "model":"opus", "permissionMode":"manual"}
```

### 9i. Quota publisher re-designation

The designated quota publisher agent disconnects. CP detects and re-designates:

```
NATS → CP:   $SYS.ACCOUNT.<acct>.DISCONNECT  {client: {nkey: "<agent-public-key>", ...}}
CP:          Looks up agent by public key → alice/myapp/laptop-a.
             This agent was the designated quota publisher for alice.
             Finds next online agent for alice (alice/webapp/cluster-a).
CP → NATS:   PUB mclaude.users.alice.hosts.cluster-a.projects.webapp.manage.designate-quota-publisher
             {}
Agent (cluster-a): Receives designation. Starts runQuotaPublisher goroutine.
                   Begins polling Anthropic API every 60s, publishing to mclaude.users.alice.quota.
```

If the old agent comes back online, its registration reply will include `quotaPublisher: false` — it will not start its publisher.

### 9j. Quota update with HasData: false

Anthropic API call fails (credentials missing, network error, rate limited):

```
Designated agent → NATS: PUB mclaude.users.alice.quota
                         {"u5":0,"u7":0,"r5":"","r7":"","hasData":false,"ts":"2026-04-30T12:01:00Z"}
All agents:  QuotaMonitors see hasData=false. No action taken.
             Running sessions continue running. Paused sessions stay paused.
             Pending sessions (waiting for first quota) continue waiting.
```

QuotaMonitors never trigger pause or resume on stale data. The next successful poll (60s later) resumes normal evaluation.

### 9k. Hard-pause resume (distinct nudge)

When a session was hard-paused (interrupted mid-response), the resume nudge is different from soft-pause:

```
Agent → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sched-001.input
              {"id":"01JTRK...","ts":1714470090000,"type":"message",
               "text":"Your previous turn was interrupted mid-response. Check git status and recover state before continuing."}
Agent:       Session KV → status: running, pausedVia: "".
```

For soft-pause, the nudge is: "Resuming — continue where you left off." (see 9g Resume).

If the caller provided a custom `resumePrompt` in `sessions.create`, that is used instead of either default.

### 9l. Empty allowedTools rejection

```
SPA → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.create
            {"id":"01JTRK...","ts":1714470070000,"sessionSlug":"sched-bad",
             "backend":"claude_code","prompt":"...",
             "softThreshold":75,"permPolicy":"strict-allowlist","allowedTools":[]}
Agent:       Rejects: strict-allowlist with empty allowedTools is invalid.
Agent → NATS: PUB ...sessions.sched-bad.lifecycle.error
              {"reason":"strict-allowlist requires non-empty allowedTools"}
```

Session is not started. No KV entry created.

### 9m. Session resume (interactive)

SPA resumes an existing session (e.g., user returns to a session that was `stopped`):

```
SPA → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sess-001.control.restart
            {}
Agent:       Reads claudeSessionID from session KV.
             Spawns CLI with: claude --resume <claudeSessionID>
             New init event published. Session KV → status: running, updated startedAt.
```

If `claudeSessionID` is missing or `--resume` fails, agent falls back to a fresh CLI process (conversation context lost):

```
Agent:       --resume failed. Spawns fresh CLI process.
Agent → NATS: PUB ...sessions.sess-001.events
              {"type":"error","error":{"message":"Resume failed, starting fresh session","code":"RESUME_FAILED"}}
Agent → NATS: PUB ...sessions.sess-001.events
              {"type":"init",...}  (new session)
```

### 9n. Agent restart recovery (KV-based)

Agent process crashes and restarts (pod eviction, daemon restart, OOM kill). Recovery uses KV — not stream replay — as the source of truth (ADR-0044):

```
Agent:        Iterates all entries in KV_mclaude-sessions-alice for its host and project scope
              (keys matching hosts.laptop-a.projects.myapp.sessions.*).

              For each session KV entry:

              Interactive sessions (softThreshold == 0):
              - status: running → CLI subprocess is dead. Reads claudeSessionID, attempts --resume.
              - status: completed/failed/cancelled → No action (session is terminal).

              Quota-managed sessions (softThreshold > 0):
              - status: pending → CLI subprocess was warm but prompt was never sent.
                Respawns CLI subprocess. Starts new QuotaMonitor.
                Subscribes to quota updates. Gates prompt delivery on next quota update.
              - status: paused → Session was paused on quota.
                Checks if subprocess is alive. If alive: starts new QuotaMonitor, waits for quota recovery.
                If dead: attempts --resume with claudeSessionID from KV (degraded fallback).
                If autoContinue and resumeAt has passed, attempts resume immediately on next favorable quota update.
              - status: running → Session was mid-execution. CLI subprocess is dead (agent restarted).
                Reads claudeSessionID from KV, attempts claude --resume <claudeSessionID>.
                On success: starts new QuotaMonitor. On failure: updates KV → status: failed.
              - status: completed/failed/cancelled → No action (session is terminal).
```

Session KV is the recovery source of truth. No temporary stream consumers or replay infrastructure is needed.

---

## 10. Agent Credential Refresh

Before the 5-min TTL expires, the agent refreshes via the same HTTP challenge-response:

```
Agent → CP:  POST /api/auth/challenge  {"nkey_public":"UABC..."}
CP → Agent:  {"challenge":"<nonce>"}
Agent → CP:  POST /api/auth/verify  {"nkey_public":"UABC...","challenge":"<nonce>","signature":"<sig>"}
CP → Agent:  {"ok":true,"jwt":"<new-agent-jwt>"}
Agent:       Updates NATS credentials in-flight
```

Same code path as bootstrap — no distinction between initial auth and refresh. If the agent's JWT expires before refresh (e.g., agent was disconnected), the NATS server closes the connection. Agent re-authenticates via HTTP and reconnects.

---

## 11. Session Import (ADR-0053)

Alice imports a project from the CLI. Binary data flows through S3 with pre-signed URLs, not NATS.

### 11-pre. CLI checks slug availability

```
CLI → NATS: REQ mclaude.users.alice.hosts.laptop-a.projects.check-slug
            {"slug":"myapp"}
            (reply-to: _INBOX.xyz)
CP → CLI:   (reply) {"available":true}
```

If the slug is taken, CP returns `{"available":false,"suggestion":"myapp-2"}`. CLI can retry or prompt the user.

### 11a. CLI requests upload URL and uploads archive to S3

```
CLI → NATS: REQ mclaude.users.alice.hosts.laptop-a.projects.myapp.import.request
            {"sizeBytes":5242880}
CP  → CLI:  (reply) {"id":"imp-001","uploadUrl":"https://s3.../alice/laptop-a/myapp/imports/imp-001.tar.gz?X-Amz-Signature=..."}
CLI → S3:   PUT <uploadUrl>  <archive-bytes>
CLI → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.import.confirm
            {"importId":"imp-001"}
```

CLI uses NATS request/reply to get a pre-signed upload URL from CP, then uploads directly to S3 (public endpoint). Confirmation is a NATS publish.

### 11b. CP creates project and dispatches provisioning

```
CP:  Creates project in Postgres (source: "import", import_ref: S3 key)
     Writes project KV state with importRef
     Dispatches provisioning to host controller (same as step 6)
```

### 11c. Agent downloads archive from S3

Agent starts, reads project KV state, sees `importRef`:

```
Agent → NATS: REQ mclaude.users.alice.hosts.laptop-a.projects.myapp.import.download
              {"importId":"imp-001"}
CP → Agent:   (reply) {"downloadUrl":"https://s3.../alice/laptop-a/myapp/imports/imp-001.tar.gz?X-Amz-Signature=..."}
Agent → S3:   GET <downloadUrl>
Agent:        Unpacks archive to session data directory
```

### 11d. Agent signals completion

```
Agent → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.import.complete
              {"id":"01JTRK8MH...","ts":1714470095000}
Agent:  Clears importRef from project KV state
CP:     Receives signal, resolves import ID from project KV, deletes S3 object
```

Agent continues with normal operation. No one-shot JWTs, no NATS Object Store.

---

## 12. User JWT Refresh (periodic + on membership change)

### 12a. Periodic refresh

Same HTTP challenge-response as all identity types:

```
SPA → CP:  POST /api/auth/challenge  {"nkey_public":"<alice-public-key>"}
CP → SPA:  {"challenge":"<nonce>"}
SPA → CP:  POST /api/auth/verify  {"nkey_public":"<alice-public-key>","challenge":"<nonce>","signature":"<sig>"}
CP:        Resolves alice's accessible hosts → host slugs
           Signs new user JWT for that public key with current per-host entries
CP → SPA:  {"ok":true,"jwt":"<new-jwt>"}
SPA:       Updates NATS credentials in-flight
```

### 12b. Grant host access

Alice grants bob access to laptop-a:

```
CLI → NATS: REQ mclaude.users.alice.hosts.laptop-a.manage.grant
            {"userSlug":"bob"}
            (reply-to: _INBOX.xyz)
CP:         Validates: alice owns laptop-a ✓
            Inserts (host_id, user_id) into host_access table.
            Revokes bob's current NATS JWT (his host list changed).
CP → NATS:  PUB _INBOX.xyz  {"ok":true}
NATS server: Closes bob's SPA WebSocket (revoked JWT).
Bob's SPA:   Reconnects via HTTP auth → gets new JWT with laptop-a in host list.
             Re-establishes all watchers (steps 2a-2e), now including laptop-a.
```

### 12c. Revoke host access

```
CLI → NATS: REQ mclaude.users.alice.hosts.laptop-a.manage.revoke-access
            {"userSlug":"bob"}
            (reply-to: _INBOX.xyz)
CP:         Validates: alice owns laptop-a ✓
            Deletes (host_id, user_id) from host_access table.
            Revokes bob's NATS JWT.
            Revokes all agent JWTs for bob's projects on laptop-a.
CP → NATS:  PUB _INBOX.xyz  {"ok":true}
NATS server: Closes bob's SPA connection + all bob's agent connections on laptop-a.
Bob's SPA:   Reconnects with JWT that no longer includes laptop-a.
```

Bob's active sessions on laptop-a are terminated — agent JWTs revoked, session KV updated to `status: error`.

---

## 13. Session Deletion

Alice deletes a session from the SPA.

```
SPA → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sess-001.delete
            {}
```

Agent receives via sessions stream consumer:

```
Agent:  Stops Claude subprocess.
Agent → NATS: PUB mclaude.users.alice.hosts.laptop-a.projects.myapp.sessions.sess-001.lifecycle.stopped
              {"reason":"user_deleted"}
Agent → NATS: PUB $KV.mclaude-sessions-alice.hosts.laptop-a.projects.myapp.sessions.sess-001
              KV-Operation: DEL  (tombstone)
```

SPA receives both: lifecycle event via stream, KV deletion via watch.

---

## 13b. Project Deletion

Alice deletes a project from the SPA. This tears down all sessions, the agent, and the KV entry.

```
SPA → CP:   DELETE /api/users/alice/projects/myapp
CP:         Validates: alice has access to laptop-a ✓, project myapp exists ✓
CP → SPA:   HTTP 200 {"ok":true}
CP → NATS:  PUB mclaude.hosts.laptop-a.users.alice.projects.myapp.delete
            {"id":"01JTRK...","ts":1714470095000}
```

The SPA sends an HTTP DELETE to CP. CP validates the request, then publishes the NATS fan-out message (same pattern as project creation — see section 6). The host controller receives this via its `mclaude.hosts.laptop-a.>` subscription. Users do not publish directly to host-scoped subjects per ADR-0054.

**Host controller:**
```
Host controller: Signals agent to drain.
Agent:           For each active session:
                   Sends control_request interrupt on sess.stdinCh.
                   CLI subprocess exits.
                   Agent → NATS: PUB ...sessions.{sslug}.lifecycle.stopped  {"reason":"project_deleted"}
                   Agent → NATS: PUB $KV.mclaude-sessions-alice...sessions.{sslug}  KV-Operation: DEL
Agent:           All sessions drained. Agent exits.
Host controller: Cleans up local state (worktrees, data directory).
```

**CP:**
```
CP:          Validates: alice has access to laptop-a ✓, project myapp exists ✓
             Deletes project from Postgres.
             Removes stored agent NKey public keys for this project.
CP → NATS:   PUB $KV.mclaude-projects-alice.hosts.laptop-a.projects.myapp
             KV-Operation: DEL  (tombstone)
```

SPA sees the project disappear from the projects KV watch. All session KV entries are tombstoned. The agent's NATS connection closes when the host controller kills it (or the agent JWT expires without refresh — CP no longer has the agent's public key to issue a new one).

---

## 14. Host Goes Offline

Host controller disconnects (laptop closes, process killed, network loss).

```
NATS server: Detects disconnect.
NATS → CP:   $SYS.ACCOUNT.<acct>.DISCONNECT  {client: {nkey: "<host-public-key>", ...}}
CP:          Looks up host by public key → laptop-a
CP → NATS:   PUB $KV.mclaude-hosts.laptop-a  {"slug":"laptop-a",...,"online":false,"lastSeenAt":"2026-04-30T14:00:00Z"}
```

Alice's SPA receives the KV watch update and shows laptop-a as offline.

Agent connections also drop (they were connected to the same NATS server). Their JWTs expire within 5 minutes. CP cleans up the stored agent NKey public keys for laptop-a's agents.

---

## 15. Host Lifecycle Management

Alice manages hosts she owns via the CLI. All management subjects are under `mclaude.users.{uslug}.hosts.{hslug}.manage.*`, covered by the user JWT's `mclaude.users.alice.hosts.*.>`.

### 15a. Rekey (NKey rotation)

Alice reinstalled her laptop. The host controller generated a new NKey on first boot. Alice reads the new public key and re-attests:

```
CLI → NATS: PUB mclaude.users.alice.hosts.laptop-a.manage.rekey
            {"nkeyPublic":"UNEW..."}
            (reply-to: _INBOX.xyz)
CP:         Validates: alice owns laptop-a ✓
            Updates hosts.nkey_public in Postgres: "UOLD..." → "UNEW..."
CP → NATS:  PUB _INBOX.xyz  {"ok":true}
```

The old NKey is now dead. If laptop-a's controller was still connected with the old key, its next refresh via HTTP will fail (`unknown public key` — CP no longer has the old key). The controller must re-authenticate with the new key.

### 15b. Deregister (graceful shutdown)

Alice wants to remove laptop-a permanently:

```
CLI → NATS: PUB mclaude.users.alice.hosts.laptop-a.manage.deregister
            {}
            (reply-to: _INBOX.xyz)
CP:         Validates: alice owns laptop-a ✓
            Sends project delete for each active project (fan-out to host controller):
CP → NATS:    PUB mclaude.hosts.laptop-a.users.alice.projects.myapp.delete  {"id":"01JTRK...","ts":1714470090000}
            Host controller drains each agent (stops sessions, disconnects).
            CP deletes host KV entry:
CP → NATS:    PUB $KV.mclaude-hosts.laptop-a  (tombstone/DEL)
            CP removes host row from Postgres (nkey_public).
            CP revokes laptop-a's JWT.
CP → NATS:  PUB _INBOX.xyz  {"ok":true}
```

Alice's SPA receives the KV tombstone and removes laptop-a from the dashboard.

### 15c. Emergency revocation

Alice suspects laptop-a was compromised. Immediate disconnect, no graceful drain:

```
CLI → NATS: PUB mclaude.users.alice.hosts.laptop-a.manage.revoke
            {}
            (reply-to: _INBOX.xyz)
CP:         Validates: alice owns laptop-a ✓
            Adds laptop-a's JWT to NATS revocation list (immediate disconnect).
            Deletes stored nkey_public (host can't re-authenticate via HTTP).
            Marks host as revoked in Postgres.
CP → NATS:  PUB $KV.mclaude-hosts.laptop-a  {"slug":"laptop-a",...,"status":"revoked"}
NATS server: Closes laptop-a's connection (revoked JWT).
             Also closes all agent connections on laptop-a (their JWTs are still valid but
             the agents lose NATS access when the host process dies — they share the network).
CP → NATS:  PUB _INBOX.xyz  {"ok":true}
```

After revocation, the host cannot re-authenticate — its public key was deleted. Alice must re-register if she wants to use the host again (new NKey, new attestation).

### 15d. Rename / rebind

```
CLI → NATS: PUB mclaude.users.alice.hosts.laptop-a.manage.update
            {"name":"Alice's Work Laptop"}
            (reply-to: _INBOX.xyz)
CP:         Updates hosts.name in Postgres. Updates KV.
CP → NATS:  PUB $KV.mclaude-hosts.laptop-a  {"slug":"laptop-a","name":"Alice's Work Laptop",...}
CP → NATS:  PUB _INBOX.xyz  {"ok":true}

```

---

## 16. What Each Identity Can See (Summary)

| Activity | Alice SPA/CLI | Agent (alice/myapp/laptop-a) | laptop-a controller | CP |
|----------|--------------|------------------------------|--------------------|----|
| Sessions KV (all alice) | Read all (watch + direct-get) | Read/write own project only | None | Write (via agents) |
| Projects KV (all alice) | Read all (watch + direct-get) | Read/write own project only | None | Write on provisioning |
| Hosts KV | Read per-host (JWT-scoped, watch + direct-get) | Read + watch own host only | None | Write ($SYS) |
| Sessions stream | Read all (ordered push, DeliverNew or DeliverAll) | Read (DeliverNew commands) + write own project events | None | None |
| Binary data (S3) | Upload via pre-signed URL | Download via pre-signed URL | None | Signs URLs, validates ownership |
| Host provisioning subjects | None (project create/delete via HTTP to CP) | None | Subscribe to `mclaude.hosts.laptop-a.>` | Subscribe to `mclaude.hosts.*.users.*.>` (fan-out); publishes create/delete after HTTP validation |
| Host management | Publish `manage.*` for owned hosts | None | None | Subscribe + execute |
| Quota | None | Subscribe (all); publish (designated only) | None | None |
| JetStream wire protocol | STREAM.INFO, CONSUMER.CREATE/INFO, ACK, flow control | Same | None | Full access |
| `$SYS` events | None | None | None | Subscribe |

---

## Subject Namespace Map

```
mclaude.
├── users.{uslug}.
│   └── hosts.{hslug}.
│       ├── projects.check-slug                     # CLI → CP (request/reply): check project slug availability
│       └── projects.{pslug}.
│           ├── sessions.create                    # SPA → stream → agent
│           ├── sessions.{sslug}.events            # agent → stream → SPA
│           ├── sessions.{sslug}.input             # SPA → stream → agent
│           ├── sessions.{sslug}.delete            # SPA → stream → agent
│           ├── sessions.{sslug}.control.interrupt  # SPA → stream → agent
│           ├── sessions.{sslug}.control.restart    # SPA → stream → agent
│           ├── sessions.{sslug}.config             # SPA → stream → agent (model, permissions)
│           ├── sessions.{sslug}.lifecycle.started               # agent → stream → SPA
│           ├── sessions.{sslug}.lifecycle.stopped               # agent → stream → SPA
│           ├── sessions.{sslug}.lifecycle.error                 # agent → stream → SPA
│           ├── sessions.{sslug}.lifecycle.session_job_paused    # agent → stream → SPA (quota)
│           ├── sessions.{sslug}.lifecycle.session_job_complete  # agent → stream → SPA (quota)
│           ├── sessions.{sslug}.lifecycle.session_job_cancelled # agent → stream → SPA (quota)
│           ├── sessions.{sslug}.lifecycle.session_job_failed    # agent → stream → SPA (quota)
│           ├── sessions.{sslug}.lifecycle.session_permission_denied  # agent → stream → SPA (quota)
│           ├── import.request                      # CLI → CP (request/reply): get pre-signed upload URL
│           ├── import.confirm                      # CLI → CP: archive uploaded to S3
│           ├── import.download                     # agent → CP (request/reply): get pre-signed download URL
│           ├── import.complete                     # agent → CP
│           ├── attachments.download                # agent → CP (request/reply): get pre-signed download URL for attachment
│           ├── attachments.upload                  # agent → CP (request/reply): get pre-signed upload URL for agent-generated attachment
│           ├── attachments.confirm                 # agent → CP: attachment uploaded to S3
│           └── manage.designate-quota-publisher    # CP → agent: you are now the quota publisher for this user
│   ├── quota                                        # designated agent → all agents (core NATS, 60s interval)
│   ├── hosts._.register                            # CLI → CP (request/reply): register new host public key
│   └── hosts.{hslug}.manage.
│       ├── update                                  # CLI → CP: rename, change type
│       ├── grant                                   # CLI → CP: grant another user access to this host
│       ├── revoke-access                           # CLI → CP: revoke another user's access to this host
│       ├── rekey                                   # CLI → CP: rotate NKey public key (SSH known_hosts model)
│       ├── deregister                              # CLI → CP: drain sessions + cleanup
│       └── revoke                                  # CLI → CP: emergency credential revocation
│
├── hosts.{hslug}.                                          # host-scoped subjects (fan-out)
│   ├── users.{uslug}.projects.{pslug}.create       # SPA → CP + host controller (fan-out)
│   ├── users.{uslug}.projects.{pslug}.delete       # SPA/CP → CP + host controller (fan-out)
│   └── api.agents.register                         # host controller → CP (request/reply): register agent public key

$KV.
├── mclaude-sessions-{uslug}.hosts.{hslug}.projects.{pslug}.sessions.{sslug}     # agent writes, SPA reads
├── mclaude-projects-{uslug}.hosts.{hslug}.projects.{pslug}                     # agent + CP write, SPA reads
└── mclaude-hosts.{hslug}                                                        # CP writes, SPA + agent read (per-host scoped)
#
# No $O.* (Object Store) subjects — binary data (imports, attachments) uses S3 with pre-signed URLs (ADR-0053)
#
MCLAUDE_SESSIONS_{uslug}
    filter: mclaude.users.{uslug}.hosts.*.projects.*.sessions.>
    captures: events, input, delete, control, config, lifecycle
$JS.API.
├── STREAM.INFO.<stream>                                    # bucket/stream init (SPA, agent)
├── CONSUMER.CREATE.<stream>.<name>.<filter>                # create ordered push consumer (SPA, agent)
├── CONSUMER.INFO.<stream>.<name>                           # query consumer state on reconnect (SPA, agent)
├── DIRECT.GET.<stream>.<key>                               # KV point read (SPA, agent)
$JS.ACK.<stream>.>                                          # message acknowledgment (SPA, agent)
$JS.FC.<stream>.>                                           # flow control under backpressure (SPA, agent — both Pub + Sub)
#
$SYS.ACCOUNT.*.CONNECT                                     # NATS server → CP
$SYS.ACCOUNT.*.DISCONNECT                                  # NATS server → CP
```
