GO ?= go
GOFMT ?= gofmt
IMAGE ?= raft-snapshot-retainer:e2e
VERSION ?= dev
.PHONY: test check build image e2e-compose e2e-compose-down
check:
	test -z "$$($(GOFMT) -l cmd internal)"
	$(GO) vet ./...
test:
	$(GO) test -race ./...
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o dist/raft-snapshot-retainer ./cmd/raft-snapshot-retainer
image:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .
e2e-compose:
	python3 test/e2e/scripts/run.py
e2e-compose-down:
	python3 test/e2e/scripts/run.py --down
