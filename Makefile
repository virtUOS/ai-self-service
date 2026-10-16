# Development tasks. `make check` runs what CI runs; run it before committing.
#
# The dev stack works with Docker or Podman. COMPOSE is `docker compose` when
# docker is installed and `podman compose` otherwise; set it to choose:
#
#   make check COMPOSE="podman compose"

COMPOSE ?= $(if $(shell command -v docker 2>/dev/null),docker compose,podman compose)
COMPOSE_FILE := dev/docker-compose.yml
# Which services `make up` starts; empty means all of them.
SERVICES ?=
LITELLM_URL := http://localhost:4000

.PHONY: check fmt vet test build e2e up down website website-assets website-serve

check: fmt vet test build e2e

fmt:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "These files need gofmt:"; echo "$$unformatted"; exit 1; \
	fi

# The e2e tests only build with their tag, so they are vetted separately.
vet:
	go vet ./...
	go vet -tags e2e ./...

test:
	go test -race ./...

# The image builds with CGO disabled; building that way here catches a
# cgo-only dependency before it reaches the registry.
build:
	CGO_ENABLED=0 go build ./...

e2e: up
	go test -tags e2e -race ./internal/litellm/ ./internal/limitsync/

# Readiness is polled here rather than left to `up --wait`, so it does not
# depend on the compose implementation supporting that flag.
up:
	$(COMPOSE) -f $(COMPOSE_FILE) up -d $(SERVICES)
	@echo "Waiting for LiteLLM (the first start takes a minute or two)..."
	@for i in $$(seq 1 90); do \
		curl -fs -o /dev/null $(LITELLM_URL)/health/readiness && exit 0; \
		sleep 2; \
	done; \
	echo "LiteLLM did not become ready; see '$(COMPOSE) -f $(COMPOSE_FILE) logs litellm'"; \
	exit 1

down:
	$(COMPOSE) -f $(COMPOSE_FILE) down

# The website and documentation (website/, built with Zensical). Not part of
# `make check`, which needs no Python; CI builds the site in its own workflow.
# The brand files are copied in from web/static and docs/brand, so there is
# one copy of each in the repository.
WEBSITE_VENV := website/.venv
ZENSICAL := $(WEBSITE_VENV)/bin/zensical
WEBSITE_ASSETS := website/docs/assets

$(ZENSICAL): website/requirements.txt
	python3 -m venv $(WEBSITE_VENV)
	$(WEBSITE_VENV)/bin/pip install -q -r website/requirements.txt
	@touch $@

website-assets:
	rm -rf $(WEBSITE_ASSETS)
	mkdir -p $(WEBSITE_ASSETS)/fonts $(WEBSITE_ASSETS)/brand
	cp web/static/fonts/* $(WEBSITE_ASSETS)/fonts/
	cp web/static/brand/*.svg $(WEBSITE_ASSETS)/brand/
	cp web/static/favicon.svg web/static/favicon.ico web/static/apple-touch-icon.png $(WEBSITE_ASSETS)/
	cp docs/brand/social-preview.png $(WEBSITE_ASSETS)/

# --strict fails the build on a broken link or a missing file.
website: $(ZENSICAL) website-assets
	cd website && ../$(ZENSICAL) build --clean --strict

website-serve: $(ZENSICAL) website-assets
	cd website && ../$(ZENSICAL) serve
