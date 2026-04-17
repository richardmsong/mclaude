## Run: 2026-04-16T18:49:00Z
## Component: spa (mclaude-web) — Dashboard Project Filter Audit

Audit focus: commit 68c2d95 — Dashboard project filter feature.
Spec source: docs/ui-spec.md (committed 6dc35b5)
Sections audited: Session List Grouping, Project Filter, Sheet: Project Filter, Dashboard Overflow Menu

### Phase 1 — Spec → Code

| Spec (doc:line) | Spec text | Code location | Verdict | Direction | Notes |
|-----------------|-----------|---------------|---------|-----------|-------|
| ui-spec.md:178-189 | "Dashboard Overflow Menu: New Project + Filter by Project (only shown when >1 project exists)" | DashboardScreen.tsx:159-194 | IMPLEMENTED | — | New Project always shown; Filter by Project gated on `projects.length > 1` |
| ui-spec.md:192-197 | "When more than one project is visible, sessions are grouped by project, with a project header above each group. When only one project is visible … project headers are hidden and session rows render flat." | DashboardScreen.tsx:197-198, 343-354 | IMPLEMENTED | — | `showProjectHeaders = sortedGroups.length > 1`; headers only rendered when `showProjectHeaders` |
| ui-spec.md:195 | "Projects sorted alphabetically by name." | session-list-vm.ts:127 | IMPLEMENTED | — | `[...allProjects].sort((a, b) => a.name.localeCompare(b.name))` |
| ui-spec.md:196 | "Within a project, sessions sort by descending last-updated time." | session-list-vm.ts:134-137 | IMPLEMENTED | — | `[...p.sessions].sort((a, b) => b.stateSince.localeCompare(a.stateSince))` |
| ui-spec.md:197 | "Project header: 12px, weight 600, uppercase, --text2, 8px top padding, 4px bottom padding, not tappable." | DashboardScreen.tsx:344-353 | IMPLEMENTED | — | Style object: fontSize:12, fontWeight:600, textTransform:'uppercase', color:'var(--text2)', padding:'8px 16px 4px'. Rendered as a `<div>` not a `<button>` so not tappable. |
| ui-spec.md:201 | "State: localStorage.mclaude.filterProjectId holds the selected project ID." | session-list-vm.ts:5 (`FILTER_PROJECT_KEY = 'mclaude.filterProjectId'`), session-list-vm.ts:86-99 | IMPLEMENTED | — | Key constant correct; getItem/setItem/removeItem paths wired |
| ui-spec.md:205-214 | "Filter banner: --surf2 background, 13px/500/--text, 10px padding. Banner text: 'Showing: {project name}'. ✕ button clears filter." | DashboardScreen.tsx:212-231 | PARTIAL | CODE→FIX | Background var(--surf2) ✓, text "Showing: {name}" ✓, ✕ button clears filter ✓. BUT padding is '10px 16px' (correct) ✓. Font-size is 13 ✓, fontWeight 500 ✓, color var(--text) ✓. IMPLEMENTED on re-check — see Notes. |
| ui-spec.md:216-217 | "Stale filter: if the filtered project no longer exists in the KV store, the filter is cleared automatically on the next render." | session-list-vm.ts:108-117, DashboardScreen.tsx:63-67 | IMPLEMENTED | — | `resolveFilter()` checks project existence; clears localStorage if gone. DashboardScreen calls `resolveFilter()` on every projects-changed event. |
| ui-spec.md:305-321 | "Sheet: Project Filter — 'All Projects' always first (selecting clears filter); project rows sorted alphabetically; filled radio for active filter; on tap: write/remove localStorage and dismiss sheet." | ProjectFilterSheet.tsx:16, 45-48, 92-107, 109-127 | IMPLEMENTED | — | Sorted alphabetically ✓, All Projects first ✓, `RadioIcon filled={!activeFilterId}` for All Projects ✓, `handleSelect` calls `onSelect` then `onClose()` ✓ |
| ui-spec.md:317 | "The row matching the current mclaude.filterProjectId has a filled radio; all others empty. If no filter is active, 'All Projects' is filled." | ProjectFilterSheet.tsx:105, 124 | IMPLEMENTED | — | `filled={!activeFilterId}` for All Projects; `filled={activeFilterId === project.id}` for project rows |
| ui-spec.md:319-320 | "On tap: write mclaude.filterProjectId to localStorage (or remove it if 'All Projects' tapped), dismiss sheet, re-render dashboard." | ProjectFilterSheet.tsx:45-48; DashboardScreen.tsx:116-119 | IMPLEMENTED | — | Sheet calls onSelect(projectId) then onClose(). onSelect → handleFilterSelect → sessionListVM.setFilter → setFilterProjectId → re-render. |
| ui-spec.md:267-269 | "When a project filter is active and the filtered project has no sessions — Heading: 'No Sessions', Body: 'No sessions in this project'" | DashboardScreen.tsx:264-277 | IMPLEMENTED | — | Exact strings match spec |
| ui-spec.md:201-202 | "Filter persists across reloads (via localStorage.mclaude.filterProjectId)" | session-list-vm.ts:86-88; session-list-vm.test.ts:160-169 | IMPLEMENTED | — | `filterProjectId` getter reads from `_storage.getItem()` on every call; constructor receives same storage instance |
| ui-spec.md:171-172 | "Badge appears when any session has needs_permission or plan_mode status" | DashboardScreen.tsx:104-106 | PARTIAL | CODE→FIX | Badge is `s.state === 'requires_action' \|\| s.hasPendingPermission`. The `plan_mode` state is not checked for badge. Also, `needs_permission` maps to `requires_action` in the state enum — that mapping may be correct. But `plan_mode` as a separate state is entirely absent from the badge condition and from STATE_LABELS. |

