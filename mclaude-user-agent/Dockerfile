FROM gcr.io/distroless/static:nonroot

COPY user-agent /usr/local/bin/user-agent

ENV LOG_LEVEL=info

ENTRYPOINT ["/usr/local/bin/user-agent"]
