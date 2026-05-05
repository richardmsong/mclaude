FROM golang:1.25-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o cmd/relay/relay ./cmd/relay/

FROM alpine:3.20
RUN apk --no-cache add ca-certificates
RUN adduser -D -u 10001 relay
COPY --from=builder /app/cmd/relay/relay /relay
USER relay
EXPOSE 8080
ENTRYPOINT ["/relay"]
