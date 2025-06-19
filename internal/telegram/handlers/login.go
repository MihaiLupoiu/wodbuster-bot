package handlers

import (
	"context"
	"log/slog"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type LogInManager interface {
	IsAuthenticated(ctx context.Context, chatID int64) bool
	LogInAndSave(ctx context.Context, chatID int64, email, password string)
}

type LogInBotAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
}

type LoginHandler struct {
	api     LogInBotAPI
	manager LogInManager
}

func NewLoginHandler(api LogInBotAPI, logInManager LogInManager) *LoginHandler {
	return &LoginHandler{
		api:     api,
		manager: logInManager,
	}
}

func (h *LoginHandler) Handle(update tgbotapi.Update) {
	ctx := context.Background()

	args := strings.Split(update.Message.Text, " ")
	if len(args) != 3 {
		h.sendMessage(update.Message.Chat.ID,
			"Please provide email and password: /login email password")
		return
	}

	email := args[1]
	password := args[2]

	// if err := h.wodbuster.Login(email, password); err != nil {
	// 	h.sendMessage(update.Message.Chat.ID,
	// 		"Login failed. Please check your credentials and try again.")
	// 	return
	// }

	h.manager.LogInAndSave(ctx, update.Message.Chat.ID, email, password)
	h.sendMessage(update.Message.Chat.ID,
		"Login successful! You can now use /book and /remove commands.")
}

func (h *LoginHandler) sendMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := h.api.Send(msg); err != nil {
		slog.Error("Failed to send message",
			"error", err,
			"chat_id", chatID)
	}
}
