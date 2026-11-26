VERSION := $(or $(shell git describe --tags --always 2>/dev/null | sed 's/^v//'),dev)
LDFLAGS := -X main.version=$(VERSION)

# Pinned once here. CI invokes these via `make vet`, `make staticcheck`,
# `make lint-strict`, and `make vulncheck` rather than duplicating the
# versioned commands in .github/workflows/test.yml, so local and CI can
# never drift apart.
STATICCHECK_VERSION := v0.8.1
GOLANGCI_LINT_VERSION := v2.12.2
GOVULNCHECK_VERSION := v1.7.0
MARKDOWNLINT_VERSION := 0.24.2
ETHOS_VERSION := v4.16.0
BIFF_VERSION := 1.24.2

# Pin the Go toolchain to the go.mod `toolchain` line for every `go`
# invocation. GOTOOLCHAIN=auto uses the NEWER of (host Go, go.mod directive),
# so a host running a Go release ahead of the pin (e.g. 1.27) would run the
# pinned analyzers (staticcheck, golangci-lint) under that newer Go — and they
# cannot decode its export-data format, crashing with "export data version N is
# greater than maximum supported version M". Forcing the pinned toolchain keeps
# the analyzers and the compiler that built the stdlib they read in lockstep,
# identically on every host and in CI.
export GOTOOLCHAIN := go1.26.11

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

.PHONY: help lint lint-strict lint-shell test-shell-discovery test-tools-biff probe-test-path vet staticcheck vulncheck docs tools-ethos tools-biff tools-beadle-email test test-integration check handoff-runtime-bundle handoff-runtime-inputs handoff-runtime-verify format build build-daemon install deploy-commands clean dist docker docker-push cover doctor prfaq clean-tex

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

lint: lint-shell vet staticcheck ## Lint (gofmt + go vet + staticcheck + shellcheck)
	@test -z "$$(gofmt -s -l ./cmd/ ./internal/ 2>/dev/null)" || { echo "gofmt -s: these files need formatting:"; gofmt -s -l ./cmd/ ./internal/; exit 1; }

vet: ## Run go vet
	go vet ./...

# Offline snapshots use the same versioned commands, built and verified when
# packaging the runtime. A missing version must fail, not fall back to PATH.
go-tool = $(if $(ETHOS_HANDOFF_RUNTIME),"$(ETHOS_HANDOFF_RUNTIME)/go-tools/$(notdir $(1))",go run $(1))

staticcheck: ## Run staticcheck alone (also part of `lint`)
	$(call go-tool,honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)) ./...

lint-strict: ## Lint (golangci-lint: errcheck, gosec, revive, gofumpt, ...)
	$(call go-tool,github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)) run ./...

vulncheck: ## Scan imports + call graph for known Go vulnerabilities
	$(call go-tool,golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)) \
	  $(if $(ETHOS_HANDOFF_RUNTIME),-db "file://$(ETHOS_HANDOFF_RUNTIME)/data/govulndb") ./...

lint-shell: test-shell-discovery test-tools-biff ## Lint every shell script in the repo (shellcheck)
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

