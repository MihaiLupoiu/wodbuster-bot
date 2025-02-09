package handlers

import (
    "strings"
    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
    "github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/session"
)

type RemoveHandler struct {
    api       BotAPI
    wodbuster WodbusterClient
    logger    Logger
    sessions  *session.Manager
}

func NewRemoveHandler(api BotAPI, wodbuster WodbusterClient, logger Logger, sessions *session.Manager) *RemoveHandler {
    return &RemoveHandler{
        api:       api,
        wodbuster: wodbuster,
        logger:    logger,
        sessions:  sessions,
    }
}

func (h *RemoveHandler) Handle(update tgbotapi.Update) {
    if !h.sessions.IsAuthenticated(update.Message.Chat.ID) {
        h.sendMessage(update.Message.Chat.ID, "Please login first using /login command")
        return
    }

    args := strings.Split(update.Message.Text, " ")
    if len(args) != 3 {
        h.sendMessage(update.Message.Chat.ID,
            "Please provide day and hour: /remove <day> <hour> (e.g., /remove Monday 10:00)")
        return
    }

    day := strings.Title(strings.ToLower(args[1]))
    hour := args[2]

    if err := h.wodbuster.RemoveBooking(day, hour); err != nil {
        h.sendMessage(update.Message.Chat.ID,
            "Failed to remove booking. Please try again later.")
        return
    }

    h.sendMessage(update.Message.Chat.ID, "Booking removed successfully!")
}

func (h *RemoveHandler) sendMessage(chatID int64, text string) {
    msg := tgbotapi.NewMessage(chatID, text)
    if _, err := h.api.Send(msg); err != nil {
        h.logger.Error("Failed to send message",
            "error", err,
            "chat_id", chatID)
    }
}
