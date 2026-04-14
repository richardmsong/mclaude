# Runtime: node base for claude CLI, Go binary copied from build context.
# claude-code installed via native installer (pre-built binary download, not npm compile).
FROM node:22-alpine
RUN apk add --no-cache curl bash git openssh-client && \
    curl -fsSL https://claude.ai/install.sh | bash && \
    cp /root/.local/bin/claude /usr/local/bin/claude && \
    chmod +x /usr/local/bin/claude && \
    apk del curl

COPY session-agent /usr/local/bin/session-agent
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENV LOG_LEVEL=info
ENV NATS_URL=nats://nats:4222

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
