package telegram

import (
	"log/slog"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/handlers"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/usecase"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/utils"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
	api          *tgbotapi.BotAPI
	logger       *slog.Logger
	manager      *usecase.Manager
	loginHandler *handlers.LoginHandler
	bookHandler  *handlers.BookingHandler
	rateLimiter  *utils.RateLimiter
	stopChan     chan struct{}
	// removeHandler *handlers.RemoveHandler
}

type Config struct {
	Token         string
	Debug         bool
	Logger        *slog.Logger
	APIClient     usecase.APIClient
	Storage       usecase.Storage
	EncryptionKey string
}

func New(cfg Config) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.Token)
	if err != nil {
		switch err.Error() {
		case "Not Found", "Unauthorized":
			cfg.Logger.Error("Invalid token. Please check your TELEGRAM_BOT_TOKEN is correct",
				"error", err)
		default:
			cfg.Logger.Error("Failed to initialize bot",
				"error", err)
		}
		return nil, err
	}

	api.Debug = cfg.Debug
	cfg.Logger.Info("Bot authorized successfully",
		"user", api.Self.UserName,
		"debug_mode", api.Debug)

	manager := usecase.NewManager(cfg.Storage, cfg.APIClient, cfg.EncryptionKey)

	// Create rate limiter: 1 command per 2 seconds, max 5 tokens
	rateLimiter := utils.NewRateLimiter(2*time.Second, 5)

	return &Bot{
		api:          api,
		logger:       cfg.Logger,
		manager:      manager,
		loginHandler: handlers.NewLoginHandler(api, manager),
		bookHandler:  handlers.NewBookingHandler(api, manager),
		rateLimiter:  rateLimiter,
		// removeHandler: handlers.NewRemoveHandler(api, cfg.Wodbuster, cfg.Logger, manager),
	}, nil
}

func (b *Bot) Start() error {
	b.stopChan = make(chan struct{})

	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 60

	updates := b.api.GetUpdatesChan(updateConfig)

	// Start cleanup goroutine for rate limiter
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				b.rateLimiter.Cleanup(24 * time.Hour)
			case <-b.stopChan:
				return
			}
		}
	}()

	for {
		select {
		case update := <-updates:
			if update.Message == nil {
				continue
			}
			go b.handleUpdate(update)
		case <-b.stopChan:
			b.logger.Info("Bot stopping...")
			return nil
		}
	}
}

func (b *Bot) Stop() error {
	if b.stopChan != nil {
		close(b.stopChan)
	}
	return nil
}

func (b *Bot) handleUpdate(update tgbotapi.Update) {
	if update.Message == nil {
		return
	}

	// Check rate limit
	if !b.rateLimiter.Allow(update.Message.Chat.ID) {
		b.sendMessage(update.Message.Chat.ID,
			"You're sending commands too quickly. Please wait a moment before trying again.")
		return
	}

	switch update.Message.Command() {
	case "start":
		b.sendMessage(update.Message.Chat.ID,
			"Welcome! Please use /login to authenticate first.")
	case "login":
		b.loginHandler.Handle(update)
	case "book":
		b.bookHandler.Handle(update)
	// case "remove":
	// 	b.removeHandler.Handle(update)
	case "help":
		b.sendMessage(update.Message.Chat.ID,
			"Available commands:\n"+
				"/login email password - Login to the system\n"+
				"/book day hour class-type - Book a class (e.g., /book Monday 10:00 wod)\n"+
				"/remove day hour class-type - Remove your booking (e.g., /remove Monday 10:00 wod)\n"+
				"/help - Show this help message")
	default:
		b.sendMessage(update.Message.Chat.ID,
			"I don't know that command. Use /help to see available commands")
	}
}

func (b *Bot) sendMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := b.api.Send(msg); err != nil {
		b.logger.Error("Failed to send message",
			"error", err,
			"chat_id", chatID)
	}
}
