# Audit: Code vs Spec Gaps — Round 4

**Date:** 2026-04-29
**Prior rounds:** 26 + 19 + 6 = 51 gaps (all fixed or flagged)
**This round:** 7 new gaps found

---

## Gap 1: `POST /api/users/{uslug}/hosts` endpoint undocumented

**Spec:** `docs/mclaude-control-plane/spec-control-plane.md` — Protected endpoints table lists `GET /api/users/{uslug}/hosts`, `PUT /api/users/{uslug}/hosts/{hslug}`, `DELETE /api/users/{uslug}/hosts/{hslug}`, device-code flow (`POST .../hosts/code`, `GET .../hosts/code/{code}`, `POST /api/hosts/register`). No direct `POST /api/users/{uslug}/hosts` creation endpoint is documented.

**Code:** `mclaude-control-plane/hosts.go:handleCreateHost` handles `POST /api/users/{uslug}/hosts` with body `{slug, name, publicKey}`. Creates a machine host row directly, bypassing the device-code flow.

**Impact:** Undocumented REST endpoint. Callers (CLI, admin scripts) may use it without knowing the spec doesn't cover it.

**Fix:** Add `POST /api/users/{uslug}/hosts` to the Protected endpoints table in spec-control-plane.md with description: "Creates a machine host directly (`{slug, name, publicKey?}`). Inserts `hosts` row, mints per-host user JWT, returns `HostResponse`."

---

## Gap 2: Chart `adminPort` (9090) vs spec `ADMIN_PORT` default (9091)

**Spec:** `docs/mclaude-control-plane/spec-control-plane.md` — `ADMIN_PORT | No | 9091`

**Code:** `mclaude-control-plane/main.go` reads `envOr("ADMIN_PORT", "9091")` — code default is 9091, matches spec.

**Chart:** `charts/mclaude-cp/values.yaml` sets `controlPlane.service.adminPort: 9090`. The deployment template injects `ADMIN_PORT: 9090`. All Helm-deployed instances use 9090, not 9091.

**Impact:** Every Helm deployment overrides the spec-documented default. The service comment confirms "Admin port (9090) intentionally NOT in this service." The spec and chart disagree on the default.

**Fix:** Either update the spec to say `ADMIN_PORT | No | 9090` (matching chart convention), or update the chart `adminPort` to 9091.

---

## Gap 3: Chart declares separate `metricsPort` (9091) but Go code has no separate metrics listener

**Spec:** `docs/mclaude-control-plane/spec-control-plane.md` — describes two ports: main (8080) and admin (9091, hosts both `/admin/*` AND `/metrics`).

**Chart:** `charts/mclaude-cp/values.yaml` declares three ports: `httpPort: 8080`, `adminPort: 9090`, `metricsPort: 9091`. The deployment template creates three named containerPorts (`http`, `admin`, `metrics`) and injects `METRICS_PORT: 9091`.

**Code:** `main.go` creates exactly two listeners: `PORT` (main) and `ADMIN_PORT` (admin mux serving both `/admin/` and `/metrics`). The Go code does NOT read `METRICS_PORT` and does NOT create a third listener. Nothing listens on port 9091 in Helm deployments (admin is on 9090).

**Impact:** The chart's `metrics` containerPort (9091) has no process listening. The control-plane Service exposes `metricsPort: 9091` which routes to a dead port. Prometheus scrapes targeting port 9091 fail; metrics are actually on 9090 (admin port).

**Fix:** Either (a) remove `metricsPort` from the chart and serve metrics on the admin port (matching code), or (b) add a third listener in Go that reads `METRICS_PORT` and serves `/metrics` separately. Option (a) is simpler and matches current code.

---

## Gap 4: Chart injects `HTTP_PORT` env var but Go code reads `PORT`

**Spec:** `docs/mclaude-control-plane/spec-control-plane.md` — `PORT | No | 8080 | Main API listen port`

**Chart:** `charts/mclaude-cp/templates/control-plane-deployment.yaml` injects `HTTP_PORT: {{ .Values.controlPlane.service.httpPort }}`.

**Code:** `main.go:port := envOr("PORT", "8080")` — reads `PORT`, not `HTTP_PORT`.

**Impact:** Currently harmless (both default to 8080, and the `containerPort` declaration is the same). But if an operator changes `httpPort` in values.yaml, the chart would set `HTTP_PORT` to the new value while the Go binary ignores it and keeps using 8080. Latent misconfiguration.

**Fix:** Either rename the chart env var from `HTTP_PORT` to `PORT` (matching code and spec), or update the Go code to read `HTTP_PORT` and update the spec.

