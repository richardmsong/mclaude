FROM alpine:3.21
RUN addgroup -g 1000 app && adduser -u 1000 -G app -s /bin/sh -D app
USER 1000
COPY controller-k8s /usr/local/bin/controller-k8s
ENV LOG_LEVEL=info
ENTRYPOINT ["/usr/local/bin/controller-k8s"]
