# Schedule Feature

Schedule an unattended dev-harness implementation session as a job in the daemon's job queue. The session runs against a spec doc, with quota-aware graceful stopping.

## Usage

```
/schedule-feature <spec-path> [--priority N] [--threshold N] [--auto-continue]
```

Arguments:
- `spec-path` — relative path to spec doc (e.g., `docs/plan-quota-aware-scheduling.md`). Must exist.
- `--priority N` — integer 1–10; default 5. Higher = survives quota pressure longer.
- `--threshold N` — integer 1–99; default 75. 5h utilization % at which to trigger graceful stop.
- `--auto-continue` — flag; if set, job re-queues at the 5h reset time after being stopped.

Examples:
- `/schedule-feature docs/plan-spa.md`
- `/schedule-feature docs/plan-k8s-integration.md --priority 7 --threshold 80`
- `/schedule-feature docs/plan-quota-aware-scheduling.md --auto-continue`

---

## Algorithm

### 1. Parse arguments

Extract `spec-path` and optional flags from the arguments string. Defaults:
- priority: 5
- threshold: 75
- auto-continue: false

### 2. Verify spec path exists

```bash
test -f <spec-path>
```

If missing, tell the user and stop.

### 3. Resolve project ID

Call the daemon's jobs HTTP server to get the project list:

```bash
curl -s http://localhost:8378/jobs/projects
```

This returns `[{"id": "...", "name": "..."}]`. Match `basename(CWD)` against `project.name` to find the `projectId`.

If no match: display the project list and ask the user to pick by name.
If the daemon is not running (connection refused): tell the user to start the daemon first.

### 4. Create the job

```bash
curl -s -X POST http://localhost:8378/jobs \
  -H 'Content-Type: application/json' \
  -d '{"specPath":"<spec-path>","projectId":"<projectId>","priority":<N>,"threshold":<N>,"autoContinue":<bool>}'
```

Parse the response `{"id": "...", "status": "queued"}`.

### 5. Display result

Show:
- Job ID
- Spec path
- Priority
- Threshold
- Auto-continue
- Status (queued)

Optionally call `GET /jobs` to show the current queue depth.

---

## Important

- The daemon must be running for this to work (it hosts the HTTP server on localhost:8378)
- The daemon's job dispatcher will start the session automatically when quota allows
- Use `/job-queue` to monitor job status after scheduling
