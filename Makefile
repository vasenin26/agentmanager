build:
	go mod tidy
	go build -o agent-service ./cmd/server

run:
	go run ./cmd/server/main.go