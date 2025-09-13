FROM golang:1.20-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
# модули и исходники должны быть в репозитории
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/agent-svc ./cmd/server

FROM scratch
COPY --from=build /bin/agent-svc /agent-svc
ENTRYPOINT ["/agent-svc"]
