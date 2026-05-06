# syntax=docker/dockerfile:1.4
FROM gcr.io/distroless/static:nonroot

ARG TARGETARCH
COPY --chmod=0755 cmd/user-agent/user-agent-${TARGETARCH} /usr/local/bin/user-agent

ENV LOG_LEVEL=info

ENTRYPOINT ["/usr/local/bin/user-agent"]
