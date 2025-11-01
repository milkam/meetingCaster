FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install dependencies for building (CGO requires gcc, musl-dev)
RUN apk add --no-cache git gcc musl-dev sqlite-dev

# Copy source code first (so go get can see the imports)
COPY *.go ./

# Copy go mod files
COPY go.mod ./

# Get the package from main branch - this will download it and update go.mod
RUN go get github.com/milkam/gochromecast@95477cd

# Get Google Cloud Text-to-Speech (let Go resolve the latest version)
RUN go get cloud.google.com/go/texttospeech/apiv1@latest

# Download all dependencies
RUN go mod download

# Tidy up dependencies
RUN go mod tidy

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o main .

# Final stage
FROM alpine:latest

# Install SQLite, ffmpeg, fonts and required dependencies for CGO
RUN apk --no-cache add ca-certificates tzdata sqlite ffmpeg font-dejavu

WORKDIR /root/

# Copy the binary from builder
COPY --from=builder /app/main .

# Create data directories
RUN mkdir -p /data
RUN mkdir -p ./data/chunks

EXPOSE 8080

CMD ["./main"]


