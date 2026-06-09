# syntax=docker/dockerfile:1

# The frontend is fully static, so we just serve the files with nginx and
# proxy /api to the backend (see nginx.conf).
FROM nginx:1.27-alpine

# Replace the default site config with ours.
COPY nginx.conf /etc/nginx/conf.d/default.conf

# Copy static assets.
COPY css/   /usr/share/nginx/html/css/
COPY js/    /usr/share/nginx/html/js/
COPY *.html /usr/share/nginx/html/

EXPOSE 80
