.PHONY: all build test test-race coverage vet fmt fmt-check docs-check helm-lint helm-template docker-build verify

GOCACHE ?= /private/tmp/proxykube-go-cache
GOMODCACHE ?= /private/tmp/proxykube-go-mod
GOENV = GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE)
COVERAGE_FILE ?= /private/tmp/proxykube-coverage.out

all: verify

build:
	$(GOENV) go build -o /private/tmp/proxykube ./cmd/proxykube

test:
	$(GOENV) go test ./...

test-race:
	$(GOENV) go test -race ./...

coverage:
	$(GOENV) go test ./... -coverprofile=$(COVERAGE_FILE)
	@total=$$($(GOENV) go tool cover -func=$(COVERAGE_FILE) | awk '/^total:/ {gsub(/%/, "", $$3); print $$3}'); \
	  echo "total coverage: $${total}%"; \
	  awk "BEGIN {exit !($${total} == 100)}" || (echo "coverage must remain at 100%"; exit 1)

vet:
	$(GOENV) go vet ./...

fmt:
	gofmt -w cmd internal

fmt-check:
	test -z "$$(gofmt -l .)"

docs-check:
	bash hack/check-function-comments.sh

helm-lint:
	helm lint charts/proxykube

helm-template:
	helm template proxykube charts/proxykube >/dev/null

docker-build:
	docker build -t proxykube:dev .

verify: fmt-check docs-check vet test-race coverage helm-lint helm-template build
