.PHONY: build test lint clean

# Variables
BINARY_NAME=bot
MAIN_PATH=./cmd/bot

build:
	go build -o $(BINARY_NAME) $(MAIN_PATH)

test:
	go test -v ./...

lint:
	@if ! command -v golangci-lint &> /dev/null; then \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	fi
	golangci-lint run

clean:
	go clean
	rm -f $(BINARY_NAME)

docker-build:
	docker build -t telegram-class-bot .

docker-run:
	docker run -e TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN} telegram-class-bot
