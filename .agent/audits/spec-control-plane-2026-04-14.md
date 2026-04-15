## Run: 2026-04-14T00:00:00Z

GAP: "Per-session-agent credentials: pub/sub { allow: ['mclaude.{userId}.>', '_INBOX.>'] }" → IssueSessionAgentJWT in nkeys.go calls SessionAgentSubjectPermissions which explicitly omits _INBOX.> from both pub and sub. The spec block shows _INBOX.> on both allow lists for session-agent credentials, but the code comment says "no _INBOX since they don't use request/reply" and the implementation omits it. (nkeys.go:32-39)

GAP: "Ensure nix PVC (nix-store) in user namespace (shared across all projects)" → Both the reconciler and the legacy provisioner create a per-project nix PVC named "nix-{projectId}" instead of a single namespace-shared PVC named "nix-store". The spec says one per namespace, the Kubernetes resources section shows claimName: nix-store, and the Nix section says "one per namespace, shared across all project pods". Code creates a separate "nix-{projectId}" per project in both reconciler.go:301 and provision.go:129.

GAP: "9. Ensure project-config ConfigMap (project-{projectId}-config) with GIT_URL from spec.gitUrl" and "10. Ensure Deployment (project-{projectId}) in user namespace with correct spec (mounts project-config ConfigMap at /etc/mclaude/config)" → The reconciler's 11-step loop skips step 9 and 10 entirely. No project-config ConfigMap is created (reconciler.go reconcileSecrets only creates user-config and user-secrets). The Deployment does not mount a project-config ConfigMap at /etc/mclaude/config — GIT_URL is passed as an env var instead. The legacy provisioner (provision.go) has the same gap.

GAP: "These endpoints bind to a separate port (:9090)" → The spec says the break-glass admin port is :9090. The code defaults to port 9091 (main.go:33: adminPort := envOr("ADMIN_PORT", "9091"), Dockerfile ENV ADMIN_PORT=9091, EXPOSE 9091). The port-forward example in the spec is `kubectl port-forward ... 9090:9090`.

GAP: "POST /users  create user + provision K8s namespace" and "GET /users  list users" and "DELETE /users/{id}  deprovision user + delete namespace" → The spec lists these as HTTP endpoints on the main API server. The code only implements them on the admin port (/admin/users). There are no /users routes on the main 8080 mux (server.go RegisterRoutes does not register /users).

GAP: "GET /auth/sso/{provider}  initiate SSO (Entra, Okta)" and "GET /auth/sso/{provider}/cb  SSO callback → NATS JWT" → No SSO endpoints are registered or implemented anywhere in the control-plane source. server.go RegisterRoutes has no /auth/sso routes.

GAP: "POST /api/projects  create project Deployment + PVC" and "DELETE /api/projects/{id}  delete project (PVC retained unless ?purge=true)" and "GET /api/projects  list projects for user (reads NATS KV)" and "GET /api/projects/{id}  get project status (reads NATS KV)" → None of these HTTP endpoints exist. server.go RegisterRoutes registers no /api/projects routes. Project creation goes through NATS only.

GAP: "POST /admin/projects  create project Deployment + PVC (same as NATS projects.create)" and "DELETE /admin/projects/{id}  delete project" and "GET /admin/projects  list projects for user (reads Postgres, not NATS KV)" and "POST /admin/sessions/{id}/stop  kill session (sends SIGTERM to pod)" → None of these break-glass HTTP endpoints are implemented. admin.go only handles /admin/users and /admin/sessions/stop (sessions/stop exists but only as a DB update stub, not SIGTERM to pod).

GAP: "POST /scim/v2/Users  IdP provisions user" and all SCIM endpoints → No SCIM implementation exists anywhere in the control-plane. No /scim routes in server.go.

GAP: "Bucket initialization: control-plane creates all four buckets idempotently on startup (nats.KeyValueStoreOrCreate). Session agents and launchers do not create buckets" → The control-plane only creates the mclaude-projects KV bucket (projects.go ensureProjectsKV). The mclaude-sessions, mclaude-heartbeats, and mclaude-locations buckets are not initialized by the control-plane on startup.

GAP: "/ready  checks Postgres connection only — NATS outage must not mark pod unready" → The /readyz endpoint (server.go:27) returns 200 unconditionally without actually checking Postgres connectivity. The spec says readiness should verify Postgres is reachable.

GAP: "Nix store (/nix/) lives on an Azure Files PVC (RWX) — one per namespace, shared across all project pods" + claimName: nix-store in the Deployment spec → The Deployment spec in the reconciler uses claimName: "nix-{projectId}" (reconciler.go:381) and provision.go uses claimName: "nix-"+projectID (provision.go line ~493). The spec Deployment YAML shows claimName: nix-store (the shared per-namespace PVC, not a per-project one).

GAP: "GET /api/providers  JWT required  List admin OAuth providers" and all other provider/connection OAuth endpoints from plan-github-oauth.md Component Changes → None of the OAuth/provider HTTP endpoints (GET /api/providers, POST /api/providers/{id}/connect, GET /auth/providers/{id}/callback, GET /api/connections/{connection_id}/repos, POST /api/providers/pat, DELETE /api/connections/{connection_id}, PATCH /api/projects/{project_id}) are implemented in the control-plane source.

GAP: "oauth_connections table" from plan-github-oauth.md → The DB schema in db.go does not include the oauth_connections table. The schema constant only has users and projects tables.

GAP: "ALTER TABLE projects ADD COLUMN git_identity_id TEXT REFERENCES oauth_connections(id) ON DELETE SET NULL" from plan-github-oauth.md → Not in the schema constant in db.go.

GAP: "Add GitIdentityID string to MCProjectSpec in mcproject_types.go" from plan-github-oauth.md → MCProjectSpec in mcproject_types.go only has UserID, ProjectID, GitURL. GitIdentityID is absent.

GAP: "controlPlane.externalUrl … Required — control-plane exits on startup if EXTERNAL_URL is empty" from plan-github-oauth.md → main.go does not read EXTERNAL_URL and does not exit on startup if it is absent.
