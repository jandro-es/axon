# =============================================================================
# AXON — build, install, and update.
#
#   make            show this help
#   make doctor     check dependencies (and how to install any that are missing)
#   make build      build the single binary (embeds the dashboard)
#   make setup      full install: binary + config + Ollama + daemon at login
#   make update     update an existing install (binary, DB, dashboard, daemon)
#
# Every target is self-documenting — run `make help`. Set NO_COLOR=1 for plain
# output. Pass extra installer flags with ARGS, e.g. `make setup ARGS=--no-ollama`.
# =============================================================================

BINARY  := axon
PKG     := ./cmd/axon
PREFIX  ?= /usr/local
ARGS    ?=

# Build metadata stamped into the binary (shown by `axon version`).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
# Dev builds keep symbols (debuggable with delve); release builds strip them.
LDFLAGS         := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
RELEASE_LDFLAGS := -s -w $(LDFLAGS)

# Cross-platform release matrix (pure-Go SQLite → CGO off → clean cross-compile).
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

# ── OS-aware dispatch for the daemon lifecycle targets ──────────────────────
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
  OS := macos
  INSTALL_SCRIPT   := scripts/install-macos.sh
  UPDATE_SCRIPT    := scripts/update-macos.sh
  UNINSTALL_SCRIPT := scripts/uninstall-macos.sh
else ifeq ($(UNAME_S),Linux)
  OS := linux
  INSTALL_SCRIPT   := scripts/install-linux.sh
  UPDATE_SCRIPT    := scripts/update-linux.sh
  UNINSTALL_SCRIPT := scripts/uninstall-linux.sh
else
  OS := other
endif

# ── Colours (disabled with NO_COLOR=1, TERM=dumb, or an unset TERM) ─────────
COLOR := yes
ifdef NO_COLOR
  COLOR := no
endif
ifeq ($(TERM),dumb)
  COLOR := no
endif
ifeq ($(TERM),)
  COLOR := no
