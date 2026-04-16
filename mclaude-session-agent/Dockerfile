# Runtime: node base for claude CLI, Go binary copied from build context.
# claude-code installed via native installer (pre-built binary download, not npm compile).
FROM node:22-alpine
# gh — available in Alpine community repo (github-cli package, 2.40+ for multi-account support).
# glab — not in Alpine repos; download binary from GitLab releases.
# Note: curl is already installed for Claude CLI setup. The glab download must happen
# BEFORE the existing `apk del curl` cleanup step.
ARG TARGETARCH=arm64
RUN apk add --no-cache curl bash git openssh-client github-cli && \
    GLAB_VERSION="1.46.0" && \
    curl -fsSL "https://gitlab.com/gitlab-org/cli/-/releases/v${GLAB_VERSION}/downloads/glab_${GLAB_VERSION}_linux_${TARGETARCH}.tar.gz" | \
    tar xz --strip-components=1 -C /usr/local/bin bin/glab && \
    chmod +x /usr/local/bin/glab && \
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
