package app

import (
	"errors"
	"log/slog"
	"os"
)

var (
	ErrMissingToken = errors.New("TELEGRAM_BOT_TOKEN environment variable is not set")
)

type Config struct {
	TelegramToken string
	Debug         bool
	Logger        *slog.Logger
}

func NewConfig() (*Config, error) {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		return nil, ErrMissingToken
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	return &Config{
		TelegramToken: token,
		Debug:         true,
		Logger:        logger,
	}, nil
}
