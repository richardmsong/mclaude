FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -o /control-plane .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates

# Run as non-root (matches helm securityContext runAsUser: 1000)
RUN addgroup -g 1000 app && adduser -u 1000 -G app -s /bin/sh -D app
USER 1000

COPY --from=builder /control-plane /usr/local/bin/control-plane

ENV LOG_LEVEL=info
ENV PORT=8080
ENV ADMIN_PORT=9091

EXPOSE 8080 9091

ENTRYPOINT ["/usr/local/bin/control-plane"]