test-tools-biff: ## Regression test: reuse biff on PATH and install the pin only when absent
	@set -e; \
	make=$$(command -v "$(MAKE_COMMAND)"); \
	mkdir -p "$(CURDIR)/.tmp"; \
	tmp=$$(mktemp -d "$(CURDIR)/.tmp/test-tools-biff.XXXXXX"); trap 'rm -rf "$$tmp"' EXIT; \
	for tool in sed xargs sh grep printenv; do ln -s "$$(command -v "$$tool")" "$$tmp/$$tool"; done; \
	test_path_for() { \
		PATH="$$1" MAKEFLAGS= "$$make" --no-print-directory \
		  BEADLE_EMAIL_BIN="$$tmp/beadle-bin" TEST_ETHOS_BIN="$$tmp/ethos-bin" probe-test-path; \
	}; \
	mkdir "$$tmp/nonexec"; \
	printf '#!/bin/sh\nexit 0\n' > "$$tmp/nonexec/biff"; \
	printf '#!/bin/sh\nexit 0\n' > "$$tmp/biff"; \
	printf '%s\n' '#!/bin/sh' \
	  'printf "%s\n" "$$*" >> "$${0%/*}/calls"' \
	  'case "$$*" in' \
	  '  "tool dir --bin") printf "%s\n" "$${0%/*}/cached" ;;' \
	  '  "tool list") printf "punt-biff v%s\n" "$(BIFF_VERSION)" ;;' \
	  '  *) printf "%s\n" "$$*" ;;' 'esac' > "$$tmp/uv"; \
	chmod +x "$$tmp/biff" "$$tmp/uv"; \
	PATH="$$tmp/nonexec:$$tmp" MAKEFLAGS= "$$make" --no-print-directory BIFF_VERSION="$(BIFF_VERSION)" tools-biff; \
	selected=$$(test_path_for "$$tmp/nonexec:$$tmp"); \
	test "$$selected" = "$$tmp/beadle-bin:$$tmp/ethos-bin:$$tmp/nonexec:$$tmp" || { echo "test-tools-biff: unexpectedly changed PATH with executable biff present: $$selected" >&2; exit 1; }; \
	test ! -e "$$tmp/calls" || { echo "test-tools-biff: unexpectedly ran uv with executable biff on PATH" >&2; exit 1; }; \
	rm "$$tmp/biff"; \
	calls=$$(PATH="$$tmp" MAKEFLAGS= "$$make" --no-print-directory BIFF_VERSION="$(BIFF_VERSION)" tools-biff); \
	test "$$calls" = "tool install --force punt-biff==$(BIFF_VERSION)" || { echo "test-tools-biff: unexpected fallback command: $$calls" >&2; exit 1; }; \
	mkdir "$$tmp/cached"; \
	printf '#!/bin/sh\nexit 0\n' > "$$tmp/cached/biff"; chmod +x "$$tmp/cached/biff"; \
	selected=$$(test_path_for "$$tmp"); \
	test "$$selected" = "$$tmp/beadle-bin:$$tmp/ethos-bin:$$tmp:$$tmp/cached" || { echo "test-tools-biff: did not append the fallback directory without biff on PATH: $$selected" >&2; exit 1; }; \
	rm "$$tmp/calls"; \
	calls=$$(PATH="$$tmp/nonexec:$$tmp" MAKEFLAGS= "$$make" --no-print-directory BIFF_VERSION="$(BIFF_VERSION)" tools-biff); \
	test -s "$$tmp/calls" || { echo "test-tools-biff: treated a non-executable biff as installed" >&2; exit 1; }; \
	grep -qFx 'tool list' "$$tmp/calls" || { echo "test-tools-biff: reused the cached fallback without checking its version" >&2; exit 1; }; \
	test -z "$$calls" || { echo "test-tools-biff: unexpectedly reinstalled the cached fallback: $$calls" >&2; exit 1; }; \
	selected=$$(test_path_for "$$tmp/nonexec:$$tmp"); \
	test "$$selected" = "$$tmp/beadle-bin:$$tmp/ethos-bin:$$tmp/nonexec:$$tmp:$$tmp/cached" || { echo "test-tools-biff: did not append the fallback directory with non-executable biff on PATH: $$selected" >&2; exit 1; }; \
	echo "test-tools-biff: PASS"

docs: ## Lint markdown
	npx --yes markdownlint-cli2@$(MARKDOWNLINT_VERSION) "**/*.md" "#node_modules"

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

TEST_ETHOS_BIN := $(CURDIR)/.tmp/bin

tools-ethos: ## Install the pinned test ethos without replacing the user's CLI
	mkdir -p "$(TEST_ETHOS_BIN)"
