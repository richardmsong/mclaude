# ADR: Set ExternalDNS target to Tailscale IP in main deploy

**Status**: accepted
**Status history**:
- 2026-04-28: accepted

## Overview

The main deploy (`deploy-main.yml` + `values-k3d-ghcr.yaml`) was not setting the `externalDnsTarget` Helm value, so ExternalDNS published the k3d internal Docker network IP (`172.20.0.2`) to DigitalOcean DNS for `dev.mclaude.richardmcsong.com`. Only the preview deploy correctly set this to the Tailscale IP via `$(tailscale ip -4)`. This made the webapp unreachable from any device other than the host Mac itself.

## Motivation

Users on the same Tailscale network (e.g. a phone) could not reach the webapp because the DNS A record resolved to `172.20.0.2`, a k3d internal IP that is not routable outside the host. The k3d load balancer already binds to `0.0.0.0:443`, so all interfaces (including Tailscale) are served — the only missing piece was the DNS target.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| How to set the Tailscale IP | `--set "ingress.externalDnsTarget=$(tailscale ip -4)"` at deploy time in CI | Same pattern already used in `deploy-preview.yml`; resolves dynamically so no hardcoded IP in repo |
| Where to apply the fix | `deploy-main.yml` Helm upgrade step | `values-k3d-ghcr.yaml` is committed and cannot use shell expansion; the dynamic `--set` override is the correct layer |

## Impact

- No spec files require update — this is a CI workflow + values gap, not a design gap.
- Affected file: `.github/workflows/deploy-main.yml` (add `--set "ingress.externalDnsTarget=$(tailscale ip -4)"`)

## Scope

**In v1:** Fix `deploy-main.yml` to pass the Tailscale IP as `externalDnsTarget`.

**Deferred:** Generalising to non-Tailscale deployments or multiple network interfaces.
