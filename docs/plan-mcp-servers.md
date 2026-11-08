# MCP servers (status, reload, config, sign-in)

## Overview

Every MClaude session runs its own headless Claude Code process, and each process holds its own MCP server connections. Today the iOS app can't see whether a session's MCP servers connected, can't reconnect one that failed, can't change the MCP config, and can't sign in to a server that needs OAuth. The laptop is **headless**: everything here must work from the phone alone, with no step on the Mac.

This feature adds:
- A per-session **MCP servers** sheet (••• menu): live status of every server in that session's process, with reconnect, enable/disable, sign-in and restart.
- A global **MCP servers** screen in Settings: the user-level MCP config (add, edit, delete), each server's status across all sessions, and **Restart all sessions** to apply config changes.

The scope is the iOS app and mclaude-server. k8s sessions aren't covered. Their MCP endpoints answer 404, and they don't appear in the global screen's session lists.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Where status lives | **Per session** (sheet from •••) plus a **global** Settings screen (Richard). No project screen. | MCP connections belong to a process, so the per-session view is the detailed one. Most config is per user, so it's edited globally. |
| Per-session actions | Reconnect one, Reconnect all, Enable/disable switch, Restart Claude (Richard). | |
| Enable/disable scope | **The session's project** (its cwd), persisted. The switch says so. | Claude Code saves the toggle in the project's `disabledMcpServers`; there's no per-process toggle. The server applies the toggle to every running session in the same cwd so they agree. |
| Project-scope servers | Shown in the per-session sheet under **This project**: reconnect and enable only, not editable (Richard: no new screen). | |
| Config editing | **Form only, no JSON editor** (Richard). User-scope servers only. Plugin and project servers are read-only. | |
| Renaming | Not supported. Delete and re-add instead. | Keeps the edit atomic. |
| Applying config edits | A banner offers **Apply to open sessions**, which restarts every session's process (Richard). | Edits only reach a process when it starts. |
| Global "restart all" | **Restarts every session's Claude process** with `--resume` (Richard). Idle sessions restart now; working ones restart when their turn ends. | Picks up config edits as well as reconnecting. |
| Secrets | Env values and header values are sent to the app **masked in the UI**, revealed per row with an eye button or in the editor (Richard). | Same trust as the rest of the token-authenticated API. |
| OAuth sign-in | **From the phone** (Richard: "pretend I can't go back to the computer"). An in-app browser catches the localhost callback automatically. A fallback opens the link in the phone's **default browser** and the callback URL is pasted back. | Some providers (e.g. Google) refuse sign-in in embedded web views. |

## Background: Claude Code's control protocol (observed on 2.1.317)

These are stream-json `control_request`s on the session process's stdin. Each gets a `control_response` with the same `request_id`.

