package handlers

import (
	"log/slog"
	"strings"
	
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"telegram-class-bot/internal/models"
)

var userSessions = make(map[int64]models.UserSession)

func HandleLogin(bot Bot, update tgbotapi.Update) {
	args := strings.Split(update.Message.Text, " ")
	if len(args) != 3 {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, 
			"Please provide username and password: /login username password")
		bot.Send(msg)
		return
	}

	username := args[1]
	password := args[2]

	// Call your web API to authenticate
	token, err := authenticateUser(username, password)
	if err != nil {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, 
			"Authentication failed. Please try again.")
		bot.Send(msg)
		return
	}

	userSessions[update.Message.Chat.ID] = models.UserSession{
		IsAuthenticated: true,
		Username:       username,
		Token:          token,
	}

	msg := tgbotapi.NewMessage(update.Message.Chat.ID, 
		"Successfully logged in! You can now use /book to reserve classes.")
	bot.Send(msg)
}

func authenticateUser(username, password string) (string, error) {
	// Implementation to call your web API authentication endpoint
	// Return the authentication token
	return "", nil
}

func IsAuthenticated(chatID int64) bool {
	session, exists := userSessions[chatID]
	return exists && session.IsAuthenticated
}
