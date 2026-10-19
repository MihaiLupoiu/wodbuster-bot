package logging

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

const token = "1812436851:AAG-not-a-real-token-at-all"

func logger(buf *bytes.Buffer, secrets ...string) *slog.Logger {
	return slog.New(NewHandler(slog.NewJSONHandler(buf, nil), NewRedactor(secrets...)))
}

// What the Telegram library logged on 2026-10-06 when a long poll dropped: the
// request URL, token included, wrapped in a *url.Error.
func telegramError() error {
	return &url.Error{
		Op:  "Post",
		URL: "https://api.telegram.org/bot" + token + "/getUpdates",
		Err: errors.New("read: connection reset by peer"),
	}
}

func TestTheTokenNeverReachesTheOutput(t *testing.T) {
	for name, log := range map[string]func(*slog.Logger){
		"in the message":        func(l *slog.Logger) { l.Info("calling bot" + token) },
		"as a string attribute": func(l *slog.Logger) { l.Info("x", "url", "https://api.telegram.org/bot"+token) },
		"inside an error":       func(l *slog.Logger) { l.Error("x", "error", telegramError()) },
		"inside a wrapped error": func(l *slog.Logger) {
			l.Error("x", "error", fmt.Errorf("failed to create Telegram bot: %w", telegramError()))
		},
		"in a group":         func(l *slog.Logger) { l.Info("x", slog.Group("req", "url", token)) },
		"in With attributes": func(l *slog.Logger) { l.With("url", token).Info("x") },
		"through the Telegram library's logger": func(l *slog.Logger) {
			PrintLogger{Log: l, Level: slog.LevelWarn, Msg: "telegram api"}.Println(telegramError())
		},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			log(logger(&buf, token))
			assert.NotContains(t, buf.String(), token)
			assert.Contains(t, buf.String(), Redacted)
		})
	}
}

func TestOrdinaryValuesAreLeftAlone(t *testing.T) {
	var buf bytes.Buffer
	logger(&buf, token).Info("booking outcome", "chat_id", int64(7), "took", 3, "class", "Monday 07:00 Wod")
	assert.Contains(t, buf.String(), `"chat_id":7`, "numbers stay numbers")
	assert.Contains(t, buf.String(), `"class":"Monday 07:00 Wod"`)
	assert.NotContains(t, buf.String(), Redacted)
}

func TestShortOrEmptySecretsAreIgnored(t *testing.T) {
	var buf bytes.Buffer
	logger(&buf, "", "abc").Info("abc and more")
	assert.Contains(t, buf.String(), "abc and more")
}

func TestURIPassword(t *testing.T) {
	assert.Equal(t, "s3cret-pass", URIPassword("mongodb+srv://bot:s3cret-pass@cluster.example.net/?retryWrites=true"))
	assert.Equal(t, "", URIPassword("mongodb://localhost:27017"))
	assert.Equal(t, "", URIPassword("::not a uri"))
}
