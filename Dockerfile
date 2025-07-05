# ---- Stage 1: Build Frontend ----
# Use a Node.js image to build the React app
FROM node:20-alpine AS frontend-builder

# Set working directory
WORKDIR /app/frontend

# Copy package.json and package-lock.json
COPY frontend/package*.json ./

# Install dependencies
RUN npm ci

# Copy the rest of the frontend source code
COPY frontend/ ./

# Build the frontend for production
# Note: We need to pass the build-time environment variables here if they are needed at build time
# For now, we assume they are not needed for the build itself, only at runtime.
RUN npm run build


# ---- Stage 2: Build Backend ----
# Use a Go image to build the backend app
FROM golang:1.22-alpine AS backend-builder

# Install build dependencies
RUN apk add --no-cache git

# Set working directory
WORKDIR /app

# Copy go.mod and go.sum
COPY backend/go.mod backend/go.sum ./

# Download dependencies
RUN go mod download

# Copy the rest of the backend source code
COPY backend/ ./

# Build the backend executable
# CGO_ENABLED=0 is important for creating a static binary
# -o /app/server builds the executable and places it in /app/server
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/server ./main.go


# ---- Stage 3: Final Production Image ----
# Use a lightweight base image like Alpine Linux
FROM alpine:latest

# Install ca-certificates for HTTPS support
RUN apk --no-cache add ca-certificates

# Set working directory
WORKDIR /app

# Create a non-root user to run the application
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Copy the built backend executable from the backend-builder stage
COPY --from=backend-builder /app/server .

# Copy the built frontend static files from the builder stage
# We'll place them in a 'public' directory to be served by the backend
COPY --from=frontend-builder /app/frontend/dist ./public

# Copy the .env.example file for reference (optional)
COPY backend/.env.example .

# Change ownership of the app directory to appuser
RUN chown -R appuser:appgroup /app

# Switch to non-root user
USER appuser

# Expose the port the application will run on
EXPOSE 8080

# The command to run the application
# The backend will need to be modified to serve the static files from the './public' directory
CMD ["./server"]