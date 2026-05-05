FROM alpine:3.21 AS chmod-stage
COPY user-agent /usr/local/bin/user-agent
RUN chmod +x /usr/local/bin/user-agent

FROM gcr.io/distroless/static:nonroot

COPY --from=chmod-stage /usr/local/bin/user-agent /usr/local/bin/user-agent

ENV LOG_LEVEL=info

ENTRYPOINT ["/usr/local/bin/user-agent"]
