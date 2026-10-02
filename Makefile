.PHONY: build test lint tidy snapshot app

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

app:
	scripts/build-macos-app.sh
