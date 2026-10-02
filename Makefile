.PHONY: build test lint tidy snapshot

build:
	go build -trimpath -o bin/hopsesh ./cmd/hopsesh

test:
	go vet ./...
	go test -race ./...

lint:
	golangci-lint run

tidy:
	go mod tidy

snapshot:
	goreleaser release --snapshot --clean
