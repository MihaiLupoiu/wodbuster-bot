.PHONY: build test lint clean generate

# Variables
BINARY_NAME=bot
MAIN_PATH=./cmd/bot
BUILD_DIR=build

generate:
	go generate ./...

build: generate create-build-dir
	go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)

create-build-dir:
	mkdir -p $(BUILD_DIR)

test: generate
	go test -v ./...

lint: generate
	@if ! command -v golangci-lint &> /dev/null; then \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	fi
	golangci-lint run

clean:
	go clean
	rm -rf $(BUILD_DIR)

docker-build:
	docker build -t telegram-class-bot .

docker-run:
	docker run -e TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN} telegram-class-bot
