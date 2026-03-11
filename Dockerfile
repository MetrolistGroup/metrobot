# Build stage: minimal Go 1.26.1 image
FROM golang:1.26.1-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Static binary for distroless (no CGO)
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -ldflags="-s -w" -o /metrobot .

# Run stage: distroless static (no shell, no package manager; CA certs included)
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /metrobot /metrobot
ENTRYPOINT ["/metrobot"]