- `{"subtype":"mcp_status"}` → `{"mcpServers":[…]}`. Each entry has `name` and `status` (`connected`, `pending`, `failed`, `needs-auth`, `disabled`), plus optional `error`, `serverInfo {name, version}`, `config` (the server's config object), `transport`, `scope` (`user`, `project`, `local`, `dynamic`, …), `source` (`user`, `plugin`, …) and `tools` (array).
- `{"subtype":"mcp_reconnect","serverName":…}` → success once the reconnect has been started. The server may report `pending` briefly afterwards.
- `{"subtype":"mcp_toggle","serverName":…,"enabled":bool}` → success. Persisted in `~/.claude.json` → `projects[<cwd>].disabledMcpServers`.
- `{"subtype":"mcp_authenticate","serverName":…}` → `{authUrl, callbackPort, state, redirectScheme:"localhost", requiresUserAction, callbackExpected}`. The `authUrl` redirects to `http://localhost:<callbackPort>/callback?code=…&state=…`.
- `{"subtype":"mcp_oauth_callback_url","serverName":…,"callbackUrl":…}` → success, or an error such as "Callback URL not accepted: … OAuth state mismatch …". A rejected URL leaves the sign-in waiting, so it can be retried. The callback only works on the process that started the sign-in.
- User-scope config lives in `~/.claude.json` → `mcpServers`. It is written only through `claude mcp add-json -s user <name> <json>` and `claude mcp remove -s user <name>`, never by editing the file (Claude processes write it concurrently).

Every rule below must fail safe. An unrecognized status shows as its raw word, unknown config keys are preserved, and an unknown control error is shown verbatim.

## Server

### Control requests

- The server sends MCP control requests to a session's process and waits for the `control_response` with the matching `request_id`.
  - Timeouts: 10 s for status and toggle; 30 s for reconnect (it answers once the server is back, and `npx` servers can be slow while others are still starting); 15 s for authenticate and callback. A timeout answers **504** `{"error":"Claude didn't answer"}`.
  - An `error` response answers **502** `{"error":"<Claude's error text>"}`.
- A session must have a running, ready process. Otherwise the endpoint answers **409** `{"error":"Claude isn't running in this session"}`.
- Control responses the server didn't ask for are ignored, as today.

### Per-session endpoints

Visibility and ownership are the same as `/sessions/:id/usage`.

- `GET /sessions/:id/mcp` → `{"project": {"name", "cwd"}, "servers": [McpServer]}`
  - McpServer:
    - `name`
    - `status`
    - `error`: optional
    - `version`: optional, from `serverInfo.version`
    - `transport`: e.g. `stdio`, `http`, `sse`
    - `group`: `user`, `project` or `plugin`. Source `plugin` → `plugin`. Scope `project` or `local` → `project`. Anything else → `user`.
    - `summary`: stdio servers show the command plus args, space-joined. URL servers show the URL.
    - `tools`: tool names (empty when none are known)
  - Servers are sorted by group (user, project, plugin), then by name.
- `POST /sessions/:id/mcp/reconnect` with `{"name": "<server>"}`, or `{}` for every server whose status isn't `disabled`.
  - The reconnects run in parallel. When all have answered, the endpoint returns the fresh `GET` body.
  - A single failed reconnect doesn't fail the request; that server's status and error show the failure.
- `POST /sessions/:id/mcp/toggle` with `{"name", "enabled"}`.
  - Sends `mcp_toggle` to this session, then to every other running session with the same cwd.
  - Returns the fresh `GET` body for this session.
- `POST /sessions/:id/mcp/auth` with `{"name"}` → `{"authUrl", "callbackPort"}` from `mcp_authenticate`.
- `POST /sessions/:id/mcp/auth/callback` with `{"name", "callbackUrl"}` → sends `mcp_oauth_callback_url`.
  - On success, the server sends `mcp_reconnect` for that server to every other running session (the credential is stored once, per user) and returns the fresh `GET` body.
  - Claude's rejection answers **400** with its error text.
- `POST /sessions/:id/restart` → restarts this session's process (see Restart below).
  - **409** `{"error":"Finish or stop the current turn first"}` while a turn is running or a prompt is pending.
  - **409** for a session that's stopped (In use, waiting for its first message) or already restarting.
  - Otherwise `{"status":"restarting"}`.

### Global endpoints

These are for the laptop owner only (same check as `/transcripts`). Anyone else gets **403**.

- `GET /mcp` → `{"servers": [GlobalMcpServer], "sessionCount": n, "staleSessions": n}`
  - User servers come from `~/.claude.json` → `mcpServers`, read-only. Plugin servers come from the status of running sessions. Project-scope servers aren't listed.
  - GlobalMcpServer:
    - `name`
    - `group`: `user` or `plugin`
    - `editable`: true for user servers whose config `type` is `stdio` (or absent), `http` or `sse`
    - `config`: the full config object, secrets included. Omitted for plugin servers.
    - `summary`
    - `sessions`: `[{"id", "name", "status", "error"?}]`, one per running managed session that reported this server
  - Statuses come from `mcp_status` on every running, ready managed session, queried in parallel with a 5 s timeout each. Sessions that don't answer are left out.
  - `sessionCount` is the number of sessions that answered.
  - `staleSessions` is the number of running sessions whose process started with a different user MCP config than the current one. The fingerprint is a hash of `mcpServers` serialized with sorted keys, recorded when each process launches.
- `POST /mcp/servers` with `{"name", "config"}` → `claude mcp add-json -s user`.
  - The name must match `^[A-Za-z0-9_.-]{1,64}$`; otherwise **400**.
  - **409** if a user server with that name exists.
  - A CLI failure answers **400** with its stderr.
  - Success returns the fresh `GET /mcp` body.
- `PUT /mcp/servers/:name` with `{"config"}`.
  - The new config is the old config object with the submitted keys replaced, so keys the form doesn't know (timeouts, oauth settings) are kept. A key the form cleared (env, headers, args) is sent as an empty value and removed.
  - Applied as `remove` then `add-json`. If `add-json` fails, the old config is added back and the request answers **400** with the CLI's stderr.
  - **404** for an unknown name.
- `DELETE /mcp/servers/:name` → `claude mcp remove -s user`. **404** for an unknown name.
- `POST /mcp/restart-all` restarts every managed session's process.
  - Idle sessions restart now, staggered 1 s apart.
  - Sessions with a running turn or pending prompt are marked and restart when that turn ends.
  - Stopped (In use) and already-restarting sessions are skipped.
  - Returns `{"restarting": n, "afterTurn": n}`.

### Restart

- A deliberate restart stops the process (as on delete) and launches it again. It uses `--resume`, or a fresh start with the same session id for a session never messaged. It's not a crash: no backoff, no crash count, no "⚠︎ Claude exited" line.
- While stopping and until the new process is ready, `process` is `restarting`, then `starting`, then `running`. These are the existing process states.
- The transcript gets one line: "↻ Restarted Claude to reload MCP servers."
- A restart deferred until the turn ends happens right after the turn's result, before queued messages are delivered. Those messages then go to the new process.
- If the launch fails, the session follows the crash-retry rules from "Session processes".

## iOS app

### Names

- Plugin servers are reported as `plugin:<plugin>:<server>` (e.g. `plugin:Notion:notion`). The app shows only the last part (`notion`) in rows, titles and accessibility labels. Requests still use the full name. Other names show as-is.

### Per-session sheet

- **Entry:** the session ••• menu gains **MCP servers** (`puzzlepiece.extension`) after Info. It opens a large-detent sheet titled "MCP servers" with a Done button.
- The sheet loads `GET /sessions/:id/mcp` on open. Pull to refresh reloads it.
  - While any server is `pending`, it reloads every 2 s, for up to 30 s.
  - A 409 shows a muted "Claude isn't running in this session yet." with a Retry button.

```
┌──────────────────────────────────────┐
│ MCP servers                     Done │
│ mclaude-v1                           │  ← project name, muted caption
│ ┌──────────────────────────────────┐ │
│ │ ↻ Reconnect all                  │ │  ← secondary capsule
│ └──────────────────────────────────┘ │
│ USER                                 │
│ ● playwright   v1.65 · 25 tools  ↻ ◉ │
│ ● bitwarden    failed            ↻ ◉ │
│   spawn ENOENT bitwarden-mcp …       │  ← danger caption, 3 lines
│ ● searxng      disabled            ○ │
│ THIS PROJECT                         │
│ ● db-tools     connected · 6 tools ↻ ◉│
│ PLUGINS                              │
│ ● Notion   needs sign-in [Sign in] ◉ │
│                                      │
│ Turning a server off applies to every│
│ session in mclaude-v1 until it's     │
│ turned back on.                      │  ← muted footnote
│ ┌──────────────────────────────────┐ │
│ │ Restart Claude                   │ │
│ └──────────────────────────────────┘ │
│ Restarts this session's Claude to    │
│ pick up MCP config changes. The      │
│ conversation continues.              │
└──────────────────────────────────────┘
```

- **Sections:** User, This project, Plugins, in that order, each omitted when empty. The rows are raised-bg cards.
- **Row:**
  - A status dot: success = connected; clay = pending (shows a small spinner instead of the dot); danger = failed; link blue = needs-auth; none = disabled.
  - The name.
  - A muted detail:
    - connected: `v<version> · <n> tools`
    - pending: `connecting`
    - failed: `failed`
    - needs-auth: `needs sign-in`
    - disabled: `disabled`
    - any other value: the raw status
  - A failed row shows the error under it in danger caption, 3 lines. Tapping it expands to the full error, which is selectable.
  - Tapping the row (outside its controls) expands it to show the summary in muted mono 11 and the tool names as a wrapped list in muted caption. Tapping again collapses it.
- **Reconnect (↻):** shown for every status except disabled.
  - Tapping it swaps the icon for a spinner, calls reconnect with that name, and applies the returned list.
  - On error, an inline danger caption on the row shows the error.
- **Enable switch:** on unless the status is `disabled`.
  - Toggling it calls toggle and applies the returned list. While the request runs, the switch is disabled.
  - On error, the switch reverts and an inline danger caption shows the error.
- **Sign in:** shown only for `needs-auth`. It's a link-blue capsule that opens the sign-in sheet (below).
- **Reconnect all:** spinner while running, then applies the returned list. Disabled while it runs.
- **Restart Claude:** disabled, with the footnote "Finish or stop the current turn first", while the session is working or waiting on you.
  - Tapping it calls restart. The sheet then shows "Restarting Claude…" with a spinner in place of the server list until the session's `process` is `running` again, then reloads.
  - If it isn't running again within 60 s, it shows "Claude hasn't come back yet." with Retry.

### Sign-in sheet

- **Opening:** tapping Sign in calls `POST …/mcp/auth`, then presents a large-detent sheet titled "Sign in to <name>". It has Cancel and an **Open in browser** button in its toolbar.
- **In-app browser:** the sheet's body is an in-app web view that loads `authUrl`.
  - Before each navigation, if the URL's host is `localhost` or `127.0.0.1` and its port equals `callbackPort`, the navigation is cancelled.
  - The full URL is then posted to `…/mcp/auth/callback`, and the sheet shows "Finishing sign-in…" with a spinner.
- **Open in browser:** opens `authUrl` in the phone's default browser and switches the sheet to the paste state.
  - The paste state shows: "After you approve, your browser lands on a page that can't load. Copy that page's address and paste it here."
  - Under it are a system paste button (no clipboard permission prompt) and a text field for the URL.
  - Pasting, or Return in the field, posts the URL.
  - If the pasted text isn't a URL with a `code` parameter, it shows "That doesn't look like the sign-in link." and nothing is posted.
- **Success:** the sheet dismisses and the MCP servers sheet applies the returned list. The server shows `connected`, or `pending` and then connected.
- **Failure:** the sheet stays open in its current state and shows Claude's error in danger caption, so the user can retry the paste or reload the web view.
- **Cancel** dismisses the sheet. The abandoned sign-in times out inside Claude on its own.

### Global screen (Settings → MCP servers)

- **Entry:** a new Settings section with one row, **MCP servers** (`puzzlepiece.extension`), with a disclosure chevron. It pushes the list screen. Shown only to the laptop owner; when `GET /mcp` answers 403 the row is hidden.
- **List screen:** title "MCP servers", with a ＋ button in the toolbar that opens the add form. It loads `GET /mcp` on appear, and pull to refresh reloads it.

```
┌──────────────────────────────────────┐
│ ‹ Settings     MCP servers         ＋ │
│ ┌──────────────────────────────────┐ │
│ │ MCP config changed. 3 sessions   │ │  ← shown when staleSessions > 0
│ │ use the old config.     [Apply]  │ │
│ └──────────────────────────────────┘ │
│ USER                                 │
│ ● playwright      6/6 · npx @play… › │
│ ● bitwarden       5/6 ⚠︎             › │
│ ● searxng         off in 1         › │
│ PLUGINS                              │
│ ● Notion          needs sign-in    › │
│ ┌──────────────────────────────────┐ │
│ │ Restart all sessions             │ │
│ └──────────────────────────────────┘ │
│ Restarts Claude in every session to  │
│ reload MCP servers and config.       │
│ Working sessions restart when their  │
│ turn ends.                           │
└──────────────────────────────────────┘
```

- **Row:**
  - Dot: success if every reporting session is connected; danger if any failed; link blue if any needs-auth; clay if any pending; none if there are no reports.
  - The name.
  - A muted detail:
    - `<connected>/<reporting>` when every session is connected
    - `<connected>/<reporting> ⚠︎` when some failed
    - `needs sign-in` when any needs-auth
    - `off in <n>` when disabled in n sessions
    - `no running sessions` when there are no reports
  - User rows also show the summary, truncated.
  - Tapping a row pushes the detail screen.
- **Stale banner:** when `staleSessions > 0`, a raised card: "MCP config changed. <n> session(s) use the old config." with an **Apply** button. Apply does the same as Restart all sessions, without the confirmation.
- **Restart all sessions:** opens a confirmation, "Restart Claude in <sessionCount> sessions?", with **Restart** and Cancel.
  - Restart calls `POST /mcp/restart-all` and shows a toast: "Restarting <restarting> now, <afterTurn> after their turn".
  - It then reloads the list after 5 s.
- **Detail screen:** titled with the server's name.
  - Config (user servers): type; command and args, or URL; env or headers as `KEY  ••••••••` rows, each with an eye button that reveals the value.
  - Plugin servers show "Provided by a plugin" instead of the config.
  - **Sessions** section: one row per reporting session with its name, status word and error (danger caption). Tapping a row opens that session.
  - **Sign in** (link-blue capsule) when any session reports `needs-auth`. It runs the sign-in sheet against the first such session.
  - **Edit** in the toolbar, shown when `editable`.
  - **Delete server**: destructive, user servers only. A confirmation reads "Delete <name>? Sessions keep it until they restart." Delete calls `DELETE`, pops back to the list, and shows the stale banner.
  - A non-editable user server shows its config read-only, with Delete only.
- **Add/Edit form** (sheet, "Add MCP server" / "Edit <name>", with Cancel and Save):
  - **Name:** add only. Validated live with the server's pattern; an invalid name shows "Letters, numbers, dot, dash and underscore only."
  - **Type:** a segmented picker: stdio, http, sse.
  - **stdio:**
    - Command: required, no autocorrect or autocapitalization.
    - Arguments: one row per argument, a ＋ row to add, swipe to delete, drag to reorder.
    - Environment: key/value rows. The value is a masked field with an eye toggle. Add with ＋, swipe to delete.
  - **http/sse:**
    - URL: required, must be `http(s)://`.
    - Headers: key/value rows, masked like env.
  - Switching type keeps the values entered for the other type until Save. Only the selected type's fields are sent.
  - Save is disabled until the required fields are valid, and shows a spinner while the request runs.
  - Success dismisses the form and applies the returned list, so the stale banner appears.
  - An error keeps the form open and shows the server's error in danger caption above Save.

## Error handling

- Every MCP call has a timeout. The app shows the server's `error` text, or "Couldn't reach the server" on a network failure. Nothing retries silently.
- If a process restarts while a sign-in is open, its callback is rejected with Claude's error. The sheet shows it, and the user taps Sign in again.
- Config writes go only through the Claude CLI. A partial edit (remove succeeded, add failed) restores the old config before answering.
- Older app builds ignore the new endpoints. A 404 from any MCP endpoint (an older server, or a k8s session) shows "MCP management isn't available for this session." in the sheet, or the same message on the global screen.

## Scope

In: everything above, for managed sessions on this server.

Out: k8s sessions; editing project or plugin servers; renaming servers; a JSON editor; signing out of a server; the web SPA.
