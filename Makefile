GO ?= go
BIN_DIR ?= bin

.PHONY: all fmt fmt-check vet test race entrypoint-test build clean docker-build

all: fmt-check vet test build

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './.git/*')

fmt-check:
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './.git/*'))"

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

entrypoint-test:
	./test/entrypoint.sh

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -buildvcs=false -o $(BIN_DIR)/bifrost-server ./cmd/bifrost-server
	$(GO) build -buildvcs=false -o $(BIN_DIR)/bifrost-client ./cmd/bifrost-client
	$(GO) build -buildvcs=false -o $(BIN_DIR)/bifrostctl ./cmd/bifrostctl

clean:
	rm -rf $(BIN_DIR) dist

docker-build:
	docker build -f Dockerfile -t bifrost:dev .
