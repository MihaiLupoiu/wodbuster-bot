package app

import (
	"log"
	"log/slog"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/handlers"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/wodbuster"
	"github.com/go-co-op/gocron"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type App struct {
	bot    *telegram.Bot
	config *Config
}

func Initialize(envFile string) (*App, error) {
	config, err := NewConfig(envFile)
	if err != nil {
		log.Fatal(err)
	}

	application, err := New(config)
	if err != nil {
		log.Fatal(err)
	}

	return application, nil
}

func New(config *Config) (*App, error) {
	wodClient, err := wodbuster.NewClient(config.Logger, config.WodbusterURL)
	if err != nil {
		config.Logger.Error("Failed to initialize wodbuster client",
			"error", err)
		return nil, err
	}

	bot, err := telegram.New(telegram.Config{
		Token:     config.TelegramToken,
		Debug:     config.LoggerLevel == slog.LevelDebug,
		Logger:    config.Logger,
		Wodbuster: wodClient,
	})
	if err != nil {
		return nil, err
	}

	return &App{
		bot:    bot,
		config: config,
	}, nil
}

func (a *App) Execute() error {
	// Initialize scheduler
	s := gocron.NewScheduler(time.UTC)
	if _, err := s.Every(1).Sunday().At("00:00").Do(func() {
		// TODO: Implement schedule sending through telegram bot
	}); err != nil {
		a.config.Logger.Error("Failed to schedule weekly task", "error", err)
		return err
	}
	s.StartAsync()

	return a.bot.Start()
}
