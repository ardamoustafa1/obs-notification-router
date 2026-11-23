.PHONY: test test-race lint build run
test:
	go test ./...
test-race:
	go test -race ./...
lint:
	go vet ./...
build:
	go build ./cmd/router
run:
	go run ./cmd/router

