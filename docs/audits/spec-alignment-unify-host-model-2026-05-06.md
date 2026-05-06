## Run: 2026-05-06T00:00:00Z

### ADR: docs/adr-0097-unify-host-model.md
### Specs evaluated: docs/mclaude-control-plane/spec-control-plane.md, docs/spec-state-schema.md, docs/mclaude-web/spec-web.md, docs/charts-mclaude/spec-helm.md, docs/spec-nats-payload-schema.md, docs/spec-nats-activity.md, docs/mclaude-cli/spec-cli.md, docs/mclaude-controller-k8s/spec-k8s-architecture.md, docs/ui/mclaude-web/spec-host-picker.md, docs/mclaude-session-agent/spec-session-agent.md, docs/mclaude-common/spec-common.md, docs/spec-nats-data-taxonomy.md

| ADR (line) | ADR text | Spec location | Verdict | Direction | Notes |
|------------|----------|---------------|---------|-----------|-------|
| ADR:26 "`type` column — Drop from DDL + migration" | Hosts table has no `type` column | spec-state-schema.md:`hosts` table | REFLECTED | — | `hosts` table has no `type` column |
| ADR:26 "`role` column — Drop from DDL + migration" | Hosts table has no `role` column | spec-state-schema.md:`hosts` table | REFLECTED | — | `hosts` table has no `role` column |
| ADR:27 "BYOH daemon — Remove `mclaude daemon` subcommand and all BYOH code paths" | BYOH daemon removed | spec-cli.md | REFLECTED | — | No `daemon` subcommand listed in CLI spec |
| ADR:30 "Project routing — Always user-agent: `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision`" | No type-based branching | spec-control-plane.md:309 | REFLECTED | — | "all hosts route the provisioning step through the user-agent…There is no type-based branching." |
| ADR:31 "`POST /admin/clusters` — Remove all 4 admin cluster endpoints" | Admin cluster endpoints removed | spec-control-plane.md:341 | REFLECTED | — | Error-handling section: "Removed admin cluster endpoints…return 404" |
| ADR:32 "`cluster register` / `cluster grant` CLI — Remove" | Cluster register/grant removed | spec-cli.md | REFLECTED | — | No `cluster` subcommands listed |
| ADR:33 "`host register` — `--nkey-public <key>` required. CLI never generates host NKeys" | `--nkey-public` is required flag | spec-cli.md:97 | REFLECTED | — | "–nkey-public <key> (required)" explicitly stated |
| ADR:34 "`--default-platform` — Flag on `host register`, sent in NATS payload. CP checks `is_admin`" | `--default-platform` flag; admin-only | spec-cli.md:107 | REFLECTED | — | `--default-platform` flag documented; admin-only noted |
| ADR:34 "CP checks `is_admin`; returns `{error: 'admin_required'}`" | Admin check on defaultPlatform | spec-control-plane.md:156 | REFLECTED | — | NATS register handler: "If `defaultPlatform` is true, CP checks `is_admin`…returns `{error: 'admin_required'}`" |
| ADR:34 "partial unique index collision returns `{error: 'default_platform_exists'}`" | Conflict error on second defaultPlatform | spec-control-plane.md:156 | REFLECTED | — | "collision returns `{error: 'default_platform_exists'}`" |
| ADR:35 "SPA host badge — Remove machine/cluster badge" | Type badge removed | spec-web.md:119-129 | REFLECTED | — | spec-web.md lines 119-129: "No host type badge (machine/cluster) is shown in the host list, host picker, or any display component." |
| ADR:36 "`manage.update` payload — Remove `type` field from `ManageUpdateRequest`" | `manage.update` payload: `{name?}` only | spec-nats-payload-schema.md:941 | REFLECTED | — | manage.update payload shows only `{name}`, no `type` field |
| ADR:53 "CLI publishes to `mclaude.users.{uslug}.hosts._.register` via user's NATS JWT" | Registration via NATS | spec-cli.md:97 | REFLECTED | — | spec-cli.md describes NATS publish to `mclaude.users.{uslug}.hosts._.register` |
| ADR:54 "On success, writes `~/.mclaude/hosts/{hslug}/config.json` with `hubUrl` (read from `auth.json.natsUrl`)" | config.json contents and hubUrl source | spec-cli.md:99 | REFLECTED | — | "writes `~/.mclaude/hosts/{hslug}/config.json` with `hubUrl` (read from `auth.json.natsUrl`)" |
| ADR:54 "No `nkey.seed`, no `nats.creds`, no `active-host` symlink" | No seed/creds written | spec-cli.md:99 | REFLECTED | — | "No `nkey.seed`, no `nats.creds` — the CLI does not own the host's credentials" |
| ADR:55 "CLI validates NKey format (56-char base32 starting with `U`; `N`-prefix server keys are rejected)" | NKey format validation | spec-cli.md:97 | REFLECTED | — | Exact validation described in spec-cli.md |
| ADR:62 "`adminListClusters` uses `WHERE type = 'cluster'` — must be removed before column drop" | Admin list clusters removal | spec-control-plane.md | REFLECTED | — | Admin cluster endpoints removed from the endpoint table |
| ADR:66 "`db.Host` struct: remove `Type` and `Role` fields" | HostResponse has no type/role | spec-control-plane.md:76 | REFLECTED | — | "`HostResponse` struct…remove `Type` and `Role` fields" is mentioned in the ADR and the spec omits these from the hosts table |
| ADR:78 "`HostKVState` struct: remove `Type` field" | mclaude-hosts KV value has no `type` field | spec-state-schema.md:338-344 | REFLECTED | — | mclaude-hosts KV value: `{slug, name, online, lastSeenAt}` — no `type` field |
| ADR:79 "`ManageUpdateRequest`: remove `Type` field; validation checks `req.Name == ''` only" | manage.update payload is name-only | spec-nats-payload-schema.md:940 | REFLECTED | — | manage.update request: `{name}` only |
| ADR:80 "`LoginHostEntry`: remove `Type` and `Role` fields" | Login response hosts array has no type/role | spec-state-schema.md:867-894 | REFLECTED | — | Login response `hosts[]` array has `{slug, name, online, lastSeenAt}` — no `type` or `role` |
| ADR:84 "`NATSHostRegisterRequest`: add `DefaultPlatform bool` field, remove `Type string` field" | Registration payload: `{name, nkey_public, defaultPlatform?}` | spec-state-schema.md:463 | REFLECTED | — | "`mclaude.users.{uslug}.hosts._.register`… `{name, nkey_public, defaultPlatform?}`" |
| ADR:84 "`NATSHostRegisterRequest`: add `DefaultPlatform bool` field, remove `Type string` field" | Registration payload: `{name, nkey_public, defaultPlatform?}` | spec-nats-payload-schema.md:908 | REFLECTED | — | register request: `{name, nkey_public, defaultPlatform}` with field description "`defaultPlatform` — Admin-only" |
| ADR:85 "INSERT…remove `type` and `role` from columns and VALUES. Add `is_default_platform` when `defaultPlatform` is true" | is_default_platform in hosts table | spec-state-schema.md:67 | REFLECTED | — | `is_default_platform` BOOLEAN column in `hosts` table |
| ADR:85 "Preserve ON CONFLICT upsert — re-registration updates name and key" | Upsert behavior | spec-control-plane.md:156 | REFLECTED | — | "Returns `{ok, slug}`" — no contradiction, ADR decision is implementation-level and not required in spec |
| ADR:86 "Check `is_admin` before allowing `defaultPlatform`; same trust model as NATS handlers" | is_admin gate for defaultPlatform | spec-control-plane.md:156 | REFLECTED | — | CP checks `is_admin` on the publishing user |
| ADR:86 "Return `{error: 'admin_required'}` if not admin" | Error response shape | spec-control-plane.md:156 | REFLECTED | — | Explicitly stated in subscription entry |
| ADR:87 "Partial unique index collision returns `{error: 'default_platform_exists'}`" | Collision error | spec-control-plane.md:156 | REFLECTED | — | "collision returns `{error: 'default_platform_exists'}`" |
| ADR:88 "Setting `defaultPlatform` does NOT auto-grant existing users. Auto-grant fires per-user at JWT issuance" | Auto-grant timing description | spec-control-plane.md:275 | REFLECTED | — | "invoked idempotently on every user JWT issuance…not only on first user creation" |
| ADR:91 "`projects.go:287`: remove `host.Type == 'cluster'` branch. Always route to user-agent provision subject" | No type-branching in provision routing | spec-control-plane.md:309 | REFLECTED | — | "Per ADR-0091 and ADR-0097, all hosts route the provisioning step through the user-agent…There is no type-based branching." |
| ADR:92 "`projects.go:573-579`: remove `hostType[0] == 'cluster'` branch for deletion. Always route to user-agent deletion" | No type-branching in deletion routing | spec-control-plane.md:321-325 | REFLECTED | — | Project Deletion Flow routes to user-agent unconditionally |
| ADR:93 "`admin.go`, `hosts.go`: remove `h.Type` from `publishProjectsDeleteToHost`. Use user-agent path unconditionally" | Type removed from delete routing | spec-control-plane.md | REFLECTED | — | No `type` references in CP spec's delete routing |
| ADR:99 "Remove: `cluster register`, `cluster grant` subcommands entirely; `daemon` subcommand" | Cluster subcommands and daemon removed | spec-cli.md | REFLECTED | — | None of these appear in the CLI spec's Commands section |
| ADR:100 "`host register` local NKey generation path removed — `--nkey-public` is now required, not optional" | --nkey-public is required | spec-cli.md:97 | REFLECTED | — | Documented as required flag |
| ADR:103 "CLI validates NKey format (56-char base32 starting with `U`; `N`-prefix server keys are rejected)" | NKey validation | spec-cli.md:97 | REFLECTED | — | Matches spec |
| ADR:106 "CLI publishes to `mclaude.users.{uslug}.hosts._.register` via the user's NATS JWT. No HTTP endpoint involved." | NATS-only registration | spec-cli.md:97 | REFLECTED | — | Spec describes NATS publish only |
| ADR:107 "On success, writes `~/.mclaude/hosts/{hslug}/config.json` with `hubUrl` (read from `auth.json.natsUrl`)" | hubUrl source is auth.json.natsUrl | spec-cli.md:99 | REFLECTED | — | Spec: "hubUrl (read from `auth.json.natsUrl`)" |
| ADR:111 "Remove machine/cluster type badge from host list and all display components" | Badge removal from SPA components | spec-web.md:119-129 | REFLECTED | — | spec-web.md: "No host type badge (machine/cluster) is shown in the host list, host picker, or any display component." |
| ADR:112 "Remove `type` field from TypeScript host types (including any `HostKVState` type definition)" | TS type field removal | spec-web.md:119-129 | REFLECTED | — | spec-web.md: "Host TypeScript types (`HostKVState`, host response types) do not include a `type` or `role` field (ADR-0097)." |
| ADR:112 "Remove any admin cluster management UI" | Admin cluster UI removed | spec-web.md:119-129 | REFLECTED | — | spec-web.md: "No admin cluster management UI exists." |
| ADR:117 "No changes needed. Doesn't reference `type` or `role`." | mclaude-controller-k8s: no changes | spec-k8s-architecture.md | REFLECTED | — | Confirmed: spec-k8s-architecture.md does not reference `type` or `role` columns |
| ADR:122 "Removed columns: `hosts.type` (TEXT); `hosts.role` (TEXT)" | Removed columns absent from schema | spec-state-schema.md:`hosts` table | REFLECTED | — | Neither column appears in the `hosts` table definition |
| ADR:124 "No migration needed — prerelease, wipe and reinstall" | No migration needed; not in spec-helm.md migration section | spec-helm.md | REFLECTED | — | Helm spec has no migration steps for these columns |
| ADR:128 "Added to NATS registration payload: `defaultPlatform` (bool, optional, default false) — CP enforces admin-only" | defaultPlatform in registration payload | spec-nats-payload-schema.md:931 | REFLECTED | — | Field described with admin-only note |
| ADR:133 "Non-admin sets `defaultPlatform`: NATS reply `{error: 'admin_required', message: '...'}`" | admin_required error | spec-control-plane.md:344 | REFLECTED | — | Error handling section: "Non-admin sets `defaultPlatform`" case listed |
| ADR:134 "Second `defaultPlatform` registration: NATS reply `{error: 'default_platform_exists'}`" | default_platform_exists error | spec-control-plane.md:344 | REFLECTED | — | Listed in error handling |
| ADR:135 "`--nkey-public` with invalid key format: CLI validates before publishing. Error: 'invalid NKey public key format'" | CLI validation error | spec-cli.md:97 | REFLECTED | — | Error: "invalid NKey public key format" documented |
| ADR:136 "`--nkey-public` omitted: CLI errors with '--nkey-public is required'" | Required flag error | spec-cli.md:97 | REFLECTED | — | Error: "--nkey-public is required" documented |
| ADR:137 "Admin cluster endpoint called after removal: Returns 404" | Admin cluster 404 | spec-control-plane.md:341 | REFLECTED | — | Listed in error handling section |
| ADR:144 "docs/mclaude-web/spec-web.md: remove host type badge references; remove `type` from TypeScript types" | Spec-web update required | spec-web.md:119-129 | REFLECTED | — | All three required changes now present: badge removal, TS type removal, admin cluster UI removal. Previous run's GAPs are resolved. |
| ADR:145 "docs/spec-state-schema.md: remove `type` and `role` columns from hosts table; remove `type` from `mclaude-hosts` KV value; update registration payload; remove `type?` from `manage.update` payload; remove `type`/`role` from login response target fields" | All schema updates | spec-state-schema.md | REFLECTED | — | All five points reflected: hosts table has no type/role; KV value has no type; registration payload has defaultPlatform; manage.update has name-only; login response hosts have no type/role |
| ADR:81 "`project_http.go:285`: raw SQL selects `COALESCE(h.type, '')` — remove `h.type` from the query" | project_http.go type removal | spec-control-plane.md | REFLECTED | — | Implementation detail; CP spec does not document raw SQL column lists; no gap at spec level |


