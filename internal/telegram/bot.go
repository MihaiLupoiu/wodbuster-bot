package telegram

import (
	"log/slog"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/handlers"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/usecase"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
	api          *tgbotapi.BotAPI
	logger       *slog.Logger
	manager      *usecase.Manager
	loginHandler *handlers.LoginHandler
	bookHandler  *handlers.BookingHandler
	// removeHandler *handlers.RemoveHandler
}

type Config struct {
	Token     string
	Debug     bool
	Logger    *slog.Logger
	APIClient usecase.APIClient
	Storage   usecase.Storage
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

	manager := usecase.NewManager(cfg.Storage, cfg.APIClient)

	return &Bot{
		api:          api,
		logger:       cfg.Logger,
		manager:      manager,
		loginHandler: handlers.NewLoginHandler(api, manager),
		bookHandler:  handlers.NewBookingHandler(api, manager),
		// removeHandler: handlers.NewRemoveHandler(api, cfg.Wodbuster, cfg.Logger, manager),
	}, nil
}

func (b *Bot) Start() error {
	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 60

	updates := b.api.GetUpdatesChan(updateConfig)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		go b.handleUpdate(update)
	}

	return nil
}

func (b *Bot) handleUpdate(update tgbotapi.Update) {
	if update.Message == nil {
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
