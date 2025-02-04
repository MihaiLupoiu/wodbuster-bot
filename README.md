# Telegram Class Booking Bot

[![CI](https://github.com/MihaiLupoiu/wodbuster-bot/actions/workflows/ci.yml/badge.svg)](https://github.com/MihaiLupoiu/wodbuster-bot/actions/workflows/ci.yml)

A Telegram bot that allows users to book and manage fitness class schedules.

## Features

- User authentication
- Class booking by day and time
- Booking cancellation
- Weekly schedule viewing
- Automated weekly schedule notifications

## Flow

1. User starts the bot with `/start`
2. User authenticates using `/login username password`
3. Once authenticated, user can:
   - View available classes (sent automatically every Sunday)
   - Book a class using `/book day hour`
   - Cancel a booking using `/remove day hour`
   - View commands with `/help`

## Architecture

```mermaid
sequenceDiagram
    participant User
    participant Bot
    participant AuthHandler
    participant BookingHandler
    participant API

    User->>Bot: /start
    Bot->>User: Welcome message

    User->>Bot: /login username password
    Bot->>AuthHandler: Handle login
    AuthHandler->>API: Authenticate
    API->>AuthHandler: Token
    AuthHandler->>User: Login success/failure

    User->>Bot: /book Monday 10:00
    Bot->>BookingHandler: Handle booking
    BookingHandler->>AuthHandler: Check authentication
    AuthHandler->>BookingHandler: Is authenticated
    BookingHandler->>API: Book class
    API->>BookingHandler: Booking confirmation
    BookingHandler->>User: Booking success/failure

    User->>Bot: /remove Monday 10:00
    Bot->>BookingHandler: Handle removal
    BookingHandler->>AuthHandler: Check authentication
    AuthHandler->>BookingHandler: Is authenticated
    BookingHandler->>API: Cancel booking
    API->>BookingHandler: Cancellation confirmation
    BookingHandler->>User: Removal success/failure
```

## Setup

1. Get a Telegram Bot Token from BotFather
2. Set the environment variable:
   ```bash
   export TELEGRAM_BOT_TOKEN=your_token_here
   ```
3. Build and run:
   ```bash
   make build
   ./build/bot
   ```

## Development

- Run tests: `make test`
- Run linter: `make lint`
- Generate mocks: `make generate`
- Build: `make build`
- Clean: `make clean`

## Project Structure

```
.
├── cmd/
│   └── bot/              # Main application
├── internal/
│   ├── handlers/         # Command handlers
│   ├── models/           # Data models
│   └── storage/          # Data storage
├── Dockerfile           # Container definition
├── go.mod              # Go modules file
├── go.sum              # Go modules checksums
└── Makefile            # Build commands
```
