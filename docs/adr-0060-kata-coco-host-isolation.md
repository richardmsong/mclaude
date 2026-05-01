# ADR: Hardware-Isolated Session Agents via Kata Containers and Confidential Computing

**Status**: draft
**Status history**:
- 2026-04-30: draft

> Depends on:
> - `adr-0054-nats-jetstream-permission-tightening.md` — defines the per-project agent JWT model and trust tiers. This ADR closes the host-operator trust gap identified in R7.
> - `adr-0058-byoh-architecture-redesign.md` — defines the per-session agent model on BYOH hosts. This ADR specifies the isolation boundary those agents run inside.

## Overview

Run each session-agent inside a hardware-isolated boundary (Kata Containers microVM or Confidential Computing enclave) so that the host operator — even with root — cannot extract agent credentials, read session data, or tamper with agent behavior. Combined with remote attestation, this enables **secure multitenancy on untrusted hosts**: alice's agent on bob's machine is opaque to bob.

## Motivation

ADR-0054's trust model explicitly accepts that the host operator can extract agent credentials:

> "If alice uses bob's laptop, bob has root access to alice's sessions on his machine (accepted — he owns the hardware)."

Per-project JWT scoping limits blast radius (one project on one host), and 5-minute TTL limits the window. But the host operator can still:
- Read the agent's NKey seed from process memory or filesystem
- Intercept local IPC between host controller and agent
- Read session data (worktree, JSONL history, memories) from the local filesystem
- Attach a debugger to the agent process
- MITM the agent's NATS connection at the network layer

