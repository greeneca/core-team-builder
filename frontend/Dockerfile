# syntax=docker/dockerfile:1

# The frontend is fully static, so we just serve the files with nginx and
# proxy /api to the backend (see nginx.conf). The unprivileged image runs as a
# non-root user (uid 101) and listens on 8080 instead of the privileged port 80.
FROM nginxinc/nginx-unprivileged:1.27-alpine

# Replace the default site config with ours, plus the shared security-headers
# snippet it includes.
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY security-headers.conf /etc/nginx/snippets/security-headers.conf

# Copy static assets.
COPY css/   /usr/share/nginx/html/css/
COPY js/    /usr/share/nginx/html/js/
COPY *.html /usr/share/nginx/html/

EXPOSE 8080
