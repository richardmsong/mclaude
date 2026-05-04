## Run: 2026-05-02T00:00:00Z

**Gaps found: 1**

1. **`Done()` vs `Updates()` — Decisions table names a non-existent watcher method** — The Decisions table row "Full e2e scope" says "drain the watcher until `Done()`". `Done()` is not a method on the `KeyWatcher` interface in `github.com/nats-io/nats.go@v1.50.0` (neither the old `nats.KeyWatcher` nor the jetstream `KeyWatcher`). The interface has only `Updates() <-chan KeyValueEntry` and `Stop() error`. The Test Cases step 6 correctly says "drain watcher entries until `watcher.Updates()` sends nil (initial values exhausted)" — but a developer reading only the Decisions table would write `watcher.Done()` and get a compile error.
   - **Doc**: Decisions table, "Full e2e scope" row: "drain the watcher until `Done()`"
   - **Code**: `KeyWatcher` interface at `github.com/nats-io/nats.go@v1.50.0/jetstream/kv.go:348` defines only `Updates() <-chan KeyValueEntry` and `Stop() error`. Same for old API at `kv.go:118`. No `Done()` method exists on either interface.

---

**Round-5 gap verification:**

**Gap 1 (In-memory store / `cliDeviceCodeEntry.PublicKey`)**: Resolved. The ADR correctly describes adding `PublicKey string` to `cliDeviceCodeEntry` (no DB migration, device codes are in `globalCLIDeviceCodeStore`). The description of the fallback logic (`entry.PublicKey` → `user.NKeyPublic` → HTTP 400) is unambiguous. The code has not yet added this field (expected — ADR is draft), but the spec is sufficient for implementation.

**Gap 2 (`AdminUserResponse.Slug`)**: Resolved. The ADR correctly specifies adding `Slug string \`json:"slug"\`` to `AdminUserResponse` and populating it from `user.Slug` in `adminCreateUser`. `CreateUser` returns `*User` which carries `Slug` (confirmed at `mclaude-control-plane/db.go:26`). The code has not yet added the field (expected — ADR is draft), but the spec is unambiguous. Note: `adminListUsers` continues to return `{id, email, name}` without slug — this is consistent with the ADR which only requires slug on the create response.

**Gap 3 (KV watcher key format)**: Resolved and the approach is technically correct. `kv.Watch("hosts.{hslug}.projects.{pslug}.sessions.>")` passes `searchKeyValid` in both the old and jetstream NATS Go v1.50.0 APIs (regex `^[-/_=\.a-zA-Z0-9*]*[>]?$` at `jetstream/kv.go:503` accepts `>` at end). The key `$KV.mclaude-sessions-{uslug}.hosts.{hslug}.projects.{pslug}.sessions.>` (after the KV prefix is prepended) is within the user JWT's allowed `$KV.mclaude-sessions-{uslug}.hosts.>` subject permission. Draining via `watcher.Updates()` until nil is the correct pattern (confirmed from `jetstream/kv.go:1382`).
