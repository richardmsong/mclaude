FROM node:22-alpine AS builder

WORKDIR /app
COPY package*.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY . .
RUN npm run build

FROM nginx:1.27-alpine

# Serve on port 8080 (non-root; matches helm securityContext runAsUser: 1000).
# Rewrite the pid path and log dirs so nginx runs without root.
RUN printf 'server {\n    listen 8080;\n    root /usr/share/nginx/html;\n    index index.html;\n    location /assets/ {\n        add_header Cache-Control "public, max-age=31536000, immutable";\n        try_files $uri =404;\n    }\n    location / {\n        add_header Cache-Control "no-cache";\n        try_files $uri $uri/ /index.html;\n    }\n}\n' \
      > /etc/nginx/conf.d/default.conf \
 && sed -i 's|pid  */var/run/nginx.pid|pid /tmp/nginx.pid|g' /etc/nginx/nginx.conf \
 && sed -i 's|pid  */run/nginx.pid|pid /tmp/nginx.pid|g' /etc/nginx/nginx.conf \
 && chown -R 1000:1000 /var/cache/nginx /var/log/nginx /etc/nginx/conf.d \
 && chmod -R 755 /var/cache/nginx \
 && mkdir -p /run && chown 1000:1000 /run

COPY --from=builder /app/dist /usr/share/nginx/html

USER 1000
EXPOSE 8080
