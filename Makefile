VERSION := $(or $(shell git describe --tags --always 2>/dev/null | sed 's/^v//'),dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: help lint docs test test-integration check format build install deploy-commands clean dist cover tools doctor

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

lint: ## Lint (gofmt + go vet + staticcheck)
	@test -z "$$(gofmt -s -l ./cmd/ ./internal/ 2>/dev/null)" || { echo "gofmt -s: these files need formatting:"; gofmt -s -l ./cmd/ ./internal/; exit 1; }
	go vet ./...
	$(shell go env GOPATH)/bin/staticcheck ./...

docs: ## Lint markdown
	npx --yes markdownlint-cli2 "**/*.md" "#node_modules"

test: ## Run tests with race detection
	go test -race -count=1 ./...

test-integration: ## Run integration tests (in-process IMAP/SMTP)
	go test -race -count=1 -tags=integration ./...

check: lint docs test ## Run all quality gates

format: ## Format code (with simplify)
	gofmt -s -w ./cmd/ ./internal/

build: ## Build binary
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o beadle-email ./cmd/beadle-email/

install: build ## Build and install to ~/.local/bin
	mkdir -p $(HOME)/.local/bin
	cp beadle-email $(HOME)/.local/bin/beadle-email

deploy-commands: ## Deploy commands to ~/.claude/commands/
	mkdir -p $(HOME)/.claude/commands
	@for f in commands/*.md; do \
		name=$$(basename "$$f"); \
		case "$$name" in *-dev.md) continue;; esac; \
		if [ ! -f "$(HOME)/.claude/commands/$$name" ] || ! diff -q "$$f" "$(HOME)/.claude/commands/$$name" >/dev/null 2>&1; then \
			cp "$$f" "$(HOME)/.claude/commands/$$name"; \
			echo "  deployed /$$( echo $$name | sed 's/\.md$$//')"; \
		fi; \
	done

clean: ## Remove build artifacts
	rm -f beadle-email coverage.out
	rm -rf dist/

dist: clean ## Cross-compile for all platforms
	mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -ldflags="-s -w $(LDFLAGS)" -o dist/beadle-email-darwin-arm64 ./cmd/beadle-email/
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -ldflags="-s -w $(LDFLAGS)" -o dist/beadle-email-darwin-amd64 ./cmd/beadle-email/
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -ldflags="-s -w $(LDFLAGS)" -o dist/beadle-email-linux-arm64  ./cmd/beadle-email/
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -ldflags="-s -w $(LDFLAGS)" -o dist/beadle-email-linux-amd64  ./cmd/beadle-email/
	cd dist && if command -v sha256sum >/dev/null 2>&1; then sha256sum beadle-email-darwin-arm64 beadle-email-darwin-amd64 beadle-email-linux-arm64 beadle-email-linux-amd64 > checksums.txt; else shasum -a 256 beadle-email-darwin-arm64 beadle-email-darwin-amd64 beadle-email-linux-arm64 beadle-email-linux-amd64 > checksums.txt; fi

cover: ## Test with coverage report
	go test -cover -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

tools: ## Install development tools
	go install honnef.co/go/tools/cmd/staticcheck@latest

doctor: build ## Run beadle-email doctor
	./beadle-email doctor
