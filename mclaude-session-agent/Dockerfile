# Runtime: node base for claude CLI, Go binary copied from build context.
# claude-code installed via native installer (pre-built binary download, not npm compile).
FROM node:22-alpine
ARG TARGETARCH=amd64

RUN apk add --no-cache curl bash && \
    curl -fsSL https://claude.ai/install.sh | bash && \
    apk del curl bash

COPY session-agent-${TARGETARCH} /usr/local/bin/session-agent

ENV LOG_LEVEL=info
ENV NATS_URL=nats://nats:4222

ENTRYPOINT ["/usr/local/bin/session-agent"]
