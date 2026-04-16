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

If `--session` and `--project` not provided, auto-detect or create:

```bash
USER_ID=<from login>

# List all session keys for this user
KEYS=$(nats --creds /tmp/mclaude-nats.creds -s nats://localhost:4222 \
  kv ls mclaude-sessions --raw 2>/dev/null | grep "^${USER_ID}\.")

# Find the first idle session
SESSION_KEY=""
for KEY in $KEYS; do
  STATE=$(nats --creds /tmp/mclaude-nats.creds -s nats://localhost:4222 \
    kv get mclaude-sessions "$KEY" --raw 2>/dev/null \
    | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('state',''))" 2>/dev/null)
  if [ "$STATE" = "idle" ]; then
    SESSION_KEY="$KEY"
    break
  fi
done
```

**If no idle session found — create one:**

First resolve a project ID. Check `mclaude-projects` KV for any key matching `{userId}.*`:

```bash
PROJECT_KEY=$(nats --creds /tmp/mclaude-nats.creds -s nats://localhost:4222 \
  kv ls mclaude-projects --raw 2>/dev/null | grep "^${USER_ID}\." | head -1)
PROJECT_ID=$(echo "$PROJECT_KEY" | cut -d. -f2)
```

If no project exists either, the cluster isn't seeded — stop and report that.

Then publish a create message (branch must be a valid git branch; use `main`):

```bash
REQUEST_ID="req-$(openssl rand -hex 6)"
nats --creds /tmp/mclaude-nats.creds -s nats://localhost:4222 pub \
  "mclaude.${USER_ID}.${PROJECT_ID}.api.sessions.create" \
  "{\"projectId\":\"${PROJECT_ID}\",\"branch\":\"main\",\"name\":\"nats-send-session\",\"requestId\":\"${REQUEST_ID}\"}"
```

Poll until the session appears in KV (up to 10s):

```bash
for i in $(seq 10); do
  sleep 1
  SESSION_KEY=$(nats --creds /tmp/mclaude-nats.creds -s nats://localhost:4222 \
    kv ls mclaude-sessions --raw 2>/dev/null | grep "^${USER_ID}\.${PROJECT_ID}\." | head -1)
  [ -n "$SESSION_KEY" ] && break
done
```

Parse out PROJECT_ID and SESSION_ID from the key (`{userId}.{projectId}.{sessionId}`).

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
- NATS CLI binary: `nats` — must be on PATH. Install: `brew install nats-io/nats-tools/nats`. Never hardcode an absolute path.
- The session must be in `idle` state to accept input
- Claude Code inside the container must be authenticated. With `/deploy-local-preview` this is handled automatically via `devOAuthToken`. If sessions receive messages but produce no response, check `kubectl get secret user-secrets -n mclaude-{userId} -o jsonpath='{.data.oauth-token}' | base64 -d` — should be non-empty.

## Prerequisites

```bash
which nats   # must be on PATH — install: brew install nats-io/nats-tools/nats
which kubectl
which curl
```
