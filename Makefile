.PHONY: build test lint clean generate tools

# Variables
BINARY_NAME=bot
MAIN_PATH=./cmd/bot
BUILD_DIR=build

tools: ## Install the tools needed for the project
	go install github.com/vektra/mockery/v2@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

generate: tools ## Generate all the mocks and the code for the bot
	go generate ./...

build: tools generate create-build-dir ## Build the bot
	go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)

run: tools generate ## Run the bot
	go run $(MAIN_PATH) -env=.env

create-build-dir: ## Create the build directory
	mkdir -p $(BUILD_DIR)

test: tools generate ## Run the tests
	go test -v ./...

lint: tools generate ## Lint the code
	@if ! command -v golangci-lint &> /dev/null; then \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	fi
	golangci-lint run

clean: ## Clean the build directory
	go clean
	rm -rf $(BUILD_DIR)

docker-build: ## Build the bot in a docker container
	docker build -t github.com/MihaiLupoiu/wodbuster-bot.

docker-run: ## Run the bot in a docker container
	docker run -e TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN} github.com/MihaiLupoiu/wodbuster-bot

# Help documentation à la https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html
help: ## Display this help message
	@grep -E '^[0-9a-zA-Z_-]+:.*?## .*$$' ./Makefile | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
	@echo
