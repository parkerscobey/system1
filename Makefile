APP=system1

.PHONY: build test fmt tidy run

build:
	go build -o bin/$(APP) ./cmd/system1

test:
	go test ./...

fmt:
	go fmt ./...

tidy:
	go mod tidy

run:
	go run ./cmd/system1 serve
