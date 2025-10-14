FROM golang:1.24-alpine AS build
WORKDIR /src

# Install git (for some VCS deps) and ensure CA certs are present in builder
RUN apk add --no-cache git ca-certificates && update-ca-certificates

# Go modules first to leverage Docker layer cache
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# модули и исходники должны быть в репозитории
COPY . .

# Build statically linked binary for linux/amd64
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /bin/agent-svc ./cmd/server

# Separate stage to obtain CA certificates for scratch image
FROM alpine:3.20 AS certs
RUN apk add --no-cache ca-certificates && update-ca-certificates

FROM scratch
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /bin/agent-svc /agent-svc
ENTRYPOINT ["/agent-svc"]
