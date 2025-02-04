package telegram

import (
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var userSessions = make(map[int64]bool)

func (b *Bot) isAuthenticated(chatID int64) bool {
	authenticated, exists := userSessions[chatID]
	return exists && authenticated
}

func (b *Bot) handleLogin(update tgbotapi.Update) {
	args := strings.Split(update.Message.Text, " ")
	if len(args) != 3 {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID,
			"Please provide username and password: /login username password")
		if _, err := b.api.Send(msg); err != nil {
			b.logger.Error("Failed to send login format message",
				"error", err,
				"chat_id", update.Message.Chat.ID)
		}
		return
	}

	username := args[1]
	password := args[2]

	err := b.wodbuster.Login(username, password)
	if err != nil {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID,
			"Login failed. Please check your credentials and try again.")
		if _, err := b.api.Send(msg); err != nil {
			b.logger.Error("Failed to send login error message",
				"error", err,
				"chat_id", update.Message.Chat.ID)
		}
		return
	}

	userSessions[update.Message.Chat.ID] = true
	msg := tgbotapi.NewMessage(update.Message.Chat.ID,
		"Login successful! You can now use /book and /remove commands.")
	if _, err := b.api.Send(msg); err != nil {
		b.logger.Error("Failed to send login success message",
			"error", err,
			"chat_id", update.Message.Chat.ID)
	}
}

func (b *Bot) handleBooking(update tgbotapi.Update) {
	args := strings.Split(update.Message.Text, " ")
	if len(args) != 3 {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID,
			"Please provide day and hour: /book <day> <hour> (e.g., /book Monday 10:00)")
		if _, err := b.api.Send(msg); err != nil {
			b.logger.Error("Failed to send booking format message",
				"error", err,
				"chat_id", update.Message.Chat.ID)
		}
		return
	}

	day := strings.Title(strings.ToLower(args[1]))
	hour := args[2]

	err := b.wodbuster.BookClass(day, hour)
	if err != nil {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID,
			"Failed to book class. Please try again later.")
		if _, err := b.api.Send(msg); err != nil {
			b.logger.Error("Failed to send booking error message",
				"error", err,
				"chat_id", update.Message.Chat.ID)
		}
		return
	}

	msg := tgbotapi.NewMessage(update.Message.Chat.ID,
		"Class booked successfully!")
	if _, err := b.api.Send(msg); err != nil {
		b.logger.Error("Failed to send booking success message",
			"error", err,
			"chat_id", update.Message.Chat.ID)
	}
}

func (b *Bot) handleRemoveBooking(update tgbotapi.Update) {
	args := strings.Split(update.Message.Text, " ")
	if len(args) != 3 {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID,
			"Please provide day and hour: /remove <day> <hour> (e.g., /remove Monday 10:00)")
		if _, err := b.api.Send(msg); err != nil {
			b.logger.Error("Failed to send remove booking format message",
				"error", err,
				"chat_id", update.Message.Chat.ID)
		}
		return
	}

	day := strings.Title(strings.ToLower(args[1]))
	hour := args[2]

	err := b.wodbuster.RemoveBooking(day, hour)
	if err != nil {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID,
			"Failed to remove booking. Please try again later.")
		if _, err := b.api.Send(msg); err != nil {
			b.logger.Error("Failed to send remove booking error message",
				"error", err,
				"chat_id", update.Message.Chat.ID)
		}
		return
	}

	msg := tgbotapi.NewMessage(update.Message.Chat.ID,
		"Booking removed successfully!")
	if _, err := b.api.Send(msg); err != nil {
		b.logger.Error("Failed to send remove booking success message",
			"error", err,
			"chat_id", update.Message.Chat.ID)
	}
}
