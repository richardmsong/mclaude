# Spec: UI Auth / Login

Login flow and error contract shared across all UI components.

## Screen: Auth / Login

Shown when no access token is stored.

```
┌─────────────────────────────────┐
│                                 │
│              ⚡                  │  (large icon, centered)
│           MClaude               │  (title, 28px bold)
│  Sign in to your account        │  (subtitle, --text2)
│                                 │
│  ┌─────────────────────────┐    │
│  │  email@example.com      │    │  (email field)
│  └─────────────────────────┘    │
│  ┌─────────────────────────┐    │
│  │  •••••••••••••          │    │  (password field)
│  └─────────────────────────┘    │
│  ┌─────────────────────────┐    │
│  │         Connect         │    │  (primary button, --blue fill)
│  └─────────────────────────┘    │
│                                 │
└─────────────────────────────────┘
```

**Behavior:**
- Pressing Return / Enter in the token field submits
- On success: token persisted in local storage; navigate to Dashboard
- On failure: error state shown inline (red text below field). Error message rules:
  - Server returned an error body (non-2xx with text): show the server's response text verbatim
  - Server returned non-2xx with no body: show `Login failed: {status}`
  - Network error (`Load failed` on Safari, `Failed to fetch` on Chrome): show `Network error — if using HTTPS with a self-signed certificate, ensure it is trusted in your system keychain`
  - Login succeeded but NATS connection failed: show `Login succeeded but could not connect to messaging ({natsUrl}): {error}`
