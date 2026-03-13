.PHONY: help lint docs test check format build clean dist cover tools doctor

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

lint: ## Lint (go vet + staticcheck)
	go vet ./...
	$(shell go env GOPATH)/bin/staticcheck ./...

docs: ## Lint markdown
	npx --yes markdownlint-cli2 "**/*.md" "#node_modules"

test: ## Run tests with race detection
	go test -race -count=1 ./...

check: lint docs test ## Run all quality gates

format: ## Format code
	gofmt -w .

build: ## Build binary
	go build -o beadle-email ./cmd/beadle-email/

clean: ## Remove build artifacts
	rm -f beadle-email coverage.out
	rm -rf dist/

dist: clean ## Cross-compile for all platforms
	mkdir -p dist
	GOOS=darwin  GOARCH=arm64 go build -o dist/beadle-email-darwin-arm64 ./cmd/beadle-email/
	GOOS=darwin  GOARCH=amd64 go build -o dist/beadle-email-darwin-amd64 ./cmd/beadle-email/
	GOOS=linux   GOARCH=arm64 go build -o dist/beadle-email-linux-arm64  ./cmd/beadle-email/
	GOOS=linux   GOARCH=amd64 go build -o dist/beadle-email-linux-amd64  ./cmd/beadle-email/

cover: ## Test with coverage report
	go test -cover -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

tools: ## Install development tools
	go install honnef.co/go/tools/cmd/staticcheck@latest

doctor: build ## Run beadle-email doctor
	./beadle-email doctor
