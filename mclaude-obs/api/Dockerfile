# MClaude Obs API: read-only JSON over ClickHouse, plus the Ops dispatcher and findings API. Standard library only.
FROM python:3.12-slim
ENV PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1
WORKDIR /app
COPY server.py /app/server.py
# Findings store (the Ops dispatcher's SQLite). A new named volume mounted here takes this ownership.
RUN mkdir -p /data && chown nobody:nogroup /data
VOLUME /data
USER nobody
EXPOSE 4320
HEALTHCHECK --interval=15s --timeout=3s --retries=3 \
  CMD ["python", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:4320/api/health', timeout=2.5)"]
CMD ["python", "/app/server.py"]
