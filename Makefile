.PHONY: build build-wodbook test lint clean generate release

# Variables
BINARY_NAME=bot
MAIN_PATH=./cmd/bot
WODBOOK_NAME=wodbook
WODBOOK_PATH=./cmd/wodbook
WODBOOK_CONFIG?=config.json
BUILD_DIR=build


generate: ## Generate all the mocks and the code for the bot
	go generate ./...
	go tool mockery

build: generate create-build-dir ## Build the bot
	go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)

build-wodbook: create-build-dir ## Build the wodbook CLI (no mocks needed)
	go build -o $(BUILD_DIR)/$(WODBOOK_NAME) $(WODBOOK_PATH)

run: generate ## Run the bot
	go run $(MAIN_PATH) -env=.env

rehearse: build-wodbook ## Full dry run of wodbook: resolves everything, books nothing
	$(BUILD_DIR)/$(WODBOOK_NAME) -config $(WODBOOK_CONFIG) -now -dry -v

create-build-dir: ## Create the build directory
	mkdir -p $(BUILD_DIR)

test: generate ## Run the tests
	go test -v ./...

lint: ## Lint the code
	go tool golangci-lint run ./...

clean: ## Clean the build directory
	go clean
	rm -rf $(BUILD_DIR)

# Usage: make release bump=patch|minor|major   (BUMP= also works)
#
# Runs the Release workflow on main: CI, then a multi-arch image pushed to
# Docker Hub, then the tag and the GitHub release. Nothing is tagged locally —
# the version is computed from the tags already on origin.
release: ## Cut a release and publish the image (bump=patch|minor|major, default patch)
	@command -v gh >/dev/null || { echo "gh is not installed: https://cli.github.com"; exit 1; }
	gh workflow run release.yml --ref main -f bump=$(or $(bump),$(BUMP),patch)
	@echo "Follow it with: gh run watch \$$(gh run list --workflow=release.yml --limit 1 --json databaseId -q '.[0].databaseId')"

docker-build: ## Build the bot image locally (same Dockerfile the release uses)
	docker build -t wodbuster-bot:dev .

docker-run: ## Run the locally built image
	docker run --rm -e TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN} wodbuster-bot:dev

docker-compose-run: ## Run the bot and MongoDB using docker-compose
	@if ! command -v docker-compose &> /dev/null; then \
		echo "Error: docker-compose is not installed. Please install Docker Compose first."; \
		exit 1; \
	fi
	docker-compose up --build

docker-compose-down: ## Stop and remove docker-compose containers
	@if ! command -v docker-compose &> /dev/null; then \
		echo "Error: docker-compose is not installed. Please install Docker Compose first."; \
		exit 1; \
	fi
	docker-compose down -v

# Help documentation à la https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html
help: ## Display this help message
	@grep -E '^[0-9a-zA-Z_-]+:.*?## .*$$' ./Makefile | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
	@echo
