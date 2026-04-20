# Spec: UI Design System

Shared design tokens — color palette, typography, spacing, viewport, and status dots — used verbatim across all UI components (web SPA, iOS, future).

## Design System

### Color Palette (dark theme — all platforms)

| Token | Hex | Usage |
|-------|-----|-------|
| `--bg` | `#111111` | Page background |
| `--surf` | `#1c1c1e` | Card / sheet surface |
| `--surf2` | `#2c2c2e` | Secondary surface (tool bodies, code blocks) |
| `--surf3` | `#3a3a3c` | Tertiary surface (progress bars, hover) |
| `--border` | `#38383a` | Dividers, card borders |
| `--text` | `#ffffff` | Primary text |
| `--text2` | `#8e8e93` | Secondary text (labels, metadata) |
| `--text3` | `#48484a` | Tertiary text (timestamps, placeholder) |
| `--blue` | `#0a84ff` | User messages, links, active states |
| `--green` | `#30d158` | Success, idle sessions, approve actions |
| `--orange` | `#ff9f0a` | Working/active status, tool events, warnings |
| `--red` | `#ff453a` | Errors, needs-permission status, cancel actions |
| `--purple` | `#bf5af2` | Thinking events, plan mode, model switch |

All platforms use this palette verbatim. Do not substitute platform system colors.

### Typography

- **Body**: SF Pro (iOS) / Inter (web) / system-ui — 14–15px
- **Monospace**: SF Mono (iOS) / Menlo / 'Courier New' — 12–13px — used for tool bodies, code, terminal
- **Nav title**: 17px, weight 600
- **Section labels**: 12px, weight 600, uppercase, letter-spacing 0.5px, color `--text2`

### Spacing

- Screen edge padding: 16px
- Card border-radius: 12px
- Small element border-radius: 8px
- List item height: ~52px with 12px vertical padding
- Separator: 1px `--border` color

### Viewport

The SPA is a full-screen app — no zoom is permitted. The viewport is locked to device width at 1:1 scale.

- **Meta tag**: `<meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no" />`
- **CSS**: `touch-action: manipulation` on `html, body, #root` — prevents double-tap zoom on Safari (which sometimes ignores `user-scalable=no` for accessibility)

### Status Dots

A filled circle (8–10px) indicating session state, animated where noted.

| State | Color | Animation |
|-------|-------|-----------|
| `working` | `--orange` | Pulsing opacity: 1.0 → 0.4 → 1.0, 1.2s loop |
| `needs_permission` | `--red` | None |
| `plan_mode` | `--purple` | None |
| `idle` | `--green` | None |
| `unknown` / `waiting_for_input` | `--text3` (dark gray) | None |

The pulse animation applies only to `working` state and uses a CSS keyframe that scales opacity, not size.
