BINARY ?= pigen
PKG    := ./cmd/pigen
IMAGE  ?= ghcr.io/OWNER/pigen:latest

.PHONY: build test vet fmt fmt-check run image clean

build: ## build the static binary
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o $(BINARY) $(PKG)

test: ## run tests
	go test ./...

vet: ## go vet
	go vet ./...

fmt: ## format the whole tree
	gofmt -w .

fmt-check: ## fail if anything is unformatted
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "unformatted:"; echo "$$out"; exit 1; fi

run: ## build and run locally
	go run $(PKG)

image: ## build the container image
	docker build -t $(IMAGE) .

clean:
	rm -f $(BINARY)
