ARG TARGETARCH=amd64

# Runtime: node base for claude CLI, Go binary copied from build context.
FROM node:22-alpine

# Install claude CLI (runs under QEMU for arm64 — Go binary is pre-built natively).
ARG CLAUDE_VERSION=latest
RUN if [ "$CLAUDE_VERSION" = "latest" ]; then \
      npm install -g @anthropic-ai/claude-code; \
    else \
      npm install -g @anthropic-ai/claude-code@${CLAUDE_VERSION}; \
    fi

COPY session-agent-${TARGETARCH} /usr/local/bin/session-agent

ENV LOG_LEVEL=info
ENV NATS_URL=nats://nats:4222

ENTRYPOINT ["/usr/local/bin/session-agent"]
