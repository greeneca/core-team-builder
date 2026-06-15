# syntax=docker/dockerfile:1

# ---- Build stage ----
FROM golang:1.25-alpine AS build
WORKDIR /src

# CA certificates for the scratch runtime (needed only if the backend ever makes
# outbound TLS, e.g. a future TLS DB connection or HIBP check).
RUN apk add --no-cache ca-certificates

# Cache dependencies first for faster rebuilds.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# Build all binaries (server + seed + bot) statically. -trimpath + stripped
# symbols keep the binaries small and free of local path leakage.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bot ./cmd/bot

# ---- Runtime stage ----
# scratch has no shell/package manager/userland, minimizing the attack surface.
# The binaries are static and the IANA tzdata is embedded (see cmd/server), so
# nothing else from a base image is required.
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/server /app/server
COPY --from=build /out/seed /app/seed
COPY --from=build /out/bot /app/bot

# Run as a non-root numeric UID/GID (no /etc/passwd needed on scratch).
USER 10001:10001
WORKDIR /app
EXPOSE 8080

# Default to the API server; the seed service overrides this command.
ENTRYPOINT ["/app/server"]
