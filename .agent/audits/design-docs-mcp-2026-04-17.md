## Run: 2026-04-17T00:00:00Z

**Gaps found: 10**

1. **`mclaude-mcp` uses Node/tsc, not Bun — design assumes `bun:sqlite` is available** — The existing `mclaude-mcp` package uses `"type": "module"`, a `tsconfig.json` targeting `Node16`, and `node src/index.js` as its start command. It is a Node package, not a Bun package. The design says "Language: TypeScript (bun)" and "Matches mclaude-mcp. Same SDK, same tooling. `bun:sqlite` has native FTS5 support." The new server is in a separate directory (`mclaude-docs-mcp/`), so it can use Bun independently — but the design does not specify what `package.json` looks like for this server, what the `bun run` entrypoint is, whether a `tsconfig.json` is needed for Bun, or whether `bun:sqlite` is a dependency or a built-in import. A developer would need to know: does `bun:sqlite` require a `bun` field in `package.json`? What is the exact import path (`import { Database } from "bun:sqlite"`)?  These are not stated.
   - **Doc**: "Language | TypeScript (bun) | Matches mclaude-mcp. Same SDK, same tooling."
   - **Code**: `mclaude-mcp/package.json` — `"scripts": { "start": "node src/index.js" }`, `tsconfig.json` targets `Node16`. No Bun usage anywhere in the existing package.

2. **`.mcp.json` doesn't exist — no merge or creation instruction** — The design says "Add to `.mcp.json` at repo root (create if missing)". The file does not currently exist. The design provides the JSON block for the `docs` server key, but does not address whether the existing `mclaude-mcp` server registration (which Claude Code must already be loading somehow) needs to co-exist in this file, or whether there is a separate mechanism (e.g. a user-level MCP config, a `.claude/settings.local.json`, or `enableAllProjectMcpServers`) that registers `mclaude-mcp`. If `.mcp.json` is created fresh with only the `docs` server, the existing server registration may be lost. A developer cannot determine the correct full contents of `.mcp.json` from the design alone.
   - **Doc**: "Add to `.mcp.json` at repo root (create if missing)"
   - **Code**: No `.mcp.json` exists at repo root.

3. **`cwd` in `.mcp.json` registration is a placeholder, not resolvable** — The design shows `"cwd": "<repo-root>"` in the `.mcp.json` snippet. This is not a valid JSON value; it is a placeholder. The doc does not say how `<repo-root>` is resolved — whether it should be an absolute path, a relative path, or if Claude Code's `.mcp.json` supports a special variable. If Claude Code resolves `cwd` relative to the `.mcp.json` file location (repo root), then `"cwd": "."` would be correct. A developer must stop and ask.
   - **Doc**: `"cwd": "<repo-root>"` in the registration snippet.
   - **Code**: No existing `.mcp.json` to compare against.

4. **Lineage indexing step 2 uses `--since=<last_commit>` but that is not valid `git log` syntax** — `git log --since` accepts a date/time, not a commit hash. To get commits reachable after a specific commit, the correct syntax is `git log <last_commit>..HEAD --name-only --diff-filter=M -- docs/*.md`. Using `--since=<hash>` will produce a git error. The implementation cannot proceed as documented.
   - **Doc**: "git log --since=<last_commit> --name-only --diff-filter=M -- docs/*.md" (Lineage Indexing, step 2)
   - **Code**: N/A — this is a pure algorithm gap.

5. **Lineage initial seed is undefined — what happens on first startup when `last_lineage_commit` is null?** — The lineage algorithm says "Get last processed commit hash from DB (stored in a metadata table)" then uses it in the git log command. On first run the metadata table is empty. The design does not say what the developer should pass to git log in this case: scan all history (`git log HEAD`), scan only the last N commits, or scan since a fixed date. This is a blocking implementation decision.
   - **Doc**: "Get last processed commit hash from DB (stored in a metadata table)" — no initial value specified.
   - **Code**: N/A.

