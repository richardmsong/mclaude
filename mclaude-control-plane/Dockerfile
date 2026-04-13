ARG TARGETARCH=amd64
FROM alpine:3.21
RUN apk add --no-cache ca-certificates

RUN addgroup -g 1000 app && adduser -u 1000 -G app -s /bin/sh -D app
USER 1000

COPY control-plane-${TARGETARCH} /usr/local/bin/control-plane

ENV LOG_LEVEL=info
ENV PORT=8080
ENV ADMIN_PORT=9091

EXPOSE 8080 9091

ENTRYPOINT ["/usr/local/bin/control-plane"]
