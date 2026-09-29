FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /app/bot ./cmd/bot

FROM alpine:latest

# chromedp does not bundle a browser, and the login needs one: WodBuster's
# login is an ASP.NET WebForms page with a "remember this device" step behind
# an UpdatePanel. Everything after the login is plain HTTP.
# tzdata is not needed (the binary embeds it), but ca-certificates is: the
# whole flow is HTTPS.
RUN apk add --no-cache chromium ca-certificates

# Where chromedp will find it, so the bot does not have to guess.
ENV WODBUSTER_CHROME_PATH=/usr/bin/chromium-browser

WORKDIR /app
COPY --from=builder /app/bot .

CMD ["./bot"]
