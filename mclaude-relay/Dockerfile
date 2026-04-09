FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o relay .

FROM alpine:3.19
COPY --from=builder /app/relay /relay
EXPOSE 8080
ENTRYPOINT ["/relay"]