6. **Section "modified in a commit" detection is underspecified for the parent-commit line-range problem** — The design says: "A section is 'modified in a commit' if the diff hunk overlaps with the section's line range. The line ranges come from the *parent* commit's version of the file (for deletions/changes) and the current version (for additions)." The current DB only stores line ranges for the current on-disk version of each section. There is no stored line range for arbitrary historical commits. To map a diff hunk to a section, the developer must parse the section boundaries from the *parent commit's* file content (not from the DB). The design does not say how to do this: re-run the markdown parser on the parent commit blob, or infer it from the diff? This is ambiguous enough that two developers would implement it differently.
   - **Doc**: "Parse the diff to find which ## sections were modified" and "The line ranges come from the *parent* commit's version of the file"
   - **Code**: N/A.

7. **`sections_fts` uses `content=` external content table but the content reindex procedure deletes and re-inserts `sections` rows — this can leave the FTS index stale without a full rebuild** — The design uses `content='sections'` (external content FTS5 table), which means FTS does not store its own copy of the text; it relies on the `sections` table. The delete/insert triggers are defined, but step 7 of content indexing says "Delete old sections for this doc" then "Insert new sections (triggers update FTS)". The AFTER DELETE trigger fires on each row deletion, which will call the `'delete'` command on the FTS index. This is correct in principle. However, the design does not address what happens if the process crashes between step 7 (delete) and step 8 (insert) — the FTS index would be out of sync with the `sections` table. The design says "Rebuilt from scratch if deleted" for the DB file, but there is no specified recovery path for a partially-updated FTS index that leaves the DB file intact.
   - **Doc**: "Delete old sections for this doc" / "Insert new sections (triggers update FTS)" — no atomicity / transaction wrapping specified.
   - **Code**: N/A.

8. **`fs.watch` with `recursive: true` behavior on Linux is unspecified and unreliable** — The design states "`fs.watch` (Node/Bun built-in) on the `docs/` directory with `recursive: true`". On Linux, Node.js `fs.watch` with `recursive: true` is not supported in older Node versions and has only been added in Node 22+; Bun's support may differ. The fallback is "polling (stat all files every 5s)" but the design does not specify how the developer detects that `fs.watch` with `recursive` fails — whether an exception is thrown at watch creation or silently degrades. This is a blocking implementation question on the target platform (k3d runs Linux containers).
   - **Doc**: "The server uses `fs.watch` (Node/Bun built-in) on the `docs/` directory with `recursive: true`" and "Falls back to polling (stat all files every 5s). Logs warning."
   - **Code**: N/A — but the MCP server runs locally (stdio, not in k3d), so this is a developer-machine concern. Still blocking because the fallback detection method is unspecified.

9. **`get_lineage` return shape references `doc_title` and `category` but `lineage` table stores only `doc_path` and `heading` — the join is not specified** — The `lineage` table schema stores `section_a_doc`, `section_a_heading`, `section_b_doc`, `section_b_heading`, `commit_count`, and `last_commit`. The `get_lineage` tool return shape includes `doc_title` and `category`. These fields live on the `documents` table, not `lineage`. The design does not specify the SQL join needed to resolve them, and more importantly does not specify what to return if the document has been deleted from the index (i.e., lineage references a doc that no longer exists in `documents`). Should the row be omitted, or returned with null title/category?
   - **Doc**: `get_lineage` returns `{ doc_path, doc_title, category, heading, commit_count, last_commit }` but `lineage` table has no `doc_title` or `category` column.
   - **Code**: N/A.

10. **DB file not gitignored — `.docs-index.db` will not be excluded by the current `.gitignore`** — The design says the DB is at `mclaude-docs-mcp/.docs-index.db` and is "Gitignored. Rebuilt from scratch if deleted." The current `.gitignore` does not contain a rule for `*.db` or `mclaude-docs-mcp/.docs-index.db`. A developer following the spec would need to add a gitignore entry, but the design does not specify where or how this should be done (top-level `.gitignore`, or a `.gitignore` inside `mclaude-docs-mcp/`).
    - **Doc**: "Co-located with server code. Gitignored."
    - **Code**: `/Users/270840341/work/mclaude/.gitignore` — no `*.db` or `mclaude-docs-mcp/` entry present.

## Run: 2026-04-17T12:00:00Z

**Gaps found: 2**

