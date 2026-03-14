# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Install dependencies
RUN apk add --no-cache git make

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN make build

# Runtime stage
FROM alpine:latest

WORKDIR /app

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates

# Copy binaries from builder
COPY --from=builder /app/bin/server /app/server
COPY --from=builder /app/bin/agent /app/agent

# Copy configuration files
COPY configs /app/configs

# Expose ports
EXPOSE 8080 9090

# Default command
CMD ["/app/server"]