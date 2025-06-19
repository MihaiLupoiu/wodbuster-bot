package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type BookingManager interface {
	IsAuthenticated(ctx context.Context, chatID int64) bool
	ScheduleBookClass(ctx context.Context, chatID int64, class models.ClassBookingSchedule) error
}

type BookingBotAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
}

type BookingHandler struct {
	api     BookingBotAPI
	manager BookingManager
}

func NewBookingHandler(api BookingBotAPI, manager BookingManager) *BookingHandler {
	return &BookingHandler{
		api:     api,
		manager: manager,
	}
}

func (h *BookingHandler) Handle(update tgbotapi.Update) {
	ctx := context.Background()

	if !h.manager.IsAuthenticated(ctx, update.Message.Chat.ID) {
		h.sendMessage(update.Message.Chat.ID, "Please login first using /login command")
		return
	}

	args := strings.Split(update.Message.Text, " ")
	if len(args) != 3 {
		h.sendMessage(update.Message.Chat.ID,
			"Please provide day and hour: /book <day> <hour> <class-type> (e.g., /book Monday 10:00 wod)")
		return
	}

	caser := cases.Title(language.English)
	day := caser.String(strings.ToLower(args[1]))
	hour := args[2]
	classType := caser.String(args[3])

	if err := h.manager.ScheduleBookClass(ctx, update.Message.Chat.ID, models.ClassBookingSchedule{
		ID:        fmt.Sprintf("%s-%s-%s", day, hour, classType),
		Day:       day,
		Hour:      hour,
		ClassType: classType,
	}); err != nil {
		h.sendMessage(update.Message.Chat.ID,
			"Failed to book class. Please try again later.")

		slog.Error("Failed to book class",
			"error", err,
			"chat_id", update.Message.Chat.ID,
			"day", day,
			"hour", hour,
			"class_type", classType)
		return
	}

	// if err := h.wodbuster.BookClass(session.Username, session.Password, day, hour); err != nil {
	// 	h.sendMessage(update.Message.Chat.ID,
	// 		"Failed to book class. Please try again later.")
	// 	return
	// }

	h.sendMessage(update.Message.Chat.ID, "Class booked successfully!")
}

func (h *BookingHandler) sendMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := h.api.Send(msg); err != nil {
		slog.Error("Failed to send message",
			"error", err,
			"chat_id", chatID)
	}
}