All 10 gaps from the previous run have been addressed. Two new gaps were introduced by the fixes.

1. **Lineage algorithm: no command specified to enumerate files modified per commit** — Step 2 of lineage indexing runs `git log <last_commit>..HEAD --reverse --format=%H -- docs/*.md`, which returns only commit hashes. Step 3 then says "For each commit that touches 2+ docs in docs/" and "For each modified file: git show <commit>:<file>" — both of which require a per-commit file list. No git command to produce that file list is specified (e.g. `git diff-tree --no-commit-id -r --name-only <commit> -- docs/*.md`). A developer cannot implement step 3 without knowing how to go from a commit hash to the list of `docs/*.md` files it touched.
   - **Doc**: Step 2: `git log ... --format=%H`; Step 3: "For each commit that touches 2+ docs" / "For each modified file: git show <commit>:<file>" — no intervening file-listing command.
   - **Code**: N/A — pure algorithm gap.

2. **Lineage algorithm: `git diff <commit>~1..<commit>` fails for root commits** — Step 3c runs `git diff <commit>~1..<commit> -- docs/*.md`. For the repository's initial commit (no parent), `<commit>~1` does not resolve to a valid ref and the command will fail with a "bad revision" error. The design does not specify how to handle root commits — whether to skip them, use the empty-tree SHA (`4b825dc...`), or use `git show --diff-filter=A <commit>`. Any repository's first commit is a root commit, so this is a real code path.
   - **Doc**: "git diff <commit>~1..<commit> -- docs/*.md to get the diff hunks" (Lineage Indexing, step 3c) — no root-commit handling specified.
   - **Code**: N/A — pure algorithm gap.

## Run: 2026-04-17T18:00:00Z

CLEAN — no blocking gaps found.

Both gaps from round 2 have been resolved:

1. **Lineage file enumeration** — Step 3a now explicitly specifies `git diff-tree --no-commit-id -r --name-only <commit> -- docs/*.md` (and the `--root` variant for root commits). Verified against the actual repo: this command correctly returns modified `.md` files for both normal commits and the root commit.

2. **Root commit diff handling** — Step 3e now specifies `git diff-tree -p --root <commit> -- docs/*.md` for root commits, and step 3a specifies `--root` for file enumeration. The design adds root-commit detection via `git rev-list --parents -1 <commit>`. All three git commands verified to work correctly against this repo's actual root commit (`a1d52a5`).

All other previously-flagged gaps remain resolved:
- `.mcp.json` creation is unambiguous (file confirmed absent; doc explicitly states "no existing server registrations to preserve" — confirmed via search of `~/.claude/settings.json`).
- `cwd` field removed from `.mcp.json` snippet; doc explains Claude Code uses `.mcp.json` directory as default cwd.
- `git log <last_commit>..HEAD --reverse --format=%H` is correct syntax.
- First-run path (null `last_lineage_commit`) uses `git log --reverse --format=%H -- docs/*.md` (full history scan).
- Section boundary sourcing from `git show <commit>:<path>` is explicit in the algorithm.
- Content indexing steps 6–10 are wrapped in a BEGIN/COMMIT TRANSACTION block — FTS atomicity is addressed.
- `fs.watch` fallback detection: "if `fs.watch` throws on setup" is sufficiently specified.
- `get_lineage` deleted-doc behavior: "lineage entry is omitted from results" is now stated.
- DB gitignore: design specifies `mclaude-docs-mcp/.gitignore` with pattern `.docs-index.db` plus repo root `*.db` entry. Root `.gitignore` currently lacks `*.db` but the doc tells the developer to add it.

## Run: 2026-04-20T00:00:00Z

**Gaps found: 9**

1. **`spec-state-schema.md` contradicts the ADR migration DDL on `hosts.public_key` nullability** — The canonical state schema (`hosts` table, `public_key` column) reads `NOT NULL` unconditionally. The ADR's migration DDL says `public_key TEXT, -- NOT NULL for machine hosts; NULL for cluster hosts` and enforces this via a CHECK constraint. A developer implementing `RegisterCluster`/`GrantClusterAccess` cannot tell which is correct — the DB will either reject cluster host inserts (schema says NOT NULL) or accept them (ADR says nullable). The schema is authoritative and must be updated or the ADR must be corrected.
   - **Doc**: "Key columns: ... `public_key` (NKey public)" and migration DDL `public_key TEXT,  -- NOT NULL for machine hosts; NULL for cluster hosts`
   - **Code**: `spec-state-schema.md` line 59: `| public_key | TEXT | NOT NULL | NKey public key ...`

