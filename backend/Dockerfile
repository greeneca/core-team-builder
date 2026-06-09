# syntax=docker/dockerfile:1

# ---- Build stage ----
FROM golang:1.25-alpine AS build
WORKDIR /src

# Cache dependencies first for faster rebuilds.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# Build both binaries (server + seed) statically.
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/seed ./cmd/seed

# ---- Runtime stage ----
FROM alpine:3.20
RUN adduser -D -u 10001 appuser
WORKDIR /app

COPY --from=build /out/server /app/server
COPY --from=build /out/seed /app/seed

USER appuser
EXPOSE 8080

# Default to the API server; the seed service overrides this command.
ENTRYPOINT ["/app/server"]