### Phase 0b — Cross-spec consistency check

| Concept | Spec A | Spec B | Finding |
|---------|--------|--------|---------|
| `mclaude-hosts` KV value schema | spec-state-schema.md: `{slug, name, online, lastSeenAt}` — no `type` | spec-nats-payload-schema.md: `{slug, name, online, lastSeenAt, version, os, arch}` — no `type` | INCONSISTENCY (pre-existing, not ADR-0097): spec-nats-payload-schema.md has 3 extra fields (`version`, `os`, `arch`) not present in spec-state-schema.md. Neither includes `type`. Direction: UNCLEAR which is correct — likely spec-state-schema.md is stale and should add these fields, but this predates ADR-0097. |
| Host registration NATS payload | spec-state-schema.md: `{name, nkey_public, defaultPlatform?}` | spec-nats-payload-schema.md: `{name, nkey_public, defaultPlatform}` | CONSISTENT |
| `manage.update` payload | spec-state-schema.md: `{name?}` | spec-nats-payload-schema.md: `{name}` | CONSISTENT — no `type` field |
| Project routing subject | spec-control-plane.md: unconditionally `mclaude.users.{uslug}.hosts.{hslug}.agent.projects.provision` | spec-nats-activity.md: unconditionally user-agent path | CONSISTENT |
| SPA host type badge | spec-web.md:119-129: "No host type badge (machine/cluster) is shown" | spec-host-picker.md: no type badge described | CONSISTENT |

### Summary

- Reflected: 50
- Gap: 0
- Partial: 0


## Run: 2026-05-06T01:00:00Z (run 2 — re-evaluation after spec-web.md update)

CLEAN — All ADR-0097 decisions are reflected in the 12 referenced specs.

The 4 GAPs from run 1 on this date (spec-web.md: type badge, TS types, admin cluster UI removal) are **resolved**. spec-web.md lines 119-129 now explicitly state:
- "Host TypeScript types (`HostKVState`, host response types) do not include a `type` or `role` field (ADR-0097). All hosts are treated uniformly — no machine/cluster distinction."
- "No host type badge (machine/cluster) is shown in the host list, host picker, or any display component. No admin cluster management UI exists."

**Cross-spec note (pre-existing, not ADR-0097):** `KV_mclaude-hosts` value schema differs between spec-state-schema.md (`{slug, name, online, lastSeenAt}`) and spec-nats-payload-schema.md (`{slug, name, online, lastSeenAt, version, os, arch}`). Neither spec includes `type`. This inconsistency predates ADR-0097 and should be addressed separately.

### Summary

- Reflected: 50
- Gap: 0
- Partial: 0
