GO_DIR := go

.PHONY: build test test-go test-python test-ts test-integration lint example crash-suite spec

build:
	cd $(GO_DIR) && go build -o ../bin/ ./agent-runtime/cmd/... ./policy-engine/cmd/...

test: test-go test-python test-ts test-integration

test-go:
	cd $(GO_DIR) && go vet ./... && go test $$(go list ./... | grep -v /integration)

test-python:
	cd packages/agent-runtime-sdk && uv run pytest -q
	cd packages/browser-gym && uv run pytest -q

test-ts:
	cd $(GO_DIR)/policy-engine/clients/ts && npm test
	cd packages/browser-gym/harness && npm test

# The browser task as a runtime job, with the Python tool server started by the test.
test-integration:
	uv sync
	cd $(GO_DIR) && HANA_PYTHON=$(CURDIR)/.venv/bin/python go test ./integration/

crash-suite:
	cd $(GO_DIR) && go test -run TestCrashSuite -count=1 -v ./agent-runtime/runtime/

# TLC model-checks the decision table and the approval rules, then confirms each seeded bug is caught.
spec:
	go/policy-engine/spec/check.sh

lint:
	uv run ruff check .
	cd $(GO_DIR) && test -z "$$(gofmt -l .)"

example: build
	examples/browser-job/run.sh
