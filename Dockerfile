# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk --no-cache add ca-certificates git

# Copy go mod files
COPY go.mod go.sum* ./
RUN go mod download

# Copy source code
COPY cmd/ ./cmd/
COPY config/ ./config/
COPY internal/ ./internal/
COPY app/ ./app/

# Build the coordinator binary
WORKDIR /build/cmd/coordinator
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /build/coordinator .

# Production stage
FROM alpine:latest

# Install runtime dependencies
# Go is needed for subprocess execution of extract.go, convert.go, store.go
RUN apk --no-cache add ca-certificates go tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/coordinator ./coordinator

# Copy preserved extraction code (100% unchanged)
COPY app/ ./app/

# Create necessary directories
RUN mkdir -p batches downloads logs archive/failed

# Set timezone
ENV TZ=UTC

# Expose ports
EXPOSE 8080 9090

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Run coordinator
CMD ["./coordinator"]
