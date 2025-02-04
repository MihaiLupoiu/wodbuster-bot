package app

import (
	"log/slog"
	"os"
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

	if token == "your_bot_token_here" {
		return nil, ErrInvalidToken
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
package app

import "errors"

var (
	ErrMissingToken = errors.New("TELEGRAM_BOT_TOKEN environment variable is not set")
	ErrInvalidToken = errors.New("Please set a valid Telegram bot token")
)
package app

import (
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/handlers"
	"github.com/go-co-op/gocron"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type App struct {
	bot    *tgbotapi.BotAPI
	config *Config
}

func New(config *Config) (*App, error) {
	bot, err := tgbotapi.NewBotAPI(config.TelegramToken)
	if err != nil {
		switch err.Error() {
		case "Not Found", "Unauthorized":
			config.Logger.Error("Invalid token. Please check your TELEGRAM_BOT_TOKEN is correct",
				"error", err)
		default:
			config.Logger.Error("Failed to initialize bot",
				"error", err)
		}
		return nil, err
	}

	bot.Debug = config.Debug
	config.Logger.Info("Bot authorized successfully",
		"username", bot.Self.UserName,
		"debug_mode", bot.Debug)

	return &App{
		bot:    bot,
		config: config,
	}, nil
}

func (a *App) Execute() error {
	// Initialize scheduler
	s := gocron.NewScheduler(time.UTC)
	if _, err := s.Every(1).Sunday().At("00:00").Do(func() {
		handlers.SendAvailableSchedule(a.bot)
	}); err != nil {
		a.config.Logger.Error("Failed to schedule weekly task", "error", err)
		return err
	}
	s.StartAsync()

	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 60

	updates := a.bot.GetUpdatesChan(updateConfig)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		go a.handleUpdate(update)
	}

	return nil
}

func (a *App) handleUpdate(update tgbotapi.Update) {
	msg := tgbotapi.NewMessage(update.Message.Chat.ID, "")

	switch update.Message.Command() {
	case "start":
		msg.Text = "Welcome! Please use /login to authenticate first."
	case "login":
		handlers.HandleLogin(a.bot, update)
		return
	case "book":
		if !handlers.IsAuthenticated(update.Message.Chat.ID) {
			msg.Text = "Please login first using /login"
		} else {
			handlers.HandleBooking(a.bot, update)
			return
		}
	case "remove":
		if !handlers.IsAuthenticated(update.Message.Chat.ID) {
			msg.Text = "Please login first using /login"
		} else {
			handlers.HandleRemoveBooking(a.bot, update)
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

	if _, err := a.bot.Send(msg); err != nil {
		a.config.Logger.Error("Failed to send message",
			"chat_id", update.Message.Chat.ID,
			"error", err)
	}
}
