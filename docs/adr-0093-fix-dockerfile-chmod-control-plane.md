# ADR: Dockerfile chmod for control-plane binary

**Status**: implemented
**Status history**:
- 2026-05-05: accepted
- 2026-05-05: implemented — CI run 25359655898 deploy-main green; control-plane pod Running on new image (`main-83a367d`).

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
| Idiom | `COPY --chmod=0755 <src> <dest>` (single instruction, sets mode at copy time) | Survives any future CI refactor (cross-runner artifact handoff, tarball roundtrip, native multi-arch). One image-layer instruction. **USER-independent**: works regardless of whether the Dockerfile drops privileges via `USER` before the COPY. Requires `# syntax=docker/dockerfile:1.4` (or higher) at the top of the Dockerfile. |
| Alternative considered | `RUN chmod +x` after `COPY` | Initial choice (commit `49f5f08`); broke when the Dockerfile had `USER 1000` already in effect — non-root user cannot chmod a root-owned file. Replaced by `COPY --chmod` in commit `19216f0`. |
| Alternative considered | Tar before upload-artifact, untar before docker build | Adds a CI step that must be maintained; doesn't help if a future contributor copies the binary another way. |
| Alternative considered | Build inside the Docker layer instead of upload-artifact handoff | Larger refactor; multi-arch build matrix already commits to cross-runner handoff. |
| Scope | All Dockerfiles that `COPY` a Go binary from build context | `mclaude-control-plane`, `mclaude-controller-k8s`, `mclaude-session-agent`, `mclaude-user-agent`. `mclaude-cli` has no Dockerfile; other components build via `RUN go build` inside the Docker layer (no artifact handoff). |

## Component Changes

### All affected Dockerfiles

Add `# syntax=docker/dockerfile:1.4` pragma at the top (required for `--chmod`). Replace any `COPY <bin> <dest>` (with or without a follow-up `RUN chmod +x <dest>`) with:

```dockerfile
COPY --chmod=0755 <bin> <dest>
```

Affected files: `mclaude-control-plane/Dockerfile`, `mclaude-controller-k8s/Dockerfile`, `mclaude-session-agent/Dockerfile` (also for `entrypoint.sh`), `mclaude-user-agent/Dockerfile`. The previous two-stage `alpine AS chmod-stage → distroless` workaround in `mclaude-user-agent/Dockerfile` is replaced by direct single-stage `COPY --chmod=0755` into distroless (distroless supports the buildkit `COPY --chmod` flag without a shell).

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
