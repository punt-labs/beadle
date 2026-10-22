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
BIFF_VERSION := 1.19.0

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

.PHONY: help lint lint-strict lint-shell test-shell-discovery vet staticcheck vulncheck docs tools-ethos tools-biff tools-beadle-email test test-integration check format build build-daemon install deploy-commands clean dist docker docker-push cover doctor prfaq clean-tex

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

# --force overrides any `biff` binary already in the uv tool bin dir that
# uv did not install itself (e.g. a distro package or a manual copy); the
# ==$(BIFF_VERSION) pin above, combined with the skip check in the recipe,
# is what actually drives a reinstall on version drift -- --force covering
# an unrelated existing install is the rare case it guards against. An
# editable or path-installed punt-biff at the pinned version also
# satisfies the skip check below (uv's env listing names the version
# regardless of install source), so a developer's own editable checkout
# of biff, pinned to this version, is left alone rather than clobbered.
#
# Outcome table -- every path this recipe can take. Review this table,
# not the shell: the rows are not all mutually exclusive (a case can
# satisfy more than one of 4a/4b/4c's conditions at once -- see below),
# so the TIEBREAKER is the fixed check order the recipe implements: 4a
# is tested before 4b, which is tested before 4c, and the first one that
# matches is the one that prints. The recipe body implements exactly
# these rows and no others.
#
# | #  | Check            | Condition                                      | Message (stderr)                                                    | Exit          |
# |----|------------------|-------------------------------------------------|----------------------------------------------------------------------|---------------|
# | 1  | uv on PATH       | `command -v uv` fails                          | "uv not found on PATH -- install uv (...) to provision biff"         | 1             |
# | 2  | bin dir known    | `mktemp` fails                                  | "mktemp failed (check TMPDIR)"                                       | 1             |
# | 2  | bin dir known    | `uv tool dir --bin` exits nonzero, OR exits 0   | "'uv tool dir --bin' failed (exit N): <captured stderr>"             | 1             |
# |    |                  | with empty stdout                               |                                                                        |               |
# | 2  | bin dir known    | exits 0 with non-empty stdout                   | (none -- that stdout becomes the bin dir for checks 3-4 below)       | continues     |
# | 3  | skip decision    | entrypoint executable AND `uv tool list` exits  | "punt-biff vX already installed, skipping"                           | 0             |
# |    |                  | 0 AND shows exactly the pin                     |                                                                        |               |
# | 4a | reinstall reason | `uv tool list` exits nonzero                    | "'uv tool list' failed (exit N): <captured stdout and stderr> --     | install's own |
# |    |                  |                                                  | reinstalling anyway"                                                  |               |
# | 4b | reinstall reason | list exits 0 but listed version != pin          | "installed <version|<none>> differs from pin <pin>; reinstalling"    | install's own |
# | 4c | reinstall reason | list exits 0, pin IS listed, entrypoint missing | "biff entrypoint missing or not executable at <path> (pin <pin> is   | install's own |
# |    |                  | or not executable                               | listed); reinstalling"                                               |               |
# | 5  | install          | reached after any of 4a/4b/4c                   | `uv tool install`'s own output                                        | install's own |
#
# No `2>/dev/null` anywhere below: every uv call's stderr is either shown
# (check 2's failure) or captured into the shared temp file so the
# reinstall-reason messages can quote it (check 4's `uv tool list`),
# never discarded. `uv tool list`'s STDOUT is captured separately from
# its stderr (not merged with 2>&1): a uv warning that happens to
# mention "punt-biff " on stderr must never be readable as the installed
# version by rows 3/4b/4c, which classify on the stdout listing alone;
# row 4a is the only one that also quotes the captured stderr. Check 2
# runs `uv tool dir --bin` fresh at recipe time rather than trusting a
# parse-time variable; the `test` target below does the same
# independently, for the same reason -- a parse-time snapshot has no way
# to recover from a transient failure, so nothing in this Makefile reads
# one for the uv bin dir any more.
tools-biff: ## Install the pinned biff CLI if missing, unreachable, or out of pin (needed by internal/daemon's MCP registry test)
	@command -v uv >/dev/null 2>&1 || { echo "tools-biff: uv not found on PATH -- install uv (https://docs.astral.sh/uv/) to provision biff" >&2; exit 1; }; \
	err=$$(mktemp) || { echo "tools-biff: mktemp failed (check TMPDIR)" >&2; exit 1; }; \
	trap 'rm -f "$$err"' EXIT; \
	bin=$$(uv tool dir --bin 2>"$$err"); bin_status=$$?; \
	if [ $$bin_status -ne 0 ] || [ -z "$$bin" ]; then \
		echo "tools-biff: 'uv tool dir --bin' failed (exit $$bin_status):" >&2; \
		cat "$$err" >&2; \
		exit 1; \
	fi; \
	listing=$$(uv tool list 2>"$$err"); list_status=$$?; \
	if [ $$list_status -eq 0 ] && test -x "$$bin/biff" && printf '%s\n' "$$listing" | grep -qFx "punt-biff v$(BIFF_VERSION)"; then \
		echo "tools-biff: punt-biff v$(BIFF_VERSION) already installed, skipping" >&2; \
	else \
		if [ $$list_status -ne 0 ]; then \
			echo "tools-biff: 'uv tool list' failed (exit $$list_status): stdout=$$listing stderr=$$(cat "$$err") -- reinstalling anyway" >&2; \
		else \
			installed=$$(printf '%s\n' "$$listing" | grep -F 'punt-biff ' | head -1); \
			if [ "$$installed" != "punt-biff v$(BIFF_VERSION)" ]; then \
				echo "tools-biff: installed $${installed:-<none>} differs from pin punt-biff v$(BIFF_VERSION); reinstalling" >&2; \
			else \
				echo "tools-biff: biff entrypoint missing or not executable at $$bin/biff (pin punt-biff v$(BIFF_VERSION) is listed); reinstalling" >&2; \
			fi; \
		fi; \
		uv tool install --force punt-biff==$(BIFF_VERSION); \
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
	mkdir -p $(BEADLE_EMAIL_BIN)
	go build -ldflags="$(LDFLAGS)" -o $(BEADLE_EMAIL_BIN)/beadle-email ./cmd/beadle-email/

