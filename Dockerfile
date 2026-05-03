# ---- Stage 1: Build ----
FROM golang:1.22-alpine AS builder

WORKDIR /build

# Download dependencies first (layer-cached)
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /app \
    ./cmd/server

# ---- Stage 2: Runtime ----
# distroless/static has no shell, no package manager, no libc vulnerabilities.
FROM gcr.io/distroless/static-debian12

# Copy only the compiled binary — templates are embedded via embed.FS
COPY --from=builder /app /app

# Run as the built-in nonroot user (UID 65532)
USER nonroot:nonroot

EXPOSE 8080

ENTRYPOINT ["/app"]
