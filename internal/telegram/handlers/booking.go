package handlers

import (
    "strings"
    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
    "github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/session"
)

type BookingHandler struct {
    api       BotAPI
    wodbuster WodbusterClient
    logger    Logger
    sessions  *session.Manager
}

func NewBookingHandler(api BotAPI, wodbuster WodbusterClient, logger Logger, sessions *session.Manager) *BookingHandler {
    return &BookingHandler{
        api:       api,
        wodbuster: wodbuster,
        logger:    logger,
        sessions:  sessions,
    }
}

func (h *BookingHandler) Handle(update tgbotapi.Update) {
    if !h.sessions.IsAuthenticated(update.Message.Chat.ID) {
        h.sendMessage(update.Message.Chat.ID, "Please login first using /login command")
        return
    }

    args := strings.Split(update.Message.Text, " ")
    if len(args) != 3 {
        h.sendMessage(update.Message.Chat.ID,
            "Please provide day and hour: /book <day> <hour> (e.g., /book Monday 10:00)")
        return
    }

    day := strings.Title(strings.ToLower(args[1]))
    hour := args[2]

    if err := h.wodbuster.BookClass(day, hour); err != nil {
        h.sendMessage(update.Message.Chat.ID,
            "Failed to book class. Please try again later.")
        return
    }

    h.sendMessage(update.Message.Chat.ID, "Class booked successfully!")
}

func (h *BookingHandler) sendMessage(chatID int64, text string) {
    msg := tgbotapi.NewMessage(chatID, text)
    if _, err := h.api.Send(msg); err != nil {
        h.logger.Error("Failed to send message",
            "error", err,
            "chat_id", chatID)
    }
}