# go install places a binary in $GOBIN if set, else $GOPATH/bin -- neither is
# guaranteed to already be on PATH. Without this, `make test` provisions
# ethos successfully but exec.LookPath("ethos") still fails: tools-ethos
# ran, the binary exists, and the remedy message points right back at the
# command that was just run. Computed once, at parse time, so every `test`
# invocation prepends the same directory `go install` actually used.
# This directory IS host-global (shared by every checkout on the host),
# which is safe only because ethos is pinned to the identical
# $(ETHOS_VERSION) everywhere -- two concurrent `go install`s race to
# write the same bytes, not different ones. beadle-email's self-build
# does NOT share this directory (see BEADLE_EMAIL_BIN above): its content
# is per-branch, so two checkouts building concurrently into one shared
# directory would let either one silently run its tests against the
# OTHER checkout's binary. An earlier version of this comment argued for
# "exactly one PATH prefix" across both tools; that argument held only
# while both built into the same host-global directory, and no longer
# applies now that beadle-email has its own per-checkout one.
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

# `uv tool install` places a binary in `uv tool dir --bin` -- on this host
# ~/.local/bin, distinct from GOINSTALL_BIN above -- so biff needs its own
# PATH prefix rather than reusing the Go one. Resolved HERE, at recipe
# time, not as a parse-time variable: a parse-time `$(shell ...)` is
# evaluated once when `make` reads this file, before tools-biff has run
# and before any transient failure in `uv tool dir --bin` has a chance
# to clear, so a value captured then can go stale by the time this
# recipe actually executes -- exactly the class of bug the tools-biff
# table above exists to rule out for biff's own provisioning. Resolving
# it again here, independently, at the point this recipe runs (after
# tools-biff has already succeeded, so uv is known reachable) means a
# transient parse-time failure can no longer surface here as "biff not
# found" the way it could when this line read a parse-time variable.
test: tools-ethos tools-biff tools-beadle-email ## Run tests with race detection
	uvbin=$$(uv tool dir --bin); \
	if [ -z "$$uvbin" ]; then \
		echo "test: 'uv tool dir --bin' produced no output at recipe time -- cannot locate biff" >&2; \
		exit 1; \
	fi; \
	PATH="$(BEADLE_EMAIL_BIN):$(GOINSTALL_BIN):$$uvbin:$$PATH" go test -race -count=1 ./...

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