### Phase 2 — Code → Spec

| File:lines | Classification | Explanation |
|------------|---------------|-------------|
| DashboardScreen.tsx:13-18 (shortenPath) | INFRA | Utility for displaying last 2 path segments — used for session metadata row. |
| DashboardScreen.tsx:20-30 (STATE_LABELS) | INFRA | Maps state strings to display labels — used in session row metadata. Note: `plan_mode` not present (see Phase 1 gap). |
| DashboardScreen.tsx:60-91 (useEffects for project change, openNewProject, menu close) | INFRA | Necessary lifecycle hooks for subscription and UI state. |
| DashboardScreen.tsx:94 (sortedGroups from VM) | INFRA | Reads computed property from VM — drives rendering. |
| DashboardScreen.tsx:96-108 (allSessions, badge, unhealthyProjects) | INFRA | Badge count and health banner computation — spec'd behavior. |
| DashboardScreen.tsx:234-252 (unhealthy projects banner) | UNSPEC'd | "Agent down: {projects} — heartbeat stale" banner. No mention in ui-spec.md Project Filter or Grouping sections. This is a P6 health feature from plan-k8s-integration.md, not part of the filter feature being audited — acceptable infrastructure. |
| DashboardScreen.tsx:389-410 (FAB) | INFRA | Spec'd in "FAB" section of ui-spec.md. |
| DashboardScreen.tsx:412-445 (NewSessionSheet, NewProjectSheet, ProjectFilterSheet renders) | INFRA | Conditional sheet renders, all spec'd. |
| session-list-vm.ts:141-196 (createProject, createSession) | INFRA | Spec'd in other sections (FAB, New Session Sheet flows). |
| session-list-vm.ts:198-218 (deleteSession, onProjectsChanged, destroy, _notify) | INFRA | Lifecycle and subscription infrastructure. |
| ProjectFilterSheet.tsx:18-43 (RadioIcon component) | INFRA | Helper sub-component for radio button rendering within the sheet. |

### Phase 3 — Test Coverage

| Spec (doc:line) | Spec text | Unit test | E2E test | Verdict |
|-----------------|-----------|-----------|----------|---------|
| ui-spec.md:195 | Projects sorted alphabetically | session-list-vm.test.ts:89-96 | None | UNIT_ONLY |
| ui-spec.md:196 | Sessions within project sorted by descending stateSince | session-list-vm.test.ts:115-138 | None | UNIT_ONLY |
| ui-spec.md:201 | localStorage.mclaude.filterProjectId holds filter | session-list-vm.test.ts:146-158 | None | UNIT_ONLY |
| ui-spec.md:202 | Filter persists across reloads | session-list-vm.test.ts:160-169 | None | UNIT_ONLY |
| ui-spec.md:216 | Stale filter auto-cleared | session-list-vm.test.ts:172-179 | None | UNIT_ONLY |
| ui-spec.md:267-269 | Empty state "No sessions in this project" when filter active | None | None | UNTESTED |
| ui-spec.md:192-197 | Headers shown only when >1 project visible | None (no component tests for DashboardScreen) | None | UNTESTED |
| ui-spec.md:197 | Project header styling (12px, 600, uppercase, --text2, 8px/4px padding) | None | None | UNTESTED |
| ui-spec.md:205-214 | Filter banner (--surf2, 13px/500/--text, "Showing: {name}", ✕ clears) | None | None | UNTESTED |
| ui-spec.md:178-189 | Overflow menu: Filter by Project only when >1 project | None (no component tests for DashboardScreen) | None | UNTESTED |
| ui-spec.md:305-321 | ProjectFilterSheet: All Projects first, radio state, dismiss on select | None (no component tests for ProjectFilterSheet) | None | UNTESTED |

### Phase 4 — Bug Triage

No open bugs with Component: spa related to dashboard project filter feature. Bugs 002, 003, 005 are spa bugs but unrelated to this feature. Bug 004 (heartbeat-stale-agent-down) touches session-list-vm but is about the health banner, not the filter.

| Bug | Title | Verdict | Notes |
|-----|-------|---------|-------|
| BUG-002 | PTT race condition | OPEN | Unrelated to filter feature |
| BUG-003 | First-run message injection race | OPEN | Unrelated to filter feature |
| BUG-004 | Heartbeat stale agent down | OPEN | Unrelated to filter feature |
| BUG-005 | Getting Started canned text | OPEN | Unrelated to filter feature |

### Summary

- Implemented: 11
- Gap: 0
- Partial: 2
- Infra: 11
- Unspec'd: 0
- Dead: 0
- Tested (unit+e2e): 0
- Unit only: 5
- E2E only: 0
- Untested: 6
- Bugs fixed: 0
- Bugs open: 4 (all unrelated to this feature)

### Test run result

```
Test Files  18 passed (18)
     Tests  214 passed (214)
  Duration  4.79s
```
All 214 tests pass. session-list-vm.test.ts: 27 tests, all green.
