# Viewport Lock — No Zoom

## Overview

The SPA should not zoom at all — pinch-to-zoom, double-tap zoom, and keyboard zoom (Ctrl/Cmd +) should all be prevented. The UI is designed for a fixed viewport and zooming breaks layout, especially on mobile Safari where zoom causes the viewport to shift and text inputs to scroll erratically.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Zoom prevention method | Viewport meta + CSS touch-action | Meta tag prevents pinch/keyboard zoom. CSS `touch-action: manipulation` prevents double-tap zoom (Safari sometimes ignores meta tag alone). Belt and suspenders. |
| Scope | All zoom on all platforms | mclaude is a full-screen app, not a content page. Zooming has no valid use case — all text is already sized for readability. |

## Component Changes

### SPA (`index.html`)

Update the viewport meta tag from:
```html
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
```
to:
```html
<meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no" />
```

### SPA (`tokens.css`)

Add `touch-action: manipulation` to the `html, body, #root` rule. This prevents double-tap-to-zoom on touch devices (Safari workaround — it sometimes ignores `user-scalable=no` for accessibility reasons, but respects `touch-action`).

## Scope

**In scope:**
- Viewport meta tag lock
- CSS touch-action on root elements

**Deferred:**
- Nothing — this is complete as described

## Implementation Plan

| Component | New/changed lines (est.) | Dev-harness tokens (est.) | Notes |
|-----------|--------------------------|---------------------------|-------|
| SPA | 2 lines | ~30k | Meta tag + CSS rule |

**Total estimated tokens:** ~30k