endif
ifeq ($(COLOR),yes)
  C_RESET := \033[0m
  C_BOLD  := \033[1m
  C_DIM   := \033[2m
  C_RED   := \033[31m
  C_GREEN := \033[32m
  C_YELLOW:= \033[33m
  C_CYAN  := \033[36m
else
  C_RESET :=
  C_BOLD  :=
  C_DIM   :=
  C_RED   :=
  C_GREEN :=
  C_YELLOW:=
  C_CYAN  :=
endif

# require_tool TOOL, PURPOSE — fail a recipe early with a pointer to `make doctor`.
define require_tool
command -v $(1) >/dev/null 2>&1 || { printf '$(C_RED)✗ %s not found$(C_RESET) — %s\n' '$(1)' "$(2)"; printf '  run $(C_BOLD)make doctor$(C_RESET) for install instructions\n'; exit 1; }
endef

.DEFAULT_GOAL := help
.PHONY: help all web binary build release test race cover vet fmt fmtcheck lint tidy check \
        doctor install setup update reload uninstall clean version run

help: ## show this help
	@printf '$(C_BOLD)AXON$(C_RESET) — local-first AI operating system for an Obsidian vault\n'
	@printf '$(C_DIM)version %s · %s/%s$(C_RESET)\n\n' '$(VERSION)' '$(OS)' '$(shell uname -m)'
	@printf 'Usage: $(C_CYAN)make$(C_RESET) $(C_BOLD)<target>$(C_RESET) [VAR=value]\n'
	@awk 'BEGIN {FS = ":.*?## "} \
	  /^##@/ {printf "\n$(C_BOLD)%s$(C_RESET)\n", substr($$0, 5); next} \
	  /^[a-zA-Z0-9_.-]+:.*?## / {printf "  $(C_CYAN)%-14s$(C_RESET) %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@printf '\n$(C_DIM)Tips: NO_COLOR=1 for plain output · pass installer flags via ARGS=…$(C_RESET)\n'

##@ Build
all: web binary ## build the dashboard SPA and the binary

web: ## build the dashboard SPA (needs Node; skipped with a notice if absent)
	@command -v npm >/dev/null 2>&1 || { printf '$(C_YELLOW)⚠ Node/npm not found$(C_RESET) — skipping dashboard build (the binary serves a fallback page)\n  install Node to get the full dashboard; see $(C_BOLD)make doctor$(C_RESET)\n'; exit 0; }
	@printf '$(C_DIM)building dashboard SPA (web/)…$(C_RESET)\n'
	cd web && npm install && npm run build
	@printf '$(C_GREEN)✓$(C_RESET) dashboard built (web/dist)\n'

binary: ## build the Go binary (embeds web/dist)
	@$(call require_tool,go,required to build the binary)
	@printf '$(C_DIM)compiling $(BINARY) $(VERSION)…$(C_RESET)\n'
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)
	@printf '$(C_GREEN)✓$(C_RESET) built ./$(BINARY) ($(VERSION))\n'

build: binary ## alias for `binary`

release: web ## cross-compile versioned binaries into dist/ (all platforms)
	@$(call require_tool,go,required to build releases)
	@mkdir -p dist
	@for pair in $(PLATFORMS); do \
	  os=$${pair%/*}; arch=$${pair#*/}; \
	  out=dist/$(BINARY)_$(VERSION)_$${os}_$${arch}; \
	  printf '$(C_DIM)→ %s/%s$(C_RESET)\n' $$os $$arch; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -ldflags "$(RELEASE_LDFLAGS)" -o $$out $(PKG) || exit 1; \
	done
	@cd dist && shasum -a 256 $(BINARY)_* > checksums.txt
	@printf '$(C_GREEN)✓$(C_RESET) release binaries in dist/\n'; ls -1 dist/

##@ Quality
test: ## run the test suite
	go test ./...

race: ## run the test suite with the race detector
	go test -race ./...

cover: ## print per-package coverage
	go test -cover ./...

vet: ## run go vet
	go vet ./...

fmt: ## format all Go code
	gofmt -w .

fmtcheck: ## fail if any file is not gofmt-clean
	@out="$$(gofmt -l .)"; test -z "$$out" || { printf '$(C_RED)✗ gofmt needed:$(C_RESET)\n%s\n' "$$out"; exit 1; }

lint: ## run golangci-lint if installed (no-op with a notice otherwise)
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || printf '$(C_YELLOW)⚠ golangci-lint not installed$(C_RESET) — skipping (https://golangci-lint.run)\n'

tidy: ## tidy module dependencies
	go mod tidy

check: fmtcheck vet test ## fast pre-commit gate: fmtcheck + vet + test

##@ Install & Update
doctor: ## check build + runtime dependencies (and how to install them)
	@scripts/preflight.sh --all

install: build ## build + install just the binary to $(PREFIX)/bin (no daemon)
	@install -d $(PREFIX)/bin 2>/dev/null || sudo install -d $(PREFIX)/bin
	@install -m 0755 $(BINARY) $(PREFIX)/bin/$(BINARY) 2>/dev/null || sudo install -m 0755 $(BINARY) $(PREFIX)/bin/$(BINARY)
	@printf '$(C_GREEN)✓$(C_RESET) installed $(PREFIX)/bin/$(BINARY)\n'
	@case ":$$PATH:" in *":$(PREFIX)/bin:"*) : ;; *) printf '$(C_YELLOW)⚠$(C_RESET) $(PREFIX)/bin is not on your PATH — add it to your shell profile\n' ;; esac
	@printf '$(C_DIM)next: `make setup` for the full daemon install, or `axon init` to provision a profile$(C_RESET)\n'

setup: ## full install: binary + config + Ollama + daemon at login
ifeq ($(OS),other)
	@printf '$(C_YELLOW)⚠ automated setup supports macOS and Linux.$(C_RESET)\n'
	@printf '  On $(UNAME_S), build + install the binary then provision manually:\n'
	@printf '    $(C_CYAN)make install$(C_RESET)\n'
	@printf '    $(C_CYAN)axon init$(C_RESET)                 # scaffold the profile + DB\n'
	@printf '    $(C_CYAN)axon service install$(C_RESET)      # emit an OS service unit (Task Scheduler on Windows)\n'
else
	@PREFIX=$(PREFIX) $(INSTALL_SCRIPT) $(ARGS)
endif

update: ## update an existing install (binary, DB, dashboard, daemon)
ifeq ($(OS),other)
	@printf '$(C_YELLOW)⚠ automated update supports macOS and Linux.$(C_RESET)\n'
	@printf '  On $(UNAME_S): $(C_CYAN)make install$(C_RESET) then $(C_CYAN)axon init$(C_RESET), and restart your service.\n'
else
	@PREFIX=$(PREFIX) $(UPDATE_SCRIPT) $(ARGS)
endif

reload: ## restart the installed daemon so a new build takes effect
ifeq ($(OS),macos)
	@p=$$(axon config get active_profile 2>/dev/null || echo personal); \
	 plist="$$HOME/Library/LaunchAgents/com.axon.$$p.plist"; \
	 if [ -f "$$plist" ]; then launchctl unload "$$plist" 2>/dev/null || true; launchctl load -w "$$plist" && printf '$(C_GREEN)✓$(C_RESET) reloaded com.axon.%s\n' "$$p"; \
	 else printf '$(C_YELLOW)⚠$(C_RESET) no launchd agent for profile %s — run `make setup` first\n' "$$p"; fi
else ifeq ($(OS),linux)
	@p=$$(axon config get active_profile 2>/dev/null || echo personal); \
	 systemctl --user restart "axon-$$p.service" && printf '$(C_GREEN)✓$(C_RESET) restarted axon-%s\n' "$$p" \
	   || printf '$(C_YELLOW)⚠$(C_RESET) could not restart axon-%s — run `make setup` first\n' "$$p"
else
	@printf '$(C_YELLOW)⚠$(C_RESET) reload is automated on macOS/Linux only; restart your service manually\n'
endif

uninstall: ## remove the daemon + binary (keeps ~/.axon; ARGS=--purge to delete it)
ifeq ($(OS),other)
	@printf '$(C_YELLOW)⚠ automated uninstall supports macOS and Linux.$(C_RESET) Remove $(PREFIX)/bin/$(BINARY) and your service unit manually.\n'
else
	@PREFIX=$(PREFIX) $(UNINSTALL_SCRIPT) $(ARGS)
endif

##@ Utility
version: ## print the build metadata that will be stamped in
	@printf 'version  $(C_BOLD)%s$(C_RESET)\ncommit   %s\ndate     %s\nos/arch  %s/%s\n' \
	  '$(VERSION)' '$(COMMIT)' '$(DATE)' '$(OS)' '$(shell uname -m)'

run: binary ## build and run the daemon locally (Ctrl-C to stop)
	./$(BINARY) start $(ARGS)

clean: ## remove build artifacts (keeps web/dist as an embeddable placeholder)
	rm -f $(BINARY)
	rm -rf dist web/node_modules
	rm -rf web/dist/assets web/dist/index.html
	@mkdir -p web/dist && touch web/dist/.gitkeep
	@printf '$(C_GREEN)✓$(C_RESET) cleaned\n'