2. **NKey signing pattern for host JWT issuance is unspecified** — The existing `IssueUserJWT` and `IssueSessionAgentJWT` in `nkeys.go` generate a new NKey pair internally and return the seed. For BYOH host registration, the host generates its own NKey locally and submits only the public key. The CP must sign a JWT using the host's submitted public key (not one it generated). A developer implementing `RegisterHost` cannot determine: (a) what function signature to use, (b) whether to call `natsjwt.NewUserClaims(hostPublicKey)` directly on the submitted key, or (c) whether a new helper like `IssueHostJWT(uslug, hslug, hostPublicKey, accountKP)` is needed. The returned artifact is a signed JWT (no seed returned, since the seed stayed on the host) — this is architecturally different from all existing JWT issuance, and no spec for the new function is given.
   - **Doc**: "CLI generates an NKey pair locally. ... CLI calls `POST /api/users/{uslug}/hosts` with `{name, publicKey, type}`. Control-plane ... signs a JWT with host-scoped permissions, returns it."
   - **Code**: `nkeys.go` — all three existing issuance functions (`IssueUserJWT`, `IssueSessionAgentJWT`, `GenerateUserNKey`) generate the NKey internally. No host-public-key signing path exists.

3. **Migration mechanism for new DDL is not specified** — The control-plane's `db.Migrate()` applies the `schema` constant in `db.go`, which uses `IF NOT EXISTS` DDL. The ADR provides multi-step migration SQL (CREATE TABLE hosts, ALTER TABLE projects ADD COLUMN host_id, backfill, make NOT NULL, drop old index, create new index) but does not say how this DDL will be applied: (a) extend the `schema` constant in `db.go`, (b) a separate migration runner/tool, or (c) a one-time migration binary. The existing `schema` constant uses only `CREATE TABLE IF NOT EXISTS` and `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` — it has no mechanism for multi-step transactional migrations, uniqueness index changes, or backfill programs. A developer cannot implement the migration without knowing which path to take.
   - **Doc**: "Migration DDL" section provides SQL steps but does not reference any migration framework or the `db.go` `schema` constant.
   - **Code**: `db.go` lines 53-56 and the `schema` constant (lines 314-349) — single-shot `IF NOT EXISTS` DDL, no versioning or multi-step ordering.

4. **`dispatchQueuedJob` and `processDispatch` UUID-as-slug coercion must be resolved but the migration path for `job.HostSlug` population is not specified** — `daemon_jobs.go` line 342 calls `subj.UserProjectAPISessionsCreate(d.cfg.UserSlug, slug.ProjectSlug(job.ProjectID))` — casting a UUID as `ProjectSlug`. After BYOH, this must become `UserHostProjectAPISessionsCreate(userSlug, hostSlug, projectSlug)`. The ADR says `JobEntry` gains `hostSlug` and `handleJobsRoute` POST requires it as a field. But it does not specify: (a) how the `POST /jobs` caller knows the `hostSlug` at job creation time, (b) whether `hostSlug` is resolved from the project's DB row, or (c) whether it must be passed in the request body explicitly. The same gap applies to `processDispatch` (line 635, 647) which also calls old-form subject helpers using only `job.ProjectID`.
   - **Doc**: "`JobEntry` gains `hostSlug` field. ... The `handleJobsRoute` POST handler reads `hostSlug` from the request body (required field)."
   - **Code**: `daemon_jobs.go` lines 342, 438, 635, 647, 848 — all call old two-arg `subj.UserProject*` helpers with `slug.ProjectSlug(job.ProjectID)` (UUID coercion). No hostSlug in `JobEntry` struct (`state.go` line 141-163).