---

## Gap 5: Chart injects phantom env vars not read by Go control-plane

**Chart:** `charts/mclaude-cp/templates/control-plane-deployment.yaml` injects:
- `JWT_REFRESH_THRESHOLD_SECONDS: 900` — Go code does not read this env var.
- `HELM_RELEASE_NAME: {{ .Release.Name }}` — Go control-plane code does not read this (only `mclaude-controller-k8s` uses it).

**Spec:** Neither env var is documented in `spec-control-plane.md`.

**Impact:** Operators tuning `jwtRefreshThresholdSeconds` in values.yaml have no effect. `HELM_RELEASE_NAME` is injected into the wrong chart component.

**Fix:** Remove `JWT_REFRESH_THRESHOLD_SECONDS` and `HELM_RELEASE_NAME` from the CP deployment template, or implement reading them in Go and document in spec.

---

## Gap 6: spec-web.md missing `mclaude-hosts` KV watch (HeartbeatMonitor)

**Spec:** `docs/mclaude-web/spec-web.md` — State Management section lists two KV watches:
- `mclaude-sessions` — session state
- `mclaude-projects` — project list and status

No mention of `mclaude-hosts`.

**Code:** `mclaude-web/src/stores/heartbeat-monitor.ts` implements `HeartbeatMonitor` which watches `mclaude-hosts` KV for host online/offline state. Drives the host liveness indicators in the UI.

**Spec cross-ref:** `docs/spec-state-schema.md` correctly notes "Readers: SPA (KV watch for host list + status)" for `mclaude-hosts` KV. The state schema knows about it; spec-web.md doesn't.

**Impact:** spec-web.md's State Management section is incomplete — omits a functional KV watch that the SPA depends on for host liveness UI.

**Fix:** Add `mclaude-hosts` to the State Management section: "- `mclaude-hosts` — host liveness and online/offline state (via `HeartbeatMonitor`)"

---

## Gap 7: `DeviceCodeStatusResponse` missing `expiresAt` field

**Spec:** `docs/mclaude-control-plane/spec-control-plane.md` — `GET /api/users/{uslug}/hosts/code/{code}`: "Returns `{status: "pending", expiresAt}` while waiting for dashboard redemption."

**Code:** `mclaude-control-plane/hosts.go:DeviceCodeStatusResponse` struct:
```go
type DeviceCodeStatusResponse struct {
    Status string  `json:"status"`
    Slug   string  `json:"slug,omitempty"`
    JWT    string  `json:"jwt,omitempty"`
    HubURL string  `json:"hubUrl,omitempty"`
}
```
No `ExpiresAt` field. The `handleHostCodeStatus` handler returns `{status: "pending"}` without `expiresAt`.

**Impact:** CLI polling the device code status cannot display when the code will expire. The `expiresAt` is stored in the `deviceCodeEntry` but not returned in the response.

**Fix:** Add `ExpiresAt int64 `json:"expiresAt,omitempty"`` to `DeviceCodeStatusResponse` and populate it from `entry.ExpiresAt.Unix()` in the pending response path.

---

## Borderline items considered (not gaps)

1. **Ingress routes `/admin` to main port (8080)**: The ingress template routes `/admin` prefix to the control-plane service's `http` port, but admin handlers only exist on the loopback admin port. Result: `/admin` via ingress → 404. This is intentional security architecture (admin is loopback-only, never externally accessible). Spec-helm.md describes the route, which technically exists in the ingress even though it's a dead path by design.

2. **`kvKeyProject()` builds slug-based key but project KV uses UUID keys**: `subj.ts:kvKeyProject(uslug, hslug, pslug)` builds `{uslug}.{hslug}.{pslug}` which is the target format. The actual project watch in `session-store.ts` uses `kvKeyProjectsForUser(this.userId)` with UUID prefix, matching current behavior. `kvKeyProject` is a forward-looking helper for the deferred key migration — not a gap.

3. **`subjClusterProvision()` / `subjClusterStatus()` / `kvKeyUserClusters()` dead code in subj.ts**: Already flagged in spec-web.md under "Dead Code" section.

4. **HTTP project CRUD endpoints listed twice in spec (as existing and as "not yet implemented")**: Both listings exist in spec-control-plane.md. The "not yet implemented" flag is explicit. This is a spec clarity issue from prior rounds but already flagged.

5. **`LifecycleStore` class not mentioned by name in spec-web.md**: The spec describes lifecycle event subscription at a conceptual level. The store is an implementation detail, unlike HeartbeatMonitor which introduces a new KV bucket watch.
