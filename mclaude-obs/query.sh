#!/bin/sh
# Run a read-only SQL query against the observability store, e.g.
#   ./query.sh "SELECT Timestamp, EventName FROM otel_logs ORDER BY Timestamp DESC LIMIT 20"
set -eu
docker exec -i mclaude-obs-clickhouse-1 clickhouse-client --user otel --password otel \
  --database otel --readonly 1 --format PrettyCompactMonoBlock -q "$1"
