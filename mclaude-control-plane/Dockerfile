FROM alpine:3.21

ARG TARGETARCH

RUN apk add --no-cache ca-certificates curl \
    && curl -fsSL -o /usr/local/bin/dbmate https://github.com/amacneil/dbmate/releases/download/v2.24.2/dbmate-linux-${TARGETARCH} \
    && chmod +x /usr/local/bin/dbmate \
    && apk del curl

RUN addgroup -g 1000 app && adduser -u 1000 -G app -s /bin/sh -D app
USER 1000

COPY control-plane-${TARGETARCH} /usr/local/bin/control-plane

ENV LOG_LEVEL=info
ENV PORT=8080
ENV ADMIN_PORT=9091

EXPOSE 8080 9091

ENTRYPOINT ["/usr/local/bin/control-plane"]
