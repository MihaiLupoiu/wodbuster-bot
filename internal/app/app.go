package app

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/health"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/usecase"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/wodbuster"
	"github.com/go-co-op/gocron"
)

type App struct {
	bot           *telegram.Bot
	config        *Config
	scheduler     *gocron.Scheduler
	healthChecker *health.Checker
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
		Token:         config.TelegramToken,
		Debug:         config.LoggerLevel == slog.LevelDebug,
		Logger:        config.Logger,
		APIClient:     wodClient,
		Storage:       store,
		EncryptionKey: config.EncryptionKey,
	})
	if err != nil {
		return nil, err
	}

	// Initialize scheduler
	scheduler := gocron.NewScheduler(time.UTC)

	// Initialize health checker
	healthChecker := health.NewChecker(store, config.Logger, config.Version)

	return &App{
		bot:           bot,
		config:        config,
		scheduler:     scheduler,
		healthChecker: healthChecker,
	}, nil
}

func (a *App) Execute() error {
	// Setup weekly schedule task
	if _, err := a.scheduler.Every(1).Sunday().At("00:00").Do(func() {
		// TODO: Implement schedule sending through telegram bot
		a.config.Logger.Info("Weekly schedule task executed")
	}); err != nil {
		a.config.Logger.Error("Failed to schedule weekly task", "error", err)
		return err
	}
	a.scheduler.StartAsync()

	// Start health check server
	go func() {
		addr := ":" + a.config.HealthCheckPort
		if err := a.healthChecker.StartServer(addr); err != nil {
			a.config.Logger.Error("Health check server failed", "error", err)
		}
	}()

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start bot in a goroutine
	botErrChan := make(chan error, 1)
	go func() {
		if err := a.bot.Start(); err != nil {
			botErrChan <- err
		}
	}()

	// Wait for either a signal or an error
	select {
	case sig := <-sigChan:
		a.config.Logger.Info("Received shutdown signal", "signal", sig.String())
		return a.shutdown(ctx)
	case err := <-botErrChan:
		a.config.Logger.Error("Bot error", "error", err)
		a.shutdown(ctx)
		return err
	}
}

func (a *App) shutdown(ctx context.Context) error {
	a.config.Logger.Info("Starting graceful shutdown...")

	// Create timeout context for shutdown
	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)

		// Stop scheduler first
		a.config.Logger.Info("Stopping scheduler...")
		a.scheduler.Stop()

		// Stop bot
		a.config.Logger.Info("Stopping bot...")
		if err := a.bot.Stop(); err != nil {
			a.config.Logger.Error("Error stopping bot", "error", err)
		}

		// Note: Health check server will stop when the process exits
		a.config.Logger.Info("Graceful shutdown completed")
	}()

	select {
	case <-done:
		return nil
	case <-shutdownCtx.Done():
		a.config.Logger.Warn("Shutdown timeout reached, forcing exit")
		return shutdownCtx.Err()
	}
}
