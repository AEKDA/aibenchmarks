GO ?= go

.PHONY: build test vet lint fmt run tidy

build:
	$(GO) build ./...

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint: vet fmt
	@test -z "$$(gofmt -l cmd internal)" || { echo "gofmt found unformatted files:"; gofmt -l cmd internal; exit 1; }

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

run:
	$(GO) run ./cmd/dispatcher
