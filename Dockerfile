# syntax=docker/dockerfile:1

# Build stage
FROM golang:1.24.2-alpine AS builder
WORKDIR /app

# Install necessary build tools for CGO (like gcc, musl-dev for alpine)
RUN apk add --no-cache gcc musl-dev

# Copy go mod and sum files
COPY go.mod go.sum ./
# Download dependencies (important now with CGO dependencies)
RUN go mod download

# Copy the source code
COPY . .

# Build the application (CGO enabled by default)
# RUN CGO_ENABLED=0 GOOS=linux go build -o /mmp-server ./cmd/mmp-server
RUN go build -o /mmp-server ./main.go

# Final stage
FROM alpine:latest
WORKDIR /app/

# Copy required C libraries if any (might be needed for SQLite depending on how it's linked)
# For standard alpine build, the C libraries should be present in the base alpine image.

# Copy the pre-built binary file from the previous stage
COPY --from=builder /mmp-server .
COPY --from=builder /app/static ./static

# If using SQLite with file-based storage, create a volume for the DB file
VOLUME /app/data

# Create directory for SQLite database
RUN mkdir -p /app/data

# Expose port 18080
EXPOSE 18080

# Command to run the executable
CMD ["./mmp-server"]