ifneq ($(ETHOS_HANDOFF_RUNTIME),)
	@tmp=$$(mktemp "$(TEST_ETHOS_BIN)/ethos.XXXXXX") && \
	  trap 'rm -f -- "$$tmp"' EXIT && \
	  install -m 0755 "$(ETHOS_HANDOFF_RUNTIME)/go-tools/ethos@$(ETHOS_VERSION)" "$$tmp" && \
	  mv -fT -- "$$tmp" "$(TEST_ETHOS_BIN)/ethos"
else
	GOBIN="$(TEST_ETHOS_BIN)" go install github.com/punt-labs/ethos/v4/cmd/ethos@$(ETHOS_VERSION)
endif

define BIFF_ON_PATH
	biff_on_path() ( \
		path=$$PATH; \
		while :; do \
			dir=$${path%%:*}; dir=$${dir:-.}; \
			if [ -f "$$dir/biff" ] && [ -x "$$dir/biff" ]; then return 0; fi; \
			case "$$path" in \
				*:*) path=$${path#*:} ;; \
				*) return 1 ;; \
			esac; \
		done \
	)
endef

tools-biff: ## Reuse biff on PATH or install the pinned fallback for tests
	@$(BIFF_ON_PATH); \
	if biff_on_path; then \
		echo "tools-biff: biff already on PATH, skipping" >&2; \
	else \
		command -v uv >/dev/null 2>&1 || { echo "tools-biff: uv not found on PATH -- install uv (https://docs.astral.sh/uv/) to provision biff" >&2; exit 1; }; \
		uvbin=$$(uv tool dir --bin); uvbin_status=$$?; \
		if [ $$uvbin_status -ne 0 ] || [ -z "$$uvbin" ]; then \
			echo "tools-biff: 'uv tool dir --bin' failed (exit $$uvbin_status) or produced no output (stdout: $$uvbin)" >&2; \
			exit 1; \
		fi; \
		if [ -f "$$uvbin/biff" ] && [ -x "$$uvbin/biff" ] && listing=$$(uv tool list) && printf '%s\n' "$$listing" | grep -qFx "punt-biff v$(BIFF_VERSION)"; then \
			echo "tools-biff: pinned fallback already installed, skipping" >&2; \
		else \
			uv tool install --force punt-biff==$(BIFF_VERSION); \
		fi; \
	fi

# Per-checkout, not host-global: tools-beadle-email self-builds THIS
# checkout's own branch content, so its output directory must not be
# shared with any other checkout. $(CURDIR) (not a bare relative ".tmp/bin")
# because `go test` runs each package's test binary chdir'd into that
# package's own source directory, not the repo root -- a relative PATH
# entry would resolve against the WRONG directory inside the test
# subprocess. Gitignored already (.tmp/* in .gitignore); mkdir -p below
# creates it on demand.
BEADLE_EMAIL_BIN := $(CURDIR)/.tmp/bin

tools-beadle-email: ## Self-build this branch's beadle-email into BEADLE_EMAIL_BIN (same test)
	mkdir -p "$(BEADLE_EMAIL_BIN)"
	go build -ldflags="$(LDFLAGS)" -o "$(BEADLE_EMAIL_BIN)/beadle-email" ./cmd/beadle-email/

# uv may install the fallback outside PATH. Add its bin directory at
# recipe time only when biff still isn't on PATH.
TEST_PREREQS := tools-ethos tools-biff tools-beadle-email

define TEST_RUN
	$(BIFF_ON_PATH); \
	test_path="$(BEADLE_EMAIL_BIN):$(TEST_ETHOS_BIN):$$PATH"; \
	if ! biff_on_path; then \
		uvbin=$$(uv tool dir --bin); uvbin_status=$$?; \
		if [ $$uvbin_status -ne 0 ] || [ -z "$$uvbin" ]; then \
			echo "$@: 'uv tool dir --bin' failed (exit $$uvbin_status) or produced no output (stdout: $$uvbin) -- cannot build the PATH that carries biff" >&2; \
			exit 1; \
		fi; \
		test_path="$$test_path:$$uvbin"; \
	fi; \
	PATH="$$test_path" $(1)
