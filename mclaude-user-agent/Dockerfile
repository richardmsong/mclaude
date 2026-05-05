# syntax=docker/dockerfile:1.4
FROM gcr.io/distroless/static:nonroot

COPY --chmod=0755 user-agent /usr/local/bin/user-agent

ENV LOG_LEVEL=info

ENTRYPOINT ["/usr/local/bin/user-agent"]