5. **`handleJobsProjects` prefix filter must change but the new filter semantics are not specified** — `daemon_jobs.go` line 887 uses `prefix := userID + "."` to filter the `mclaude-projects` KV. After BYOH, the KV key format is `{uslug}.{hslug}.{pslug}`. The ADR says "KV key prefix lookup switches from UUID (`userID + "."`) to slug-based (`userSlug + "." + hostSlug + "."`)". But the daemon manages one host (per `mclaude daemon --host <hslug>`), so filtering by `userSlug + "." + hostSlug + "."` makes sense — but this is not explicit. A developer must decide: does this endpoint filter to the daemon's specific host, or to all projects for the user across all hosts? The ADR text ("Dispatcher uses slug fields ... to construct KV keys") does not resolve this for `handleJobsProjects`.
   - **Doc**: "`handleJobsProjects` handler: KV key prefix lookup switches from UUID (`userID + "."`) to slug-based (`userSlug + "." + hostSlug + "."`)."
   - **Code**: `daemon_jobs.go` line 887: `prefix := userID + "."` — UUID-based, against a KV that will have slug-based keys.

6. **Hardcoded lifecycle init subject in `main.go` bypasses `subj` package and will silently break after host insertion** — `main.go` line 200 constructs the init lifecycle subject as a raw format string: `"mclaude.users.%s.projects.%s.lifecycle._init"`. After ADR-0004, the correct shape is `mclaude.users.{uslug}.hosts.{hslug}.projects.{pslug}.lifecycle._init`. This string will not be caught at compile time (it bypasses the typed `subj` helpers). The ADR's session-agent component change section does not list this specific call site, and there is no `HOST_SLUG` env var available in `main.go` at the point where this subject is constructed (the host slug is not yet read before calling `NewAgent`).
   - **Doc**: "All NATS subscriptions use host-inclusive subject shape via `pkg/subj` helpers." / "Reads `HOST_SLUG` env var"
   - **Code**: `main.go` line 200 — raw format string `"mclaude.users.%s.projects.%s.lifecycle._init"`, constructed before `HOST_SLUG` is read.

7. **`DaemonConfig` has no `HostSlug` field and daemon startup with missing host credentials is not specified** — The existing `DaemonConfig` struct (`daemon.go` line 37) has no `HostSlug` field. The daemon opens `mclaude-laptops` KV on startup; after BYOH the bucket is renamed to `mclaude-hosts`. The ADR says the daemon starts with `mclaude daemon --host <hslug>` and reads credentials from `~/.mclaude/hosts/{hslug}/nats.creds`. But it does not specify: (a) the exact flag name and env var for `HostSlug` on the daemon, (b) what error the daemon produces if `--host` is omitted but there are multiple registered hosts, (c) what happens at startup if `mclaude-hosts` KV does not yet exist (the old `mclaude-laptops` is gone but a new-format daemon connects before the CP migrates the bucket name). The `NewDaemon` function calls `js.KeyValue(ctx, "mclaude-laptops")` — any rename must also be coordinated with CP startup order.
   - **Doc**: "`mclaude daemon --host <hslug>` starts daemon scoped to a specific host." / "KV bucket ... `mclaude-laptops` renamed to `mclaude-hosts`."
   - **Code**: `daemon.go` lines 37-48 (`DaemonConfig`) — no `HostSlug` field. `daemon.go` line 85: `js.KeyValue(ctx, "mclaude-laptops")` hardcoded.

8. **`projects.go` NATS subscriber subject and KV key format are legacy and the migration is not specified in the component changes** — `projects.go` line 45 subscribes to `mclaude.*.api.projects.create` with `parts[1]` as the raw `userID` (UUID). After BYOH the canonical subject is `mclaude.users.{uslug}.api.projects.create` and the payload gains `hostSlug`. Line 141 writes the KV key as `userID + "." + proj.ID` (UUID + UUID). After BYOH the key format is `{uslug}.{hslug}.{pslug}`. The ADR's "Component Changes → mclaude-control-plane" section lists new host endpoints and subject-publishing changes but does not explicitly call out this subscriber rewrite, nor does it specify what `parts` index the `uslug` is at after the subject structure change (it would be `parts[2]`).
   - **Doc**: "Subject-publishing for project-scoped messages uses host-inclusive `pkg/subj` helpers." / "`mclaude.users.{uslug}.api.projects.create` — payload gains `hostSlug` field"
   - **Code**: `projects.go` lines 45, 48, 141 — old subject and UUID-based KV key.

