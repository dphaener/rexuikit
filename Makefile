.DEFAULT_GOAL := test

.PHONY: test
test: ## Run the rexuikit test suite (hermetic, sandbox-safe)
	go test ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: live
live: ## Run live contract tests against a running Rex (unsandboxed)
	go test -tags rexlive ./livetest/...
