# Job Queue

View and manage scheduled dev-harness jobs in the daemon's job queue.

## Usage

```
/job-queue [list|cancel <jobId>|status <jobId>]
```

Default (no subcommand): same as `list`.

Examples:
- `/job-queue` — list all jobs
- `/job-queue list` — list all jobs
- `/job-queue status abc12345` — show full details for a job
- `/job-queue cancel abc12345` — cancel a running or queued job

---

## Algorithm

### `list` (default)

```bash
curl -s http://localhost:8378/jobs
```

Parse the JSON array of JobEntry objects. Display as a table:

```
ID         SPEC                     PRI  STATUS           SESSION
abc12345   docs/adr-YYYY-MM-DD-spa.md         7    running          sess-xyz
def67890   docs/adr-...-k8s-integration.md   5    queued           -
```

Columns:
- ID: first 8 characters of the job UUID
- SPEC: spec path, truncated to 25 chars
- PRI: priority (1–10)
- STATUS: queued, starting, running, paused, completed, failed, needs_spec_fix, cancelled
- SESSION: first 8 chars of session ID, or `-` if none

If the response is empty, say "No jobs in queue."

### `status <jobId>`

```bash
curl -s http://localhost:8378/jobs/<jobId>
```

Display the full JobEntry including all fields:
- ID, status, spec path, priority, threshold, auto-continue
- Session ID, branch, PR URL (if completed)
- Failed tool (if needs_spec_fix), error (if failed)
- Retry count, resume at (if paused)
- Created at, started at, completed at

### `cancel <jobId>`

```bash
curl -s -X DELETE http://localhost:8378/jobs/<jobId>
```

If the job is running, the daemon will stop the session before marking it cancelled. Confirm the cancellation to the user.

---

## Important

- The daemon must be running (localhost:8378) for these commands to work
- Jobs are persisted in the `mclaude-job-queue` NATS KV bucket — they survive daemon restarts
- Use `/schedule-feature` to create new jobs