endef

probe-test-path:
	@$(call TEST_RUN,printenv PATH)

test: $(TEST_PREREQS) ## Run tests with race detection
	$(call TEST_RUN,go test -race -count=1 ./...)

test-integration: $(TEST_PREREQS) ## Run integration tests (in-process IMAP/SMTP)
	$(call TEST_RUN,go test -race -count=1 -tags=integration ./...)

check: handoff-runtime-verify lint lint-strict vulncheck docs test ## Run all quality gates

handoff-runtime-inputs:
	@sha256sum go.mod go.sum && printf '%s\n' \
	  "$(GOTOOLCHAIN) $(STATICCHECK_VERSION) $(GOLANGCI_LINT_VERSION) $(GOVULNCHECK_VERSION) $(MARKDOWNLINT_VERSION) $(ETHOS_VERSION) $(BIFF_VERSION)"

handoff-runtime-verify:
ifneq ($(ETHOS_HANDOFF_RUNTIME),)
	@inputs=$$($(MAKE) --no-print-directory --silent handoff-runtime-inputs) && \
	  printf '%s\n' "$$inputs" | cmp -s - "$(ETHOS_HANDOFF_RUNTIME)/inputs" || { \
	  echo 'Handoff runtime does not match go.mod, go.sum, or Makefile tool pins; build and pin a new bundle.' >&2; exit 1; }
endif

handoff-runtime-bundle: ## Build an Ethos offline runtime archive in .tmp/
	mkdir -p .tmp
	inputs=$$($(MAKE) --no-print-directory --silent handoff-runtime-inputs) && printf '%s\n' "$$inputs" | \
	  bash scripts/build-handoff-runtime.sh .tmp "$(MARKDOWNLINT_VERSION)" "$(BIFF_VERSION)" \
	  honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) \
	  github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) \
	  golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) \
	  github.com/punt-labs/ethos/v4/cmd/ethos@$(ETHOS_VERSION)

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
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -ldflags="-s -w $(LDFLAGS)" -o dist/beadle-daemon-darwin-arm64 ./cmd/beadle-daemon/
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -ldflags="-s -w $(LDFLAGS)" -o dist/beadle-daemon-darwin-amd64 ./cmd/beadle-daemon/
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -ldflags="-s -w $(LDFLAGS)" -o dist/beadle-daemon-linux-arm64  ./cmd/beadle-daemon/
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -ldflags="-s -w $(LDFLAGS)" -o dist/beadle-daemon-linux-amd64  ./cmd/beadle-daemon/
	cd dist && if command -v sha256sum >/dev/null 2>&1; then sha256sum beadle-email-darwin-arm64 beadle-email-darwin-amd64 beadle-email-linux-arm64 beadle-email-linux-amd64 beadle-daemon-darwin-arm64 beadle-daemon-darwin-amd64 beadle-daemon-linux-arm64 beadle-daemon-linux-amd64 > checksums.txt; else shasum -a 256 beadle-email-darwin-arm64 beadle-email-darwin-amd64 beadle-email-linux-arm64 beadle-email-linux-amd64 beadle-daemon-darwin-arm64 beadle-daemon-darwin-amd64 beadle-daemon-linux-arm64 beadle-daemon-linux-amd64 > checksums.txt; fi

docker: ## Build Docker image
	docker build --build-arg VERSION=$(VERSION) -t ghcr.io/punt-labs/beadle-email:latest .

docker-push: docker ## Push Docker image to ghcr.io
	docker push ghcr.io/punt-labs/beadle-email:latest

cover: $(TEST_PREREQS) ## Test with coverage report
	$(call TEST_RUN,go test -cover -coverprofile=coverage.out ./...)
	go tool cover -func=coverage.out

doctor: build ## Run beadle-email doctor
	./beadle-email doctor
