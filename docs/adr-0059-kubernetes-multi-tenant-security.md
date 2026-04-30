# ADR: Kubernetes Multi-Tenant Security

**Status**: draft
**Status history**:
- 2026-04-30: draft

## Overview
Define the security boundaries and hardening measures for mclaude's K8s worker controller operating in multi-tenant clusters. The K8s controller is explicitly multi-tenant — other tenants may have pods in the same cluster. This ADR assesses which K8s-native security mechanisms (RBAC, Pod Security Standards, NetworkPolicies, secret encryption, KSA token exchange) add meaningful security on top of the NATS permission model established in ADR-0054.

## Motivation
The mclaude K8s controller manages session-agent pods in shared Kubernetes clusters. NATS credentials (controller JWT, agent JWTs) are stored as K8s Secrets within the mclaude namespace. In a multi-tenant cluster, other tenants' workloads run alongside mclaude pods, creating attack surface that pure NATS-level permissions cannot address.

ADR-0054 established that the NATS permission model is the primary security boundary — it controls what a credential can do once obtained. However, K8s-level controls determine how hard it is to *obtain* those credentials in the first place, and what lateral movement is possible if a pod is compromised.

Key insight from the ADR-0054 design discussion: "The real security boundary is the NATS permission model (what a credential can do once obtained), not the introduction mechanism (how the credential is obtained)." This is true, but defense-in-depth still matters — K8s controls raise the cost of credential theft even if NATS permissions limit blast radius.

## Threat Vectors

### 1. Rogue Tenant Pod Reading mclaude Secrets
A pod in another namespace attempts to read K8s Secrets in the mclaude namespace. This is the most straightforward attack — if successful, the attacker obtains NATS credentials.

**Current mitigation**: K8s RBAC (Secrets are namespace-scoped; default RBAC denies cross-namespace Secret access).
**Residual risk**: Misconfigured ClusterRoleBindings or overly permissive RBAC policies could grant cross-namespace access.

### 2. Compromised Agent Pod — Lateral Movement
A session-agent pod is compromised (e.g., via malicious user input to Claude). The attacker attempts to:
- Read other Secrets in the mclaude namespace
- Access the K8s API to enumerate or modify resources
- Reach other services on the cluster network

**Current mitigation**: ADR-0054 scopes agent JWTs to per-user/per-project NATS resources.
**Residual risk**: The pod's K8s ServiceAccount may have permissions beyond what the agent needs. Network access to cluster services is unrestricted by default.

### 3. Compromised Controller Pod — Elevated Blast Radius
The controller pod has broader permissions than agent pods (it creates/deletes pods, manages Secrets). If compromised:
- Attacker can read all agent Secrets in the namespace
- Attacker can create pods with arbitrary specs
- Attacker holds the controller's NATS JWT

**Current mitigation**: ADR-0054 removes the account signing key from host controllers (only CP can mint JWTs).
**Residual risk**: Controller RBAC permissions are broad within the namespace.

### 4. Cluster Admin with Legitimate Access
A cluster-admin user (or CI service account with cluster-admin) can read all Secrets in all namespaces. This is by design — cluster-admin is fully trusted at the K8s layer.

**Current mitigation**: None at K8s layer (cluster-admin bypasses all RBAC). NATS permission model limits what stolen credentials can do.
**Residual risk**: Organizational — must limit who has cluster-admin.

### 5. Network-Level Attacks (NATS Traffic Interception)
NATS traffic between pods and the hub NATS server traverses the cluster network and potentially the internet. An attacker on the network path could intercept or modify traffic.

**Current mitigation**: NATS TLS (if configured).
**Residual risk**: If TLS is not enforced or certificates are not validated, MITM is possible.

### 6. Supply Chain Attacks on Controller/Agent Images
Malicious or tampered container images for the controller or session-agent.

**Current mitigation**: Images pulled from a private registry (ghcr.io).
**Residual risk**: Compromised CI pipeline, registry credentials, or base images.

## Key Questions (OPEN)

### Q1: What RBAC Policies Are Required to Isolate the mclaude Namespace?
**Status**: OPEN

What is the minimum RBAC configuration to ensure:
- No cross-namespace Secret access to the mclaude namespace
- Controller ServiceAccount has only the permissions it needs (pods, secrets, configmaps in its own namespace)
- Agent pods have no K8s API access at all (or minimal read-only)

