.PHONY: build test lint clean generate tools

# Variables
BINARY_NAME=bot
MAIN_PATH=./cmd/bot
BUILD_DIR=build

tools: ## Install the tools needed for the project
	@if ! command -v mockery &> /dev/null; then \
		echo "Installing mockery..." && \
		go install github.com/vektra/mockery/v2@latest; \
	fi
	@if ! command -v golangci-lint &> /dev/null; then \
		echo "Installing golangci-lint..." && \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	fi

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

lint: tools ## Lint the code
	golangci-lint run

clean: ## Clean the build directory
	go clean
	rm -rf $(BUILD_DIR)

docker-build: ## Build the bot in a docker container
	docker build -t github.com/MihaiLupoiu/wodbuster-bot .

docker-run: ## Run the bot in a docker container
	docker run -e TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN} github.com/MihaiLupoiu/wodbuster-bot

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
