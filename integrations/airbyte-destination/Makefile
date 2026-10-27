# Local-development helpers for the Airbyte MDDB destination connector.
# All targets are invocable from this directory OR from the MDDB repo root
# via the `airbyte-*` aliases in /Makefile.

SHELL      := /bin/bash
IMAGE      := tradik/airbyte-destination-mddb
TAG        := 0.1.1
URL        ?= https://mddb.tradik.com
VENV       := .venv

.PHONY: help build push test spec check clean lock

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ { printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

build: ## Build local docker image (tags :TAG and :dev)
	docker build -t $(IMAGE):$(TAG) -t $(IMAGE):dev .

push: ## Push :TAG and :latest to registry
	docker tag $(IMAGE):$(TAG) $(IMAGE):latest
	docker push $(IMAGE):$(TAG)
	docker push $(IMAGE):latest

test: ## Run unit tests with coverage report
	@test -d $(VENV) || python3 -m venv $(VENV)
	@$(VENV)/bin/pip install -q --upgrade pip
	@$(VENV)/bin/pip install -q -r requirements.txt pytest pytest-cov
	$(VENV)/bin/pytest unit_tests/ --cov=destination_mddb --cov-report=term-missing -q

spec: ## Print connector spec
	docker run --rm $(IMAGE):$(TAG) spec | python3 -m json.tool

check: ## Run check command (URL=<mddb base url>)
	@printf '{"mddbUrl":"%s"}' "$(URL)" | \
	docker run --rm -i $(IMAGE):$(TAG) check --config /dev/stdin | \
	python3 -m json.tool

lock: ## Regenerate requirements.lock (hashes) from requirements.txt, in the image's Python
	docker run --rm -v "$$PWD":/src -w /src python:3.13-slim sh -c '\
	  pip install -q --root-user-action=ignore pip-tools && \
	  pip-compile -q --generate-hashes --allow-unsafe --strip-extras --no-header \
	    --output-file requirements.lock requirements.txt && \
	  chown $(shell id -u):$(shell id -g) requirements.lock'

clean: ## Remove local venv, caches, coverage
	rm -rf $(VENV) .pytest_cache .coverage htmlcov destination_mddb/__pycache__ unit_tests/__pycache__
