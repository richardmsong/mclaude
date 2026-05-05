# ADR: Dockerfile chmod for control-plane binary

**Status**: accepted
**Status history**:
- 2026-05-05: accepted

## Overview

The `mclaude-control-plane` Docker image must explicitly `chmod +x /usr/local/bin/control-plane` after the `COPY` step so the binary is executable inside the container regardless of the source file's mode bits in the build context.

## Motivation

The deploy-main CI runs starting at commit `7e01e8b` (multi-arch refactor) ship the `control-plane` binary as a non-executable file (`-rw-r--r--`) inside the published image. The container fails to start with:

```
exec: "control-plane": executable file not found in $PATH
```

Three failed deploys (run IDs 25357522438, 25357603950, 25357683610) confirm the regression. The neighboring `dbmate` binary in the same image is `-rwxr-xr-x` and works — the difference is that `dbmate` is downloaded and `chmod +x`d inside the Dockerfile, while `control-plane` is `COPY`d from the build context.

The build context's `control-plane` file loses its executable bit because:
- `actions/upload-artifact` in GitHub Actions strips Linux file mode bits on upload (well-known behavior, [actions/upload-artifact#38](https://github.com/actions/upload-artifact/issues/38))
- `actions/download-artifact` cannot restore the bit
- `docker/build-push-action` then `COPY`s a `0644` file into the image

The smallest, most portable fix is to add `RUN chmod +x` in the Dockerfile so the image is correct regardless of how the binary arrived in the build context.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Where to fix | `mclaude-control-plane/Dockerfile` — `RUN chmod +x` after `COPY` | Survives any future CI refactor (cross-runner artifact handoff, tarball roundtrip, native multi-arch). One image-layer instruction. |
| Alternative considered | Tar before upload-artifact, untar before docker build | Adds a CI step that must be maintained; doesn't help if a future contributor copies the binary another way. |
| Alternative considered | Build inside the Docker layer instead of upload-artifact handoff | Larger refactor; multi-arch build matrix already commits to cross-runner handoff. |
| Scope | This ADR fixes only the `mclaude-control-plane` image | Other Go-binary images (`mclaude-controller-k8s`, `mclaude-session-agent`, `mclaude-user-agent`, `mclaude-cli`) potentially have the same risk. Each image's Dockerfile should `chmod +x` its `COPY`'d binary too — covered as in-scope cleanup. |

## Component Changes

### mclaude-control-plane/Dockerfile

Add a `RUN chmod +x /usr/local/bin/control-plane` line immediately after `COPY control-plane /usr/local/bin/control-plane`.

### mclaude-controller-k8s/Dockerfile, mclaude-session-agent/Dockerfile, mclaude-user-agent/Dockerfile, mclaude-cli/Dockerfile

Same treatment — `chmod +x` after `COPY` for each Go binary, to make every image robust to artifact-bit-stripping. Apply only to images that actually exist; skip any that build via `go install` or `go build` in a multi-stage build (no artifact handoff).

## Impact

- No spec is touched. Dockerfile mode-bit handling is an implementation detail; no `docs/**/spec-*.md` describes it.
- No data model or API changes.

## Scope

**In scope:**
- Add `RUN chmod +x` after `COPY` in every component Dockerfile that copies a Go binary from the build context.

**Out of scope:**
- CI workflow refactor to preserve mode bits across upload-artifact/download-artifact (heavier change, not required once Dockerfiles are robust).

## Integration Test Cases

No integration tests — change is build-only. The smoke-test gate is the next deploy-main CI run: pulling the resulting image and starting any container that uses the binary as `command:` (init-keys Job, controller, etc.) must not return `executable file not found in $PATH`.

## Implementation Plan

| Component | Est. lines | Est. tokens | Notes |
|-----------|-----------|-------------|-------|
| Dockerfiles (5 components) | ~5 lines | 10k | One `RUN chmod +x` per Dockerfile. |

**Total estimated tokens:** 10k
