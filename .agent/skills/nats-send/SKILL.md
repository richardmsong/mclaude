# NATS Send

Send a message to a mclaude session via the NATS API. Used for testing the deployed platform end-to-end.

## Usage

```
/nats-send <message>
```

Optional arguments:
- `--session <id>` — target session ID (default: auto-detect from KV)
- `--project <id>` — target project ID (default: auto-detect from KV)
- `--watch` — subscribe to events stream and print the response

Examples:
- `/nats-send say hello in one word`
- `/nats-send --watch what is 2+2`
- `/nats-send --session abc123 explain this codebase`

---

## Algorithm

### 1. Ensure NATS port-forward

Check if localhost:4222 is reachable. If not, start a port-forward:

```bash
kubectl port-forward svc/mclaude-nats 4222:4222 -n mclaude-system &>/dev/null &
```

### 2. Get NATS credentials

Login via the control-plane HTTP API to get a user JWT and NKey seed:

```bash
# Port-forward control-plane if needed
kubectl port-forward svc/mclaude-control-plane 8080:8080 -n mclaude-system &>/dev/null &

# Login
curl -s http://localhost:8080/auth/login -X POST \
  -H 'Content-Type: application/json' \
  -d '{"email":"dev@mclaude.local","password":"dev"}'
```

Write the JWT and NKey seed to a creds file at `/tmp/mclaude-nats.creds`:

```
-----BEGIN NATS USER JWT-----
<jwt>
------END NATS USER JWT------

-----BEGIN USER NKEY SEED-----
<nkeySeed>
------END USER NKEY SEED------
```

### 3. Resolve target session

If `--session` and `--project` not provided, auto-detect from KV:

```bash
# Get user ID from login response
USER_ID=<from login>

# List sessions
nats --creds /tmp/mclaude-nats.creds -s nats://localhost:4222 kv ls mclaude-sessions
# Key format: {userId}.{projectId}.{sessionId}

# If multiple sessions, pick the first idle one by reading each value
nats --creds /tmp/mclaude-nats.creds -s nats://localhost:4222 kv get mclaude-sessions '<key>' --raw
# Parse JSON, check state=="idle"
```

### 4. Send the message

The input subject is: `mclaude.{userId}.{projectId}.api.sessions.input`

The payload must include `session_id` (snake_case) and `type` (stream-json requires it):

```bash
nats --creds /tmp/mclaude-nats.creds -s nats://localhost:4222 pub \
  "mclaude.${USER_ID}.${PROJECT_ID}.api.sessions.input" \
  '{"session_id":"<sessionId>","type":"user","message":{"role":"user","content":"<message>"}}'
```

### 5. Watch for response (if --watch)

Subscribe to the JetStream events stream for the session:

```bash
nats --creds /tmp/mclaude-nats.creds -s nats://localhost:4222 sub \
  "mclaude.${USER_ID}.${PROJECT_ID}.events.${SESSION_ID}" \
  --stream mclaude-events
```

Parse incoming events. Look for `assistant` type events containing the response text. Print the response content and stop when an `idle` state change is received.

If not `--watch`, just confirm the message was published and check KV for state change from `idle` to `busy`.

---

## Important

- Field name is `session_id` (snake_case), NOT `sessionId` (camelCase)
- Dev credentials: `dev@mclaude.local` / `dev`
- NATS CLI binary: `nats` (installed at `nats`)
- The session must be in `idle` state to accept input
- Claude Code inside the container must be authenticated (has OAuth token) for the message to produce a response
