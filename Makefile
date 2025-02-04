.PHONY: build test lint clean generate tools

# Variables
BINARY_NAME=bot
MAIN_PATH=./cmd/bot
BUILD_DIR=build

tools:
	go install github.com/vektra/mockery/v2@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

generate: tools
	go generate ./...

build: tools generate create-build-dir
	go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)

create-build-dir:
	mkdir -p $(BUILD_DIR)

test: tools generate
	go test -v ./...

lint: tools generate
	@if ! command -v golangci-lint &> /dev/null; then \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	fi
	golangci-lint run

clean:
	go clean
	rm -rf $(BUILD_DIR)

docker-build:
	docker build -t github.com/MihaiLupoiu/wodbuster-bot.

docker-run:
	docker run -e TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN} github.com/MihaiLupoiu/wodbuster-bot
