FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /app/bot ./cmd/bot

FROM alpine:latest

WORKDIR /app
COPY --from=builder /app/bot .

CMD ["./bot"]
