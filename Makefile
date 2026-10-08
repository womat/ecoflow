BINARY_NAME=ecoflowd

# Branch policy: there is one branch, RELEASE_BRANCH; work reaches it through pull
# requests and releases are tags on it. The release target only tags a clean
# RELEASE_BRANCH that matches origin, because a tag on a commit that never reached it
# produces artifacts that no longer match what the repository publishes.
RELEASE_BRANCH ?= main

GREEN  := $(shell tput -Txterm setaf 2)
YELLOW := $(shell tput -Txterm setaf 3)
RESET  := $(shell tput -Txterm sgr0)

.PHONY: all build test lint golden image snapshot release clean help

all: help

build: ## build ./$(BINARY_NAME) for this machine
	CGO_ENABLED=0 go build -trimpath -o $(BINARY_NAME) ./cmd/ecoflowd

test: ## run all tests with the race detector, and the decoder and script self-checks
	go test -race ./...
	@for c in fast slow damaged; do \
		python3 scripts/ecoflow-frames.py < internal/frames/testdata/$$c.txt \
			| diff -u internal/frames/testdata/$$c.golden - || exit 1; \
	done
	scripts/ecoflow-api.sh selftest

lint: ## gofmt, go vet and govulncheck
	@test -z "$$(gofmt -l ./cmd ./internal)" || { echo "not gofmt'ed:"; gofmt -l ./cmd ./internal; exit 1; }
	go vet ./...
	go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...

golden: ## regenerate the .golden files with the python decoder - after a change to either decoder
	@for c in fast slow damaged; do \
		python3 scripts/ecoflow-frames.py < internal/frames/testdata/$$c.txt > internal/frames/testdata/$$c.golden; \
	done

image: ## build the container image for this machine as $(BINARY_NAME):dev
	docker build -t $(BINARY_NAME):dev .

snapshot: ## build all release archives into ./dist without publishing (needs goreleaser)
	goreleaser release --snapshot --clean

clean: ## remove build output
	rm -fr ./dist ./$(BINARY_NAME)

release: ## tag $(RELEASE_BRANCH) and push it, triggering the GitHub release workflow (make release TAG=v0.7.0)
	@test -n "$(TAG)" || { echo "usage: make release TAG=v0.7.0"; exit 1; }
	@echo "$(TAG)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "TAG must be semver with a v prefix, e.g. v0.7.0"; exit 1; }
	@git diff --quiet HEAD || { echo "working tree is dirty, commit first"; exit 1; }
	@test "$$(git rev-parse --abbrev-ref HEAD)" = "$(RELEASE_BRANCH)" || \
		{ echo "releases are cut from $(RELEASE_BRANCH), but you are on $$(git rev-parse --abbrev-ref HEAD) - run: git checkout $(RELEASE_BRANCH)"; exit 1; }
	@git fetch origin --quiet
	@test "$$(git rev-parse HEAD)" = "$$(git rev-parse origin/$(RELEASE_BRANCH))" || \
		{ echo "$(RELEASE_BRANCH) and origin/$(RELEASE_BRANCH) differ - pull or push first"; exit 1; }
	git tag -a $(TAG) -m "release $(TAG)"
	git push origin $(TAG)
	@echo "Tag pushed. Watch the release build with: gh run watch"

help: ## Show this help.
	@echo ''
	@echo 'Usage:'
	@echo '  ${YELLOW}make${RESET} ${GREEN}<target>${RESET}'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[0-9a-zA-Z_-]+:.*?## / {printf "${YELLOW}%-10s${GREEN}%s${RESET}\n", $$1, $$2}' $(MAKEFILE_LIST) \
		| sed -e 's|[$$][(]RELEASE_BRANCH[)]|$(RELEASE_BRANCH)|g' -e 's|[$$][(]BINARY_NAME[)]|$(BINARY_NAME)|g'
