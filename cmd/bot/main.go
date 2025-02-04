package main

import (
	"log/slog"
	"os"
	"time"

	"github.com/go-co-op/gocron"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"telegram-class-bot/internal/handlers"
)

func main() {
	// Initialize structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		slog.Error("TELEGRAM_BOT_TOKEN environment variable is not set")
		os.Exit(1)
	}

	if token == "your_bot_token_here" {
		slog.Error("Please set a valid Telegram bot token")
		os.Exit(1)
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		slog.Error("Failed to initialize bot", "error", err)
		os.Exit(1)
	}

	bot.Debug = true
	slog.Info("Bot authorized successfully", 
		"username", bot.Self.UserName,
		"debug_mode", bot.Debug)

	// Initialize scheduler
	scheduler := gocron.NewScheduler(time.UTC)
	scheduler.Every(1).Sunday().At("00:00").Do(func() {
		handlers.SendAvailableSchedule(bot)
	})
	scheduler.StartAsync()

	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 60

	updates := bot.GetUpdatesChan(updateConfig)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		go handleUpdate(bot, update)
	}
}

func handleUpdate(bot *tgbotapi.BotAPI, update tgbotapi.Update) {
	msg := tgbotapi.NewMessage(update.Message.Chat.ID, "")

	switch update.Message.Command() {
	case "start":
		msg.Text = "Welcome! Please use /login to authenticate first."
	case "login":
		handlers.HandleLogin(bot, update)
		return
	case "book":
		if !handlers.IsAuthenticated(update.Message.Chat.ID) {
			msg.Text = "Please login first using /login"
		} else {
			handlers.HandleBooking(bot, update)
			return
		}
	case "remove":
		if !handlers.IsAuthenticated(update.Message.Chat.ID) {
			msg.Text = "Please login first using /login"
		} else {
			handlers.HandleRemoveBooking(bot, update)
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

	if _, err := bot.Send(msg); err != nil {
		slog.Error("Failed to send message", 
			"chat_id", update.Message.Chat.ID,
			"error", err)
	}
}
