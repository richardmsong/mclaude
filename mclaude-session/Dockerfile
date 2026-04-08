FROM node:22-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    tmux git curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN npm install -g @anthropic-ai/claude-code

RUN mkdir -p /home/node/.claude /workspace && \
    chown -R node:node /home/node /workspace

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

USER node
ENV HOME=/home/node
WORKDIR /workspace

ENTRYPOINT ["/entrypoint.sh"]
