FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /app/bot ./cmd/bot

# Pinned: an unpinned base means the browser under the bot can change without
# anything in this repo changing.
FROM alpine:3.22

# chromedp does not bundle a browser, and the login needs one: WodBuster's
# login is an ASP.NET WebForms page with a "remember this device" step behind
# an UpdatePanel. Everything after the login is plain HTTP.
# tzdata is not needed (the binary embeds it), but ca-certificates is: the
# whole flow is HTTPS.
RUN apk add --no-cache chromium ca-certificates

# Where chromedp will find it, so the bot does not have to guess.
ENV WODBUSTER_CHROME_PATH=/usr/bin/chromium-browser

# Fail the build, not the Sunday run, if the package ever moves the binary.
RUN test -x "$WODBUSTER_CHROME_PATH"

# Chrome is launched with --no-sandbox, so do not also hand it root.
RUN adduser -D -u 10001 bot
USER bot

WORKDIR /app
COPY --from=builder /app/bot .

CMD ["./bot"]
