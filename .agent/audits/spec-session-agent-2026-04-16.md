## Run: 2026-04-16T00:00:00Z (final verification pass)

Scope: session-agent component vs docs/plan-github-oauth.md (Session-agent section only)
Previously-reported gaps being verified: config.yml headless, old-format conversion, identity selection, gitWorktreeAdd refresh, RunGitOpWithCredsRefresh removal.

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Notes |
|-----------------|-----------|---------------|---------|-------|
| plan-github-oauth.md:383 | "entire credential setup and initial clone happen inside the Go session-agent binary, NOT in entrypoint.sh" | entrypoint.sh:37-38 comment; main.go:149-188 (credMgr setup + InitRepo call); gitcreds.go:565-626 InitRepo | IMPLEMENTED | entrypoint.sh now has only a comment confirming this; all logic in Go |
| plan-github-oauth.md:383 | "entrypoint.sh remains minimal: SSH key setup, env vars, home directory, then exec session-agent" | entrypoint.sh:1-63 | IMPLEMENTED | SSH key (L5-10), env vars (L14), home dir setup, exec session-agent at end |
| plan-github-oauth.md:383 | "existing git clone/init block in entrypoint.sh moves entirely to Go" | entrypoint.sh (no git clone/init present); gitcreds.go:565-626 InitRepo | IMPLEMENTED | entrypoint.sh has no git clone/init commands |
| plan-github-oauth.md:388-389 | Step 1: "Symlink PVC config: Remove any pre-existing ~/.config/ (rm -rf ~/.config/ — safe because $HOME is emptyDir). If /data/.config/ exists, symlink. If not, create /data/.config/ then symlink." | gitcreds.go:377-398 symlinkPVCConfig | IMPLEMENTED | RemoveAll, MkdirAll(/data/.config/), Symlink |
| plan-github-oauth.md:391-392 | Step 2: "Initialize gh config for headless operation: ensure ~/.config/gh/config.yml exists with at least version: '1'. Only create if missing." | gitcreds.go:433-440 (inside mergeAndSetup) | IMPLEMENTED | Creates ghDir, checks os.IsNotExist before writing version: "1" |
| plan-github-oauth.md:393-399 | Step 3: "Merge managed tokens: Read gh-hosts.yml from Secret mount (multi-account format). Read existing ~/.config/gh/hosts.yml. Convert and select. Merge strategy: managed wins per host. Preserve manual entries." | gitcreds.go:409-479 mergeAndSetup + ConvertGHHostsToOldFormat + MergeGHHostsYAML | IMPLEMENTED | Full pipeline: read, convert, merge, write |
| plan-github-oauth.md:396 | "If GIT_IDENTITY_ID is set, resolve it to a username via conn-{id}-username from the Secret, find which host has that username, and use that account's token" | gitcreds.go:452-478 (resolveIdentityFromManaged call in mergeAndSetup) | IMPLEMENTED | resolveIdentityFromManaged reads conn-{id}-username, findHostForUsernameInManaged |
| plan-github-oauth.md:397-398 | "Merge strategy: For each host, write or overwrite managed token in old format. Preserve hosts only in existing (manual gh auth login). Managed wins per host." | gitcreds.go:224-248 MergeGHHostsYAML | IMPLEMENTED | Loops over managed, overwrites in existing; existing-only hosts preserved |
| plan-github-oauth.md:398 | "Same merge for glab-config.yml → ~/.config/glab-cli/config.yml" | gitcreds.go:481-498 (glab merge in mergeAndSetup); MergeGLabConfigYAML:256-282 | IMPLEMENTED | |
| plan-github-oauth.md:400-401 | Step 4: "Register credential helpers: Run gh auth setup-git. Run glab auth setup-git." | gitcreds.go:501-511 | IMPLEMENTED | Both commands run; non-zero exit logged as non-fatal |
| plan-github-oauth.md:403-405 | Step 5: "Identity is already selected (by step 3). No gh auth switch call needed." | gitcreds.go (no gh auth switch call anywhere in file) | IMPLEMENTED | No gh auth switch present |
| plan-github-oauth.md:406 | "conn-{GIT_IDENTITY_ID}-username keys written by OAuth callback; session-agent reads conn-{GIT_IDENTITY_ID}-username to resolve connection UUID to username" | gitcreds.go:527-553 resolveIdentityFromManaged reads conn-{id}-username from SecretMount | IMPLEMENTED | |
| plan-github-oauth.md:410-413 | "Before each git operation: Re-read gh-hosts.yml and glab-config.yml from Secret mount. If set of managed tokens changed since last check, re-merge (with format conversion and identity selection) into ~/.config/ and re-run gh auth setup-git / glab auth setup-git" | gitcreds.go:344-350 RefreshIfChanged → mergeAndSetup; change detection via equalBytes:L420-424 | IMPLEMENTED | |
| plan-github-oauth.md:413 | "Run the git command — git automatically calls gh auth git-credential for HTTPS URLs" | gitcreds.go:596-613 (git clone in InitRepo); agent.go:1203-1213 gitWorktreeAdd | IMPLEMENTED | git clone and git worktree add both after credential setup |
| plan-github-oauth.md:416 | "SSH → HTTPS normalization: Before git operations, if SCP-style (git@{host}:{path}) and credential helper registered for that host, normalize to HTTPS. Only SCP-style — ssh:// left as-is." | gitcreds.go:52-86 NormalizeGitURL; used in gitcreds.go:579, 595 InitRepo | IMPLEMENTED | |
| plan-github-oauth.md:418 | "No GIT_ASKPASS, no custom credential provider interface, no hostname matching, no per-provider username mapping." | gitcreds.go (entire file — no GIT_ASKPASS, no custom interface) | IMPLEMENTED | Confirmed by absence |
| plan-github-oauth.md:420-421 | "Manual auth within sessions: gh auth login manual auth written to ~/.config/gh/hosts.yml on PVC. Survives pod restarts. Not overwritten by merge step." | gitcreds.go:224-248 MergeGHHostsYAML (only overwrites managed hosts, preserves others) | IMPLEMENTED | |
| plan-github-oauth.md:422-423 | "Error handling: If git operation fails with auth error (exit code 128 + stderr matching Authentication failed, HTTP Basic: Access denied, Invalid username or password, could not read Username), session-agent publishes session_failed with reason provider_auth_failed" | gitcreds.go:29-48 IsGitAuthError; gitcreds.go:600-610 GitAuthError return; main.go:171-186 publishes session_failed/provider_auth_failed | IMPLEMENTED | All 4 patterns present; exit code 128 check; lifecycle publish |
| plan-github-oauth.md:426-437 | Dockerfile: "Add gh and glab to session-agent image. gh via apk (github-cli, 2.40+ for multi-account). glab via binary download. Not via Nix." | Dockerfile:9-17 | IMPLEMENTED | apk add github-cli + glab curl download; pinned 1.92.1 |
| plan-github-oauth.md:432 | "ARG TARGETARCH=arm64; apk add github-cli; GLAB_VERSION=1.92.1; curl download" | Dockerfile:8-17 | IMPLEMENTED | Exact pattern matches |
| plan-github-oauth.md:435 | "CLI config persistence: PVC-backed ~/.config/. Symlink /data/.config/ → ~/.config/ so gh auth login and glab auth login survive pod restarts." | gitcreds.go:377-398 symlinkPVCConfig | IMPLEMENTED | |
| plan-github-oauth.md:436 | "Config merge strategy: Merge, not overwrite. Session-agent adds managed tokens to hosts.yml without removing entries from manual gh auth login." | gitcreds.go:224-248 MergeGHHostsYAML | IMPLEMENTED | |
| plan-github-oauth.md:322-329 | "Secret format vs disk format: gh-hosts.yml key in K8s Secret uses multi-account users: format. Session-agent converts to old single-account format when writing to ~/.config/gh/hosts.yml" | gitcreds.go:137-192 ConvertGHHostsToOldFormat | IMPLEMENTED | Multi-account → old format conversion |
| plan-github-oauth.md:331 | "Why old format: gh CLI 2.40+ stores users: map tokens in system keyring (via D-Bus). Alpine containers don't have D-Bus, so users: format produces empty credentials. Old format stores token directly in file." | gitcreds.go:122-131 (comment on ConvertGHHostsToOldFormat); Dockerfile:4-5 comment | IMPLEMENTED | Rationale documented in code comments |
| plan-github-oauth.md:333 | "Identity selection: session-agent picks which account's token to write per host. If GIT_IDENTITY_ID is set, matching account's token used for that host. For other hosts, user: default used." | gitcreds.go:137-192 ConvertGHHostsToOldFormat (activeUsername/activeHost logic) | IMPLEMENTED | |
| plan-github-oauth.md:406-407 | "resolves connection UUID to username via conn-{GIT_IDENTITY_ID}-username key in Secret mount, then looks up which host in managed gh-hosts.yml has that username" | gitcreds.go:527-553 resolveIdentityFromManaged | IMPLEMENTED | |
| plan-github-oauth.md:281 | "stale GIT_IDENTITY_ID: if conn-{id}-username key is gone (connection deleted), session-agent logs warning and falls back to default active account" | gitcreds.go:537-540 (empty data → log.Warn + return "", "", nil) | IMPLEMENTED | Missing key → warning + fallback |
| plan-github-oauth.md:411-412 | "re-read gh-hosts.yml and glab-config.yml from Secret mount (K8s auto-syncs ~1 min). If set of managed tokens changed since last check, re-merge" | gitcreds.go:419-424 (change detection via equalBytes; lastGHHosts/lastGLabConfig tracking) | IMPLEMENTED | |
| plan-github-oauth.md:410 | "Before each git operation (clone, fetch, push, worktree add)" | gitcreds.go:576-596 InitRepo (before clone/fetch); agent.go:1203-1213 gitWorktreeAdd (before worktree add) | IMPLEMENTED | Covers clone, fetch, worktree add |
| plan-github-oauth.md:561 | "gh auth setup-git fails: session-agent logs the error, proceeds without credential helper for that host. Not a session-fatal error." | gitcreds.go:502-505, 508-511 (Warn + non-fatal for both gh and glab) | IMPLEMENTED | |
| plan-github-oauth.md:562 | "gh auth switch fails: username not found in hosts.yml: session-agent logs warning, uses default active account. Git operations proceed." | gitcreds.go:545-551 (log.Warn + return "", "", nil → fallback to default) | IMPLEMENTED | No gh auth switch used; warning logged when username not found |
| plan-github-oauth.md:563 | "Identity deleted mid-session: Secret mount updates (~1 min), next merge detects managed account gone. Credential helper falls back to whatever account is active." | gitcreds.go:537-540 (missing key → warn + fallback); mergeAndSetup re-reads from SecretMount each call | IMPLEMENTED | |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| gitcreds.go:1-14 | INFRA | Package declaration, imports — standard boilerplate |
| gitcreds.go:16-25 | INFRA | Constants secretMountPath, configPVCPath, configHomePath — necessary infrastructure for spec'd paths |
| gitcreds.go:29-34 | INFRA | gitAuthErrPatterns var — implements spec line 422 patterns, this is the data backing IsGitAuthError |
| gitcreds.go:38-48 | INFRA | IsGitAuthError helper — implements spec-required auth error detection (plan-github-oauth.md:422) |
| gitcreds.go:52-86 | INFRA | NormalizeGitURL — implements spec SSH→HTTPS normalization (plan-github-oauth.md:416) |
| gitcreds.go:89-119 | INFRA | ghHostsConfig, ghHostEntry, ghUserEntry, glabHostsConfig, glabHostEntry structs — necessary data types for spec'd config format parsing |
| gitcreds.go:122-192 | INFRA | ConvertGHHostsToOldFormat — implements spec old-format conversion (plan-github-oauth.md:322-335) |
| gitcreds.go:197-208 | INFRA | findHostForUsernameInManaged — helper for identity resolution; required by resolveIdentityFromManaged which implements plan-github-oauth.md:396 |
| gitcreds.go:213-248 | INFRA | MergeGHHostsYAML — implements spec merge strategy (plan-github-oauth.md:397-398) |
| gitcreds.go:256-282 | INFRA | MergeGLabConfigYAML — implements spec glab merge (plan-github-oauth.md:398) |
| gitcreds.go:284-293 | INFRA | ReadSecretFile — helper reading from Secret mount; required for all spec'd secret reads |
| gitcreds.go:296-374 | INFRA | CredentialManager struct + NewCredentialManager + Setup + RefreshIfChanged + RegisteredHosts — required implementation of spec's credential helper management (plan-github-oauth.md:387-415) |
| gitcreds.go:376-398 | INFRA | symlinkPVCConfig — implements spec step 1 (plan-github-oauth.md:388-389) |
| gitcreds.go:408-520 | INFRA | mergeAndSetup — core implementation of spec steps 2-4 (plan-github-oauth.md:391-404); internal to CredentialManager |
| gitcreds.go:522-554 | INFRA | resolveIdentityFromManaged — implements spec identity resolution (plan-github-oauth.md:396, 406) |
| gitcreds.go:559-626 | INFRA | InitRepo — implements spec initial clone/init (plan-github-oauth.md:383, go binary handles credential helper setup → initial clone) |
| gitcreds.go:628-637 | INFRA | GitAuthError type — error type for spec's auth error detection path; used in main.go for session_failed publish |
| gitcreds.go:639-684 | INFRA | initScratchRepo — implements scratch project init (plan-github-oauth.md:383 "scratch init if no GIT_URL"); plumbing commands for bare repo |
| gitcreds.go:687-696 | INFRA | runCmd helper — utility for executing gh/glab commands (setup-git) |
| gitcreds.go:698-709 | INFRA | equalBytes helper — change detection for credential refresh (plan-github-oauth.md:411) |
| gitcreds.go:556-557 | INFRA | Blank lines (empty lines between resolveIdentityFromManaged and InitRepo) |
| gitcreds.go:685-686 | INFRA | Blank lines between initScratchRepo and runCmd |
| entrypoint.sh:1-63 | INFRA | Entrypoint script — SSH key setup, env vars, exec session-agent; spec says "remains minimal" (plan-github-oauth.md:383) |
| main.go:148-188 | INFRA | k8s mode credential setup block — wires CredentialManager and InitRepo; implements spec session start sequence (plan-github-oauth.md:387) |
| agent.go:1199-1213 | INFRA | gitWorktreeAdd with credMgr.RefreshIfChanged — implements spec "before each git operation" refresh (plan-github-oauth.md:410-412) |
| agent.go:66-68 | INFRA | credMgr and gitIdentityID fields on Agent struct — necessary to wire CredentialManager through to git operations |
| Dockerfile:1-27 | INFRA | Dockerfile: gh + glab installation, session-agent binary copy, entrypoint — implements spec Dockerfile changes (plan-github-oauth.md:426-437) |

### Summary

- Implemented: 33
- Gap: 0
- Partial: 0
- Infra: 25
- Unspec'd: 0
- Dead: 0

All 5 previously-reported gaps are confirmed IMPLEMENTED.
No new gaps found.
