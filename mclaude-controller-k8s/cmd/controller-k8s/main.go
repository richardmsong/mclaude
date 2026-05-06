// mclaude-controller-k8s: Kubernetes controller for per-user namespace management.
//
// Per ADR-0091, this controller's scope is reduced to:
//   - Boot via hostauth.NewHostAuthFromSeed, decode hslug from JWT (ADR-0063).
//   - Subscribe to mclaude.hosts.{hslug}.grants.create → run per-user reconciler.
//   - Keep users.*.projects.*.update handler for OAuth token rotation (user-secrets only).
//
// Removed per ADR-0091:
//   - MCProject CRD reconciliation (delegated to user-agent).
//   - Per-project Deployment, PVCs, agent-nkey-{projectId} Secret.
//   - users.*.projects.*.create and .delete handlers.
//   - DEV_OAUTH_TOKEN env var.
package main

import "mclaude-controller-k8s/controller"

func main() {
	controller.Run()
}
