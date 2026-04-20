# Spec: UI Connection Indicator

Small connection-state dot rendered in the nav bar. Shared across all UI components.

## Component: Connection Indicator

A small colored dot (`.cdot`) in the nav bar.

| State | Color |
|-------|-------|
| Connected | `--green` |
| Connecting | gray, pulsing |
| Error | `--red` |
| Off / disconnected | `--text3` (dark gray) |
