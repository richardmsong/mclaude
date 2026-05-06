# syntax=docker/dockerfile:1.4
FROM alpine:3.21

ARG TARGETARCH

RUN addgroup -g 1000 app && adduser -u 1000 -G app -s /bin/sh -D app
USER 1000
COPY --chmod=0755 cmd/controller-k8s/controller-k8s-${TARGETARCH} /usr/local/bin/controller-k8s
ENV LOG_LEVEL=info
ENTRYPOINT ["/usr/local/bin/controller-k8s"]
