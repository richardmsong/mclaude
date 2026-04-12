---
name: ui-change
description: Make a UI change to the mclaude SPA. Always updates the spec and wireframes first, then implements via dev-harness. Spec is the source of truth — no UI code is written before the spec is updated.
---

# UI Change

Make a UI change to the mclaude SPA. The spec and wireframes are updated first, then the implementation follows. No code is written that isn't reflected in the spec.

## Usage

```
/ui-change <description of the change>
```

Examples:
- `/ui-change remove server URL field from login screen`
- `/ui-change add dark mode toggle to settings`
- `/ui-change show token usage inline in session header`

---

## Algorithm

```
1. Read the current spec
   - docs/ui-spec.md          (screens, wireframes, behavior)
   - docs/plan-client-architecture.md  (stores, viewmodels, protocol)
   - docs/feature-list.md     (feature IDs and platform matrix)

2. Understand the change
   - Identify which screen(s) and component(s) are affected
   - Identify any store/viewmodel/API surface changes needed
   - Note what must be REMOVED vs ADDED vs CHANGED

3. Update the spec — before any code
   a. Edit docs/ui-spec.md:
      - Update wireframe ASCII art to reflect the new layout
      - Update behavior bullets (field labels, validation, interactions)
      - Remove or add entries precisely — don't leave stale text
   b. If the change touches stores, viewmodels, or API:
      - Update docs/plan-client-architecture.md accordingly
   c. Commit: "spec(ui): <description of change>"
      - Commit message must describe WHAT changed and WHY
      - Never commit code and spec changes together

4. Implement the change
   - Run /dev-harness spa --category views
   - dev-harness reads the updated spec and implements exactly what it says
   - Do not implement anything not now present in the spec

5. Verify
   - Build passes (npm run build)
   - Tests pass (npm test)
   - Visual check: the rendered screen matches the updated wireframe

6. Deploy (if dev cluster is running)
   - docker build + k3d image import + kubectl rollout restart
```

---

## Spec Update Rules

These rules apply when editing `docs/ui-spec.md`:

**Removing a field or element:**
- Delete the field from the wireframe ASCII art
- Delete its behavior bullet
- Delete any reference to it in other sections
- If it drove a store/viewmodel value, note in the commit whether that value is still used elsewhere

**Adding a field or element:**
- Add it to the wireframe ASCII art in the correct position
- Add a behavior bullet describing: label, validation, default, what happens on submit
- If it needs a new store value or API field, add it to plan-client-architecture.md too

**Changing a label or layout:**
- Update the wireframe to show the new label/position
- Update the behavior bullet to match

**Never:**
- Leave the spec and the implementation out of sync
- Add code that has no corresponding spec entry
- Describe intended future behavior in the spec — only describe what will be built now

---

## Reference

- Spec: `docs/ui-spec.md`
- Client arch: `docs/plan-client-architecture.md`
- Features: `docs/feature-list.md`
- Implementation: `mclaude-web/src/`
