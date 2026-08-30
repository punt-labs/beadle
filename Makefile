VERSION := $(or $(shell git describe --tags --always 2>/dev/null | sed 's/^v//'),dev)
LDFLAGS := -X main.version=$(VERSION)

# Pinned once here. CI invokes these via `make vet`, `make staticcheck`,
# `make lint-strict`, and `make vulncheck` rather than duplicating the
# versioned commands in .github/workflows/test.yml, so local and CI can
# never drift apart.
STATICCHECK_VERSION := v0.8.1
GOLANGCI_LINT_VERSION := v2.12.2
GOVULNCHECK_VERSION := v1.7.0

# Every shell script in the repo, discovered rather than enumerated: a
# hardcoded list means a new .sh ships unlinted while `make lint` and CI stay
# green. `--cached` catches tracked scripts, `--others --exclude-standard`
# catches a new one that is not `git add`ed yet, and gitignored paths (.tmp/,
# node_modules/) stay out. Assumes no spaces in script paths, which the shell
# standard forbids anyway.
SHELL_SCRIPTS := $(shell git ls-files --cached --others --exclude-standard '*.sh' 2>/dev/null)

.PHONY: help lint lint-strict lint-shell vet staticcheck vulncheck docs test test-integration check format build build-daemon install deploy-commands clean dist docker docker-push cover doctor prfaq clean-tex

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

lint: lint-shell vet staticcheck ## Lint (gofmt + go vet + staticcheck + shellcheck)
	@test -z "$$(gofmt -s -l ./cmd/ ./internal/ 2>/dev/null)" || { echo "gofmt -s: these files need formatting:"; gofmt -s -l ./cmd/ ./internal/; exit 1; }

vet: ## Run go vet
	go vet ./...

staticcheck: ## Run staticcheck alone (also part of `lint`)
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

lint-strict: ## Lint (golangci-lint: errcheck, gosec, revive, gofumpt, ...)
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

vulncheck: ## Scan imports + call graph for known Go vulnerabilities
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

lint-shell: ## Lint every shell script in the repo (shellcheck)
	@command -v shellcheck >/dev/null 2>&1 || { echo "shellcheck not found — install it (apt install shellcheck / brew install shellcheck)"; exit 1; }
	@test -n "$(SHELL_SCRIPTS)" || { echo "lint-shell: discovered no *.sh — 'git ls-files' returned nothing (not a git checkout?)"; exit 1; }
	shellcheck $(SHELL_SCRIPTS)

docs: ## Lint markdown
	npx --yes markdownlint-cli2 "**/*.md" "#node_modules"

# LaTeX intermediate files to remove after compilation. prfaq.pdf itself is
# tracked (README's Working Backwards badge links to it); everything else
# here is a build artifact .gitignore already excludes.
LATEX_ARTIFACTS = *.aux *.log *.out *.bbl *.bcf *.blg *.run.xml *.fls \
                  *.fdb_latexmk *.synctex.gz *.toc \
                  docs/*.aux docs/*.log docs/*.out docs/*.bbl docs/*.bcf docs/*.blg \
                  docs/*.run.xml docs/*.fls docs/*.fdb_latexmk docs/*.synctex.gz \
                  docs/*.toc

TEX_FILES = prfaq.tex docs/architecture.tex docs/beadle-identity.tex docs/audit-beadle.tex docs/checklist.tex

prfaq: ## Compile .tex files to .pdf and clean intermediate artifacts
	@for f in $(TEX_FILES); do \
	  echo "Compiling $$f ..."; \
	  dir=$$(dirname "$$f"); base=$$(basename "$$f" .tex); \
	  pdflatex -interaction=nonstopmode -output-directory="$$dir" "$$f" > /dev/null 2>&1; \
	  if [ -f "$$dir/$$base.bib" ] && command -v biber > /dev/null 2>&1; then \
	    (cd "$$dir" && biber "$$base") > /dev/null 2>&1 || true; \
	    pdflatex -interaction=nonstopmode -output-directory="$$dir" "$$f" > /dev/null 2>&1; \
	  fi; \
	  pdflatex -interaction=nonstopmode -output-directory="$$dir" "$$f" > /dev/null 2>&1; \
	  if [ -f "$$dir/$$base.pdf" ]; then \
	    echo "  $$dir/$$base.pdf"; \
	  else \
	    echo "Error: $$f failed to compile" >&2; exit 1; \
	  fi; \
	done
	@rm -f $(LATEX_ARTIFACTS)

clean-tex: ## Remove LaTeX intermediate files
	@rm -f $(LATEX_ARTIFACTS)

test: ## Run tests with race detection
	go test -race -count=1 ./...

test-integration: ## Run integration tests (in-process IMAP/SMTP)
	go test -race -count=1 -tags=integration ./...

check: lint lint-strict vulncheck docs test ## Run all quality gates

format: ## Format code (golangci-lint fmt: gofumpt + goimports, matching lint-strict)
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) fmt ./...

build: ## Build beadle-email binary
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o beadle-email ./cmd/beadle-email/

build-daemon: ## Build beadle-daemon binary
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o beadle-daemon ./cmd/beadle-daemon/

install: build ## Build and install to ~/.local/bin
	mkdir -p $(HOME)/.local/bin
	cp beadle-email $(HOME)/.local/bin/beadle-email

deploy-commands: ## Deploy commands to ~/.claude/commands/
	mkdir -p $(HOME)/.claude/commands
	@for f in plugin/commands/*.md; do \
		name=$$(basename "$$f"); \
		case "$$name" in *-dev.md) continue;; esac; \
		if [ ! -f "$(HOME)/.claude/commands/$$name" ] || ! diff -q "$$f" "$(HOME)/.claude/commands/$$name" >/dev/null 2>&1; then \
			cp "$$f" "$(HOME)/.claude/commands/$$name"; \
			echo "  deployed /$$( echo $$name | sed 's/\.md$$//')"; \
		fi; \
	done

clean: ## Remove build artifacts
	rm -f beadle-email beadle-daemon coverage.out
	rm -rf dist/

dist: clean ## Cross-compile for all platforms
	mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -ldflags="-s -w $(LDFLAGS)" -o dist/beadle-email-darwin-arm64 ./cmd/beadle-email/
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -ldflags="-s -w $(LDFLAGS)" -o dist/beadle-email-darwin-amd64 ./cmd/beadle-email/
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -ldflags="-s -w $(LDFLAGS)" -o dist/beadle-email-linux-arm64  ./cmd/beadle-email/
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -ldflags="-s -w $(LDFLAGS)" -o dist/beadle-email-linux-amd64  ./cmd/beadle-email/
	cd dist && if command -v sha256sum >/dev/null 2>&1; then sha256sum beadle-email-darwin-arm64 beadle-email-darwin-amd64 beadle-email-linux-arm64 beadle-email-linux-amd64 > checksums.txt; else shasum -a 256 beadle-email-darwin-arm64 beadle-email-darwin-amd64 beadle-email-linux-arm64 beadle-email-linux-amd64 > checksums.txt; fi

docker: ## Build Docker image
	docker build --build-arg VERSION=$(VERSION) -t ghcr.io/punt-labs/beadle-email:latest .

docker-push: docker ## Push Docker image to ghcr.io
	docker push ghcr.io/punt-labs/beadle-email:latest

cover: ## Test with coverage report
	go test -cover -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

doctor: build ## Run beadle-email doctor
	./beadle-email doctor
