# Build stage
FROM golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache gcc musl-dev sqlite-dev

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum* ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o qr-tracker main.go

# Runtime stage
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache ca-certificates sqlite-libs

# Create app directory
WORKDIR /root/

# Copy binary from builder
COPY --from=builder /app/qr-tracker .

# Create directory for database
RUN mkdir -p /root/data

# Expose port
EXPOSE 8080

# Set environment variables
ENV PORT=8080

# Run the application
CMD ["./qr-tracker"]