Considerations:
- Default K8s RBAC denies cross-namespace Secret reads, but we should audit for ClusterRoleBindings that widen this
- Should we create a dedicated ClusterRole that explicitly denies certain operations?

### Q2: Are Pod Security Standards Needed for the mclaude Namespace?
**Status**: OPEN

Should the mclaude namespace enforce Pod Security Standards (PSS)?
- `restricted`: No privilege escalation, no host namespaces, read-only root filesystem, non-root user, specific seccomp profile
- `baseline`: Blocks known privilege escalations but less restrictive

Considerations:
- Agent pods don't need any elevated privileges — `restricted` seems appropriate
- Controller pod also doesn't need elevated privileges
- PSS prevents a compromised pod from escalating to host-level access

### Q3: Do We Need NetworkPolicies for Agent Pod Egress?
**Status**: OPEN

Should we restrict what agent pods can talk to on the network?
- Allow: NATS hub server (specific IP/port)
- Allow: Claude API (api.anthropic.com:443)
- Deny: Everything else (K8s API server, other pods, other namespaces, arbitrary internet)

Considerations:
- Default K8s networking allows all pod-to-pod and pod-to-internet traffic
- A compromised agent pod could scan the cluster network, reach the K8s API server, or exfiltrate data
- NetworkPolicies require a CNI that supports them (Calico, Cilium, etc.)
- Egress restriction to only NATS + Claude API significantly limits lateral movement

### Q4: Should Secrets Be Encrypted at Rest?
**Status**: OPEN

K8s Secrets are base64-encoded by default (not encrypted). Should we require `EncryptionConfiguration` for etcd-at-rest encryption?

Considerations:
- Protects against etcd backup theft or direct etcd access
- Most managed K8s providers (GKE, EKS, AKS) encrypt etcd at rest by default
- For self-managed clusters (k3d, kubeadm), this is not on by default
- May be overkill for dev/preview environments but important for production multi-tenant

### Q5: Is KSA Token Exchange Worth Implementing?
**Status**: OPEN

KSA token exchange: agent pod presents its K8s ServiceAccount token to the control-plane, which validates it (via TokenReview API) before issuing a NATS JWT.

**Argument for**: Adds a layer — attacker must compromise both the pod identity and the NATS credential exchange.
**Argument against**: If an attacker breaks RBAC enough to read the controller's NATS credentials from a Secret, they can likely read agent credentials directly too. The NATS permission model (ADR-0054) already limits blast radius regardless of how the credential was obtained.

This is a cost/benefit question: implementation complexity vs. marginal security gain given that NATS permissions are the real boundary.

### Q6: How Do We Handle Cluster-Admin Users?
**Status**: OPEN

Cluster-admin users inherently have access to all Secrets. This is a K8s design choice, not a bug.

Options:
- Accept as organizational risk — limit who has cluster-admin via policy
- Use external secret management (Vault, AWS Secrets Manager) so credentials aren't in K8s Secrets at all
- Audit logging (K8s audit policy) to detect Secret reads by admins

### Q7: Threat Model — Compromised Agent vs. Compromised Controller
**Status**: OPEN

Define the expected blast radius for each scenario:

**Compromised agent pod**:
- NATS: Access limited to one user's one project (per ADR-0054)
- K8s: Should have zero K8s API access (no ServiceAccount token mounted, or token with no permissions)
- Network: Should be restricted to NATS + Claude API only

**Compromised controller pod**:
- NATS: Controller JWT scoped to host-level operations (per ADR-0054)
- K8s: Can create/delete pods and secrets in mclaude namespace
- Network: Needs access to NATS hub, K8s API server

The asymmetry is intentional — controllers are fewer and more hardened, agents are numerous and disposable.

## Dependencies
- **ADR-0054** (NATS JetStream Permission Tightening) — defines the NATS permission model that is the primary security boundary. K8s security is the defense-in-depth layer on top.
- **ADR-0035** (Unified Host Architecture) — K8s controller design, defines how the controller creates and manages agent pods.

## Next Steps
1. Audit current RBAC configuration for the mclaude namespace
2. Prototype Pod Security Standards enforcement and verify controller/agent pods comply
3. Prototype NetworkPolicies for agent pod egress restriction
4. Decide on KSA token exchange based on cost/benefit analysis
5. Document recommended security posture for production multi-tenant deployments vs. dev/preview environments
