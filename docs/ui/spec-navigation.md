# Spec: UI Navigation Model

Route-to-screen mapping and navigation bar behavior shared across all UI components.

## Navigation Model

Hash-based routing (web) / stack navigation (iOS):

| Route | Screen |
|-------|--------|
| `#` (default) | Dashboard |
| `#s/{sessionId}` | Session Detail |
| `#settings` | Settings |
| `#usage` | Token Usage |
| `#users` | User Management (admin only) |

Navigation bar is always visible (fixed top). Back navigation uses a `‹ Back` button on the left side. The nav title is always centered.
