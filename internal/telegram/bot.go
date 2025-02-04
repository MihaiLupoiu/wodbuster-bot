package telegram

import (
	"log/slog"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/wodbuster"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
	api       *tgbotapi.BotAPI
	wodbuster *wodbuster.Client
	logger    *slog.Logger
}

type Config struct {
	Token     string
	Debug     bool
	Logger    *slog.Logger
	Wodbuster *wodbuster.Client
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
		"username", api.Self.UserName,
		"debug_mode", api.Debug)

	return &Bot{
		api:       api,
		wodbuster: cfg.Wodbuster,
		logger:    cfg.Logger,
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
	msg := tgbotapi.NewMessage(update.Message.Chat.ID, "")

	switch update.Message.Command() {
	case "start":
		msg.Text = "Welcome! Please use /login to authenticate first."
	case "login":
		b.handleLogin(update)
		return
	case "book":
		if !b.isAuthenticated(update.Message.Chat.ID) {
			msg.Text = "Please login first using /login"
		} else {
			b.handleBooking(update)
			return
		}
	case "remove":
		if !b.isAuthenticated(update.Message.Chat.ID) {
			msg.Text = "Please login first using /login"
		} else {
			b.handleRemoveBooking(update)
			return
		}
	case "help":
		msg.Text = "Available commands:\n" +
			"/login username password - Login to the system\n" +
			"/book day hour - Book a class (e.g., /book Monday 10:00)\n" +
			"/remove day hour - Remove your booking (e.g., /remove Monday 10:00)\n" +
			"/help - Show this help message"
	default:
		msg.Text = "I don't know that command. Use /help to see available commands"
	}

	if _, err := b.api.Send(msg); err != nil {
		b.logger.Error("Failed to send message",
			"chat_id", update.Message.Chat.ID,
			"error", err)
	}
}
