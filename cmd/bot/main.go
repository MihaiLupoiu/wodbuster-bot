package main

import (
	"log"
	"os"
	"time"

	"github.com/go-co-op/gocron"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"telegram-class-bot/internal/handlers"
	"telegram-class-bot/internal/models"
)

var (
	// Store user sessions (you might want to use Redis in production)
	userSessions = make(map[int64]models.UserSession)
)

func main() {
	bot, err := tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = true
	log.Printf("Authorized on account %s", bot.Self.UserName)

	// Initialize scheduler
	scheduler := gocron.NewScheduler(time.UTC)
	scheduler.Every(1).Sunday().At("00:00").Do(func() {
		sendAvailableSchedule(bot)
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
		handleLogin(bot, update)
		return
	case "book":
		if !isAuthenticated(update.Message.Chat.ID) {
			msg.Text = "Please login first using /login"
		} else {
			handleBooking(bot, update)
			return
		}
	default:
		msg.Text = "I don't know that command"
	}

	if _, err := bot.Send(msg); err != nil {
		log.Println(err)
	}
}