This is acceptable for single-user BYOH (you're the host operator and the user). It is **not acceptable** for shared BYOH or platform hosts where a different person owns the machine. Today the only mitigation is "don't provision sensitive projects on untrusted hosts." Hardware isolation makes the host operator untrusted by design.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Isolation technology | Kata Containers (microVM per agent) | Lightweight VM boundary. Host operator can see the VM exists but not what's inside. Integrates with containerd/CRI on K8s. BYOH uses Kata runtime directly. |
| Confidential computing | AMD SEV-SNP / Intel TDX (optional, additive) | Hardware memory encryption. Even the hypervisor cannot read VM memory. Requires compatible CPU. Not mandatory — Kata alone provides strong isolation; CoCo adds hardware-level attestation. |
| Attestation model | Remote attestation to CP before credential issuance | Agent proves it is running in a genuine Kata/CoCo enclave. CP verifies the attestation report before signing the agent JWT. Prevents the host operator from requesting credentials for a fake agent. |
| Credential flow | Agent generates NKey inside the enclave, attests to CP, receives JWT | NKey seed never exists outside the enclave. Host controller never sees it. CP trusts the attestation, not the host. |
| Linux users | Defense-in-depth inside the VM, not a security boundary | Agent runs as non-root inside the microVM. Useful for least-privilege hygiene but not the isolation mechanism — the VM boundary is. |
| VM multitenancy | Allowed — multiple agents per host, one VM per agent | Each agent gets its own microVM. The host runs many microVMs. This IS multitenancy — hardware isolation makes it safe. |
| BYOH deployment | Kata runtime installed on the host, host controller launches microVMs | Host operator installs the Kata runtime (one-time setup). `mclaude-controller-local` launches agents as Kata VMs instead of bare processes. |
| K8s deployment | Kata as RuntimeClass, pods request it | K8s operator installs Kata as a RuntimeClass. Agent pods specify `runtimeClassName: kata`. Transparent to the agent code. |
| Fallback for non-Kata hosts | Bare process (current model), flagged as "unattested" | Hosts without Kata support continue to work. CP issues credentials but marks them as unattested. Users see a warning: "This host does not provide hardware isolation." |
| Session data at rest | Encrypted filesystem inside the VM | Agent's worktree, JSONL, memories stored on an encrypted volume. Key derived from attestation (sealed to the enclave). Host operator sees ciphertext. |

## Threat Model Changes

### Before (ADR-0054)

| Threat | Mitigation | Residual |
|--------|-----------|----------|
| Host operator extracts agent credential | Per-project scoping + 5-min TTL | Full access to one project's data for up to 5 minutes |
| Host operator reads session data on disk | None | Full access to worktree, history, memories |
| Host operator intercepts NKey at issuance | None (host controller handles the keypair) | Can impersonate the agent |

### After (this ADR)

| Threat | Mitigation | Residual |
|--------|-----------|----------|
| Host operator extracts agent credential | NKey generated inside enclave, never exposed | Cannot extract — VM memory is opaque (Kata) or encrypted (CoCo) |
| Host operator reads session data on disk | Encrypted filesystem, key sealed to enclave | Cannot decrypt without the enclave |
| Host operator intercepts NKey at issuance | Agent attests directly to CP, no host involvement in credential flow | Host controller never sees the NKey |
| Host operator launches fake agent | Attestation fails — CP rejects non-enclave requests | Cannot obtain credentials for a fake agent |

## Credential Flow (with attestation)

```
1. Host controller receives provisioning message from CP
2. Host controller launches a Kata microVM for the agent
3. Inside the microVM, the agent:
   a. Generates an NKey pair (seed stays in VM memory)
   b. Obtains an attestation report from the hardware/hypervisor
      - Kata: virtio-vsock attestation via the Kata agent
      - CoCo (SEV-SNP): SNP_GET_REPORT ioctl → signed report
      - CoCo (TDX): TDG.MR.REPORT → signed report
   c. Sends attestation report + NKey public key to CP
      (via NATS using a bootstrap token from the provisioning message,
       or via HTTP if no NATS credentials yet)
4. CP validates the attestation report:
   a. Verifies report signature (AMD/Intel root of trust)
   b. Checks measurement matches expected agent image
   c. Checks the report is fresh (nonce)
5. CP signs a JWT for the agent's NKey public key
6. Agent connects to NATS with JWT + its NKey seed
   (host controller never sees either)
```

### Bootstrap problem

The agent needs to reach CP to attest, but it has no NATS credentials yet. Two options:

TODO: decide bootstrap mechanism — HTTP attestation endpoint vs. one-time NATS bootstrap token

## K8s Integration

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: agent-alice-myapp
spec:
  runtimeClassName: kata  # or kata-cc for CoCo
  containers:
  - name: session-agent
    image: mclaude-session-agent:latest
    env:
    - name: ATTESTATION_ENDPOINT
      value: "https://cp.mclaude.internal/api/attest"
```

Kata integrates via the CRI (Container Runtime Interface). The K8s operator installs the Kata runtime and registers it as a RuntimeClass. Agent pods request it. No changes to the agent code beyond the attestation handshake.

The Confidential Containers (CoCo) project extends Kata with hardware attestation. `kata-cc` RuntimeClass wraps SEV-SNP or TDX. The agent image is measured at launch and included in the attestation report.

## BYOH Integration

On BYOH hosts, Kata runs without K8s. The host controller uses the Kata runtime CLI to launch microVMs:

```bash
# Host controller launches agent in Kata VM
kata-runtime run \
  --bundle /path/to/agent-bundle \
  --console-socket /tmp/agent-alice-myapp.sock \
  agent-alice-myapp
```

The agent bundle contains the session-agent binary and a minimal rootfs. The host controller manages the VM lifecycle (start, stop, health check) the same way it would manage a process, but the VM boundary prevents credential extraction.

TODO: decide BYOH Kata packaging — OCI bundle vs. raw VM image vs. Firecracker microVM

## What This Does NOT Solve

- **Network-level attacks**: The host operator controls the network. They can block, throttle, or observe NATS traffic metadata (connection timing, message sizes). They cannot read TLS-encrypted content but can perform traffic analysis. A future ADR could explore Tor/onion routing for NATS connections if this matters.
- **Side-channel attacks**: Even with CoCo, sophisticated side-channel attacks (cache timing, power analysis) may leak information. This is a known limitation of TEEs.
- **Denial of service**: The host operator can always kill the VM. Hardware isolation protects confidentiality and integrity, not availability.
- **Supply chain attacks on the agent image**: If the agent binary itself is compromised, attestation proves the compromised binary is running correctly. Image signing and supply chain security are separate concerns.

## Component Changes

### mclaude-control-plane
- New: attestation verification endpoint (HTTP or NATS)
- New: attestation policy engine (which measurements are accepted, which report types)
- Modify credential issuance: if attestation is required (host policy), reject non-attested requests
- New: host capability field — `attestation_support: none | kata | kata-cc-sev | kata-cc-tdx`

### mclaude-controller-local
- Replace process spawning with Kata VM launching
- Pass bootstrap token (not credentials) to the VM
- Health monitoring via virtio-vsock instead of process signals

### mclaude-controller-k8s
- Add `runtimeClassName` to agent pod spec (configurable per host)
- No other changes — K8s handles VM lifecycle

### mclaude-session-agent
- New: attestation handshake at startup (obtain report, send to CP, receive JWT)
- New: NKey generation at startup (inside the VM, never exported)
- New: encrypted filesystem setup for session data
- Remove: acceptance of NKey seed from host controller (seed is self-generated)

### mclaude-common
- New: attestation report types and validation helpers

## Scope

**v1 (this ADR):**
- Kata Containers integration for K8s hosts (RuntimeClass)
- Remote attestation handshake (agent → CP)
- CP attestation verification (AMD SEV-SNP report format)
- Host capability advertisement (`attestation_support` field)
- Fallback mode for non-Kata hosts (unattested, warning to user)

**Deferred:**
- BYOH Kata integration (requires Kata runtime packaging for macOS/Linux desktop)
- Intel TDX support (different attestation report format)
- Encrypted filesystem for session data at rest
- Network-level protections (traffic analysis resistance)
- Attestation policy management UI
- Image signing and measurement management

## Open questions

- Bootstrap mechanism: HTTP attestation endpoint vs. one-time NATS bootstrap token. HTTP is simpler (agent doesn't need NATS to attest) but requires the agent to reach CP over HTTP (which may not be available on all networks). NATS bootstrap token reuses existing infrastructure but requires the host controller to have a token-issuance path.
- BYOH Kata packaging: OCI bundle vs. raw Firecracker microVM image. OCI is standard but requires containerd on the host. Firecracker is lighter but non-standard.
- macOS support: Kata uses Linux KVM. macOS BYOH hosts would need Virtualization.framework or a different hypervisor. Is macOS BYOH in scope for hardware isolation, or is it inherently single-user (you own the machine)?
- Cost of microVM per agent: memory overhead (~30-50MB per microVM), startup latency (~150-300ms). Is this acceptable for the expected agent density per host?
- Migration path for existing hosts: how do hosts transition from bare-process to Kata? Is it opt-in per host, per group, or globally enforced?