9. **The `POST /api/users/{uslug}/hosts` and `POST /api/hosts/register` request and response schemas are not fully specified** — The authed registration endpoint (`POST /api/users/{uslug}/hosts`) request body is implied as `{name, publicKey, type}` from the user flow prose but is never formally listed with field names, types, required/optional status, and validation rules. The response schema is described only as "signs a JWT ... returns it" — a developer cannot determine the exact JSON shape (e.g., is it `{slug, jwt, serverUrl}` like the device-code path, or just `{jwt}`?). The device-code endpoint (`POST /api/hosts/register`) response is `{slug, jwt, serverUrl}` per the flow, but the HTTP status codes for each success and error case are not listed for either endpoint. The error table lists `duplicate host name → numeric suffix` but not what HTTP status is returned for an auth failure on `POST /api/users/{uslug}/hosts` when the OAuth token's `{uslug}` doesn't match the URL `{uslug}`.
   - **Doc**: User flow step 4-6 (authed), step 5-7 (device code) — prose only, no formal request/response schemas.
   - **Code**: `server.go` / `admin.go` — no host registration handlers exist yet; developer must implement from scratch with no schema contract.

## Run: 2026-04-20T00:00:00Z

Evaluated: docs/adr-0004-multi-laptop.md (BYOH — Bring Your Own Host)
Cross-checked: docs/spec-state-schema.md, docs/adr-0024-typed-slugs.md, docs/adr-0016-nats-security.md, docs/adr-0011-multi-cluster.md
Code verified: mclaude-common/pkg/slug/slug.go, mclaude-common/pkg/subj/subj.go, mclaude-control-plane/db.go, mclaude-control-plane/nkeys.go, mclaude-control-plane/projects.go, mclaude-session-agent/daemon.go, mclaude-session-agent/state.go, mclaude-session-agent/agent.go, mclaude-session-agent/daemon_jobs.go

CLEAN — no blocking gaps found.

### Cross-spec consistency check

All key fields and key formats in the ADR match spec-state-schema.md exactly:
- `mclaude-sessions` key: `{uslug}.{hslug}.{pslug}.{sslug}` — match
- `mclaude-projects` key: `{uslug}.{hslug}.{pslug}` — match
- `mclaude-hosts` key: `{uslug}.{hslug}` — match (renamed from `mclaude-laptops`)
- `mclaude-job-queue` key: `{uslug}.{jobId}` — unchanged, match
- `hosts` table columns — match (id, user_id, slug, name, type, role, cluster_id, public_key, created_at, last_seen_at)
- `projects.host_id` FK — match
- `user_clusters` removal — consistent across ADR-0004, spec-state-schema, ADR-0011 supersession note
- JetStream filter strings — match
- NATS subject tree — match
- HTTP URL structure — match
- `hosts` reserved word — already present in slug.go (reservedHosts constant at line 77)
- `FormatNATSCredentials` — exists in nkeys.go:150; ADR says move to mclaude-common/pkg/nats/creds.go (valid since it's an ADR-describing future change, not a pre-existing gap)
- `mclaude-heartbeats` bucket removal — consistent (agent.go currently has hbKV/kvBucketHeartbeats; ADR prescribes removal, which is implementation work not a spec gap)
- `EXTERNAL_URL` env var — confirmed to exist in main.go:67; ADR's `serverUrl` from EXTERNAL_URL is correct
- `mclaude-hosts` KV bucket: spec-state-schema.md says "Created by: control-plane (pre-created; opened by daemon in `NewDaemon`)" — consistent with ADR's description that CP creates on startup, daemon opens it
- `HostSlug` type — already in slug.go:29 (from ADR-0024 implementation)

### Scoping rule applied

Per instructions: code still using old patterns (mclaude-laptops, LaptopsKVKey, UserProject* helpers without host arg, heartbeat bucket, missing HOST_SLUG) is expected pre-implementation and not reported as gaps.
