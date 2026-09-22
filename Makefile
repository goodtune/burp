.PHONY: build test vet check fmt lint run docker clean

BINARY  := burp
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

## build: compile the static binary
build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/burp

## test: run unit tests
test:
	go test ./...

## vet: run go vet
vet:
	go vet ./...

## fmt: check formatting
fmt:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "run gofmt -w ." && exit 1)

## check: fmt + vet + test
check: fmt vet test

## lint: run golangci-lint if installed
lint:
	golangci-lint run ./...

## run: build and start the server in dev mode against real GitHub
run: build
	BURP_DEV_MODE=true ./$(BINARY) serve

## docker: build the container image
docker:
	docker build --build-arg VERSION=$(VERSION) -t burp:$(VERSION) .

clean:
	rm -f $(BINARY) burp.db burp.db-shm burp.db-wal
