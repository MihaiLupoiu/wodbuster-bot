package app

import (
	"log"
	"log/slog"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/usecase"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/wodbuster"
	"github.com/go-co-op/gocron"
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
	var store usecase.Storage
	var err error

	// Initialize storage based on configuration
	switch config.StorageType {
	case "mongodb":
		store, err = storage.NewMongoStorage(
			config.MongoURI,
			config.MongoDB,
		)
		if err != nil {
			config.Logger.Error("Failed to initialize MongoDB storage",
				"error", err)
			return nil, err
		}
	default:
		store = storage.NewMemoryStorage()
	}

	wodClient, err := wodbuster.NewClient(config.WodbusterURL,
		wodbuster.WithLogger(config.Logger))
	if err != nil {
		config.Logger.Error("Failed to initialize wodbuster client",
			"error", err)
		return nil, err
	}

	bot, err := telegram.New(telegram.Config{
		Token:     config.TelegramToken,
		Debug:     config.LoggerLevel == slog.LevelDebug,
		Logger:    config.Logger,
		APIClient: wodClient,
		Storage:   store,
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
