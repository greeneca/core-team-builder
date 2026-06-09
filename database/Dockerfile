# syntax=docker/dockerfile:1

# PostgreSQL with our schema baked into the first-boot init directory.
# Files in /docker-entrypoint-initdb.d run automatically (in name order) the
# first time the data volume is initialized.
FROM postgres:16-alpine

COPY migrations/ /docker-entrypoint-initdb.d/
