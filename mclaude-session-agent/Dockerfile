FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /session-agent .

# --- runtime ---
# Use node base so we can install claude via npm
FROM node:22-alpine

# Install claude CLI
ARG CLAUDE_VERSION=latest
RUN if [ "$CLAUDE_VERSION" = "latest" ]; then \
      npm install -g @anthropic-ai/claude-code; \
    else \
      npm install -g @anthropic-ai/claude-code@${CLAUDE_VERSION}; \
    fi

COPY --from=builder /session-agent /usr/local/bin/session-agent

ENV LOG_LEVEL=info
ENV NATS_URL=nats://nats:4222

ENTRYPOINT ["/usr/local/bin/session-agent"]
