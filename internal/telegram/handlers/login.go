package handlers

import (
	"strings"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/session"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type LoginHandler struct {
	api       BotAPI
	wodbuster WodbusterClient
	logger    Logger
	sessions  *session.Manager
}

func NewLoginHandler(api BotAPI, wodbuster WodbusterClient, logger Logger, sessions *session.Manager) *LoginHandler {
	return &LoginHandler{
		api:       api,
		wodbuster: wodbuster,
		logger:    logger,
		sessions:  sessions,
	}
}

func (h *LoginHandler) Handle(update tgbotapi.Update) {
	args := strings.Split(update.Message.Text, " ")
	if len(args) != 3 {
		h.sendMessage(update.Message.Chat.ID,
			"Please provide username and password: /login username password")
		return
	}

	username := args[1]
	password := args[2]

	if err := h.wodbuster.Login(username, password); err != nil {
		h.sendMessage(update.Message.Chat.ID,
			"Login failed. Please check your credentials and try again.")
		return
	}

	h.sessions.SetAuthenticated(update.Message.Chat.ID, true, username, password)
	h.sendMessage(update.Message.Chat.ID,
		"Login successful! You can now use /book and /remove commands.")
}

func (h *LoginHandler) sendMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := h.api.Send(msg); err != nil {
		h.logger.Error("Failed to send message",
			"error", err,
			"chat_id", chatID)
	}
}
