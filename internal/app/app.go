package app

import (
	"context"
	"fmt"
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
)

type App struct {
	bot              *telegram.Bot
	manager          *usecase.Manager
	sessionManager   *usecase.SessionManager
	bookingScheduler *usecase.BookingScheduler
	storage          usecase.Storage
	logger           *slog.Logger
	config           *Config
	healthChecker    *health.Checker
}

func Initialize(envFile string) (*App, error) {
	config, err := NewConfig(envFile)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize config: %w", err)
	}

	application, err := New(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create application: %w", err)
	}

	return application, nil
}

func New(config *Config) (*App, error) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Initialize storage
	var store usecase.Storage
	var err error

	switch config.StorageType {
	case "mongodb":
		store, err = storage.NewMongoStorage(config.MongoURI, config.MongoDB)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize MongoDB storage: %w", err)
		}
	case "memory":
		store = storage.NewMemoryStorage()
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", config.StorageType)
	}

	// Initialize WODBuster client
	client, err := wodbuster.NewClient(config.WODBusterURL, wodbuster.WithLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("failed to create WODBuster client: %w", err)
	}

	// Create session manager with proper dependencies
	sessionManager := usecase.NewSessionManager(store, logger, config.WODBusterURL, config.EncryptionKey)

	// Create booking scheduler with session manager dependency
	bookingScheduler := usecase.NewBookingScheduler(sessionManager, store, logger)

	// Create manager with all dependencies injected
	manager := usecase.NewManager(
		store,
		client,
		config.EncryptionKey,
		sessionManager,
		bookingScheduler,
		logger,
	)

	// Initialize Telegram bot
	bot, err := telegram.New(config.TelegramToken, manager, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create Telegram bot: %w", err)
	}

	// Create health checker
	healthChecker := health.NewChecker(store, logger, config.Version)

	return &App{
		bot:              bot,
		manager:          manager,
		sessionManager:   sessionManager,
		bookingScheduler: bookingScheduler,
		storage:          store,
		logger:           logger,
		config:           config,
		healthChecker:    healthChecker,
	}, nil
}

func (a *App) Start(ctx context.Context) error {
	a.logger.Info("Starting WODBuster Bot",
		"version", a.config.Version,
		"storage_type", a.config.StorageType,
		"wodbuster_url", a.config.WODBusterURL)

	// Start health check server
	go func() {
		addr := ":" + a.config.HealthCheckPort
		if err := a.healthChecker.StartServer(addr); err != nil {
			a.logger.Error("Health check server failed", "error", err)
		}
	}()

	// Start booking scheduler at app level (Saturday cronjob)
	if err := a.bookingScheduler.Start(); err != nil {
		a.logger.Error("Failed to start booking scheduler", "error", err)
		return fmt.Errorf("failed to start booking scheduler: %w", err)
	}

	// Start Telegram bot
	return a.bot.Start()
}

func (a *App) Stop() {
	a.logger.Info("Stopping WODBuster Bot")

	// Stop booking scheduler and close all sessions
	a.bookingScheduler.Stop()
	a.sessionManager.CloseAllClients()

	// Stop bot
	a.bot.Stop()

	// Close storage if it has a Close method
	if mongoStorage, ok := a.storage.(*storage.MongoStorage); ok {
		if err := mongoStorage.Close(); err != nil {
			a.logger.Error("Failed to close MongoDB connection", "error", err)
		}
	}

	a.logger.Info("WODBuster Bot stopped")
}

func (a *App) Execute() error {
	// Start the Saturday booking scheduler
	a.logger.Info("Starting Saturday booking scheduler...")
	if err := a.bookingScheduler.Start(); err != nil {
		a.logger.Error("Failed to start booking scheduler", "error", err)
		return err
	}

	// Start health check server
	go func() {
		addr := ":" + a.config.HealthCheckPort
		if err := a.healthChecker.StartServer(addr); err != nil {
			a.logger.Error("Health check server failed", "error", err)
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

	a.logger.Info("🤖 WODBuster Bot is running...",
		"health_check_port", a.config.HealthCheckPort,
		"wodbuster_url", a.config.WODBusterURL)

	// Wait for either a signal or an error
	select {
	case sig := <-sigChan:
		a.logger.Info("Received shutdown signal", "signal", sig.String())
		return a.shutdown(ctx)
	case err := <-botErrChan:
		a.logger.Error("Bot error", "error", err)
		a.shutdown(ctx)
		return err
	}
}

func (a *App) shutdown(ctx context.Context) error {
	a.logger.Info("Starting graceful shutdown...")

	// Create timeout context for shutdown
	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)

		// Stop booking scheduler
		a.logger.Info("Stopping booking scheduler...")
		a.bookingScheduler.Stop()

		// Stop bot
		a.logger.Info("Stopping bot...")
		a.bot.Stop()

		// Note: Health check server will stop when the process exits
		a.logger.Info("Graceful shutdown completed")
	}()

	select {
	case <-done:
		return nil
	case <-shutdownCtx.Done():
		a.logger.Warn("Shutdown timeout reached, forcing exit")
		return shutdownCtx.Err()
	}
}
