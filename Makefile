# AXON — build entry points.
#
# The daemon builds with plain `go build`; these targets exist mainly so the
# macOS Companion app (apps/companion) has the same one-word workflow, and so
# CI and a human run identical commands.

COMPANION := apps/companion

.PHONY: help build test dashboard companion companion-test companion-run \
        companion-release companion-appcast clean

help: ## Show this help
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) \
	  | awk -F':.*?## ' '{printf "  \033[1m%-20s\033[0m %s\n", $$1, $$2}'

# ── daemon ──────────────────────────────────────────────────────────────────

dashboard: ## Build the dashboard SPA into web/dist (embedded by go build)
	cd web && npm ci && npm run build

build: dashboard ## Build the axon binary with the SPA embedded
	go build ./cmd/axon

test: ## Run the Go test suite with the race detector
	go test -race ./...

# ── companion (macOS only) ──────────────────────────────────────────────────

companion: ## Build and launch the Companion menu bar app (dev loop)
	$(COMPANION)/Scripts/compile_and_run.sh

companion-test: ## Run the AxonKit test suite
	cd $(COMPANION) && swift test

companion-run: companion ## Alias for `companion`

companion-release: ## Build, Developer ID sign, notarize and staple Companion
	$(COMPANION)/Scripts/sign-and-notarize.sh

companion-appcast: ## Sign the Sparkle appcast for the current release zip
	$(COMPANION)/Scripts/make_appcast.sh

clean: ## Remove build output
	rm -rf $(COMPANION)/.build $(COMPANION)/dist $(COMPANION)/build
	rm -f $(COMPANION)/Icon.icns
	go clean ./...
