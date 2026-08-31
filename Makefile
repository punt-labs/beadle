VERSION := $(or $(shell git describe --tags --always 2>/dev/null | sed 's/^v//'),dev)
LDFLAGS := -X main.version=$(VERSION)

# Pinned once here. CI invokes these via `make vet`, `make staticcheck`,
# `make lint-strict`, and `make vulncheck` rather than duplicating the
# versioned commands in .github/workflows/test.yml, so local and CI can
# never drift apart.
STATICCHECK_VERSION := v0.8.1
GOLANGCI_LINT_VERSION := v2.12.2
GOVULNCHECK_VERSION := v1.7.0
ETHOS_VERSION := v4.16.0

# Every shell script in the repo, discovered by shebang rather than by
# extension or a hardcoded list: a script deployed under a literal binary
# name (scripts/beadle-sysreport, resolved by beadle-daemon's cli-runner
# whitelist against exactly that filename, no .sh suffix) still needs
# shellcheck, and an extension-only glob silently skips it -- along with
# any future extensionless script. `--cached` catches tracked scripts,
# `--others --exclude-standard` catches a new one that is not `git add`ed
# yet, and gitignored paths (.tmp/, node_modules/) stay out. Each
# candidate's first line is read and matched against a shell shebang (sh,
# bash, dash, zsh, ksh); the match anchors at end-of-line rather than using
# `\b` (a GNU grep extension a BSD grep lacks), so `#!/usr/bin/env python3`
# and the like are still correctly excluded. Assumes no spaces in script
# paths, which the shell standard forbids anyway.
#
# DISCOVER_SHELL_SCRIPTS is a shared pipeline stage, not inlined here,
# because it is also exercised directly by test-shell-discovery below: a
# candidate filename arrives at the inner shell as a genuine argv element
# ("$$@"), never spliced into the TEXT of the -c script, so a filename
# containing shell metacharacters (an embedded quote, a semicolon) cannot
# break out of quoting and execute. An earlier version used `xargs -I{}`,
# substituting each filename directly into the script string -- `git
# ls-files --others` includes untracked files, so a crafted filename in
# the working tree was enough to run arbitrary shell during `make check`.
# Both this variable and the test below must keep calling the identical
# command so a future edit to one cannot silently reintroduce the bug the
# test exists to catch.
define DISCOVER_SHELL_SCRIPTS
xargs -0 sh -c 'for f in "$$@"; do [ -f "$$f" ] || continue; \
	head -n1 "$$f" 2>/dev/null | grep -aqE "^#!.*(sh|bash|dash|zsh|ksh)$$" && printf "%s\n" "$$f"; \
done' _
endef

SHELL_SCRIPTS := $(shell git ls-files --cached --others --exclude-standard -z 2>/dev/null | $(DISCOVER_SHELL_SCRIPTS))

.PHONY: help lint lint-strict lint-shell test-shell-discovery vet staticcheck vulncheck docs tools-ethos test test-integration check format build build-daemon install deploy-commands clean dist docker docker-push cover doctor prfaq clean-tex

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

lint-shell: test-shell-discovery ## Lint every shell script in the repo (shellcheck)
	@command -v shellcheck >/dev/null 2>&1 || { echo "shellcheck not found — install it (apt install shellcheck / brew install shellcheck)"; exit 1; }
	@test -n "$(SHELL_SCRIPTS)" || { echo "lint-shell: discovered no shell scripts (by shebang) — 'git ls-files' returned nothing (not a git checkout?)"; exit 1; }
	shellcheck $(SHELL_SCRIPTS)

test-shell-discovery: ## Regression test: a malicious filename must not execute during shell-script discovery (beadle-qtei)
	@tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
	( cd "$$tmp" && git init -q && \
	  printf '#!/bin/sh\necho hi\n' > 'x";touch PWNED;"y' && \
	  chmod +x 'x";touch PWNED;"y' && \
	  git ls-files --cached --others --exclude-standard -z | $(DISCOVER_SHELL_SCRIPTS) > found.txt; \
	  test ! -e PWNED || { echo "test-shell-discovery: FAIL -- a crafted filename executed as shell text (PWNED created)"; exit 1; }; \
	  grep -qF 'x";touch PWNED;"y' found.txt || { echo "test-shell-discovery: FAIL -- the crafted-but-legitimate script was not discovered"; exit 1; } ) && \
	echo "test-shell-discovery: PASS"

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
	  rm -f "$$dir/$$base.pdf"; \
	  pdflatex -interaction=nonstopmode -halt-on-error -output-directory="$$dir" "$$f" > /dev/null 2>&1 \
	    || { echo "Error: $$f failed to compile (pdflatex)" >&2; exit 1; }; \
	  if [ -f "$$dir/$$base.bcf" ]; then \
	    command -v biber > /dev/null 2>&1 \
	      || { echo "Error: $$f uses biblatex but biber is not installed" >&2; exit 1; }; \
	    (cd "$$dir" && biber "$$base") > /dev/null 2>&1 \
	      || { echo "Error: $$f failed to resolve citations (biber)" >&2; exit 1; }; \
	    pdflatex -interaction=nonstopmode -halt-on-error -output-directory="$$dir" "$$f" > /dev/null 2>&1 \
	      || { echo "Error: $$f failed to compile (pdflatex, post-biber)" >&2; exit 1; }; \
	  fi; \
	  pdflatex -interaction=nonstopmode -halt-on-error -output-directory="$$dir" "$$f" > /dev/null 2>&1 \
	    || { echo "Error: $$f failed to compile (pdflatex, final pass)" >&2; exit 1; }; \
	  echo "  $$dir/$$base.pdf"; \
	done
	@rm -f $(LATEX_ARTIFACTS)

clean-tex: ## Remove LaTeX intermediate files
	@rm -f $(LATEX_ARTIFACTS)

tools-ethos: ## Install the pinned ethos CLI (needed by internal/daemon's gate-4 tests)
	go install github.com/punt-labs/ethos/v4/cmd/ethos@$(ETHOS_VERSION)

# go install places a binary in $GOBIN if set, else $GOPATH/bin -- neither is
# guaranteed to already be on PATH. Without this, `make test` provisions
# ethos successfully but exec.LookPath("ethos") still fails: tools-ethos
# ran, the binary exists, and the remedy message points right back at the
# command that was just run. Computed once, at parse time, so every `test`
# invocation prepends the same directory `go install` actually used.
#
# GOPATH may be a colon-separated list (":"-joined on every GOOS, including
# darwin and linux -- Go never uses the OS path-list separator here). `go
# install` with no GOBIN always writes to the FIRST entry's bin/, so take
# only that entry -- appending /bin to the whole list would name a directory
# nothing was ever installed to, and would also inject a second, wrong
# directory onto PATH.
GOINSTALL_BIN := $(shell go env GOBIN)
ifeq ($(GOINSTALL_BIN),)
GOINSTALL_BIN := $(shell go env GOPATH | cut -d: -f1)/bin
endif

test: tools-ethos ## Run tests with race detection
	PATH="$(GOINSTALL_BIN):$$PATH" go test -race -count=1 ./...

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
