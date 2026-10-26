package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/utils"
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

// Handle adds a class to the athlete's schedule and reports whether it did.
func (h *BookingHandler) Handle(update tgbotapi.Update) bool {
	ctx := context.Background()

	if !h.manager.IsAuthenticated(ctx, update.Message.Chat.ID) {
		h.sendMessage(update.Message.Chat.ID, "Please login first using /login command")
		return false
	}

	// Fields, not Split: a double space used to make a valid command look
	// malformed. The class name is everything after the hour, because the
	// box's names can be more than one word ("Open Box").
	args := strings.Fields(update.Message.Text)
	if len(args) < 4 {
		h.sendMessage(update.Message.Chat.ID,
			"Please provide day, hour and class: /book <day> <hour> <class> (e.g., /book Monday 10:00 wod)")
		return false
	}

	// Sanitize inputs
	rawDay := utils.SanitizeInput(args[1])
	rawHour := utils.SanitizeInput(args[2])
	rawClassType := utils.SanitizeInput(strings.Join(args[3:], " "))

	// Validate inputs
	if err := utils.ValidateDay(rawDay); err != nil {
		h.sendMessage(update.Message.Chat.ID,
			"Invalid day. Please use: Monday, Tuesday, Wednesday, Thursday, Friday, Saturday, Sunday")
		return false
	}

	if err := utils.ValidateTime(rawHour); err != nil {
		h.sendMessage(update.Message.Chat.ID,
			"Invalid time format. Please use HH:MM format (e.g., 10:00)")
		return false
	}

	if err := utils.ValidateClassType(rawClassType); err != nil {
		h.sendMessage(update.Message.Chat.ID,
			"Invalid class name. Use the name as the box shows it in WodBuster (e.g., Wod, Open Box, Hyrox)")
		return false
	}

	// Format inputs
	caser := cases.Title(language.English)
	day := caser.String(strings.ToLower(rawDay))
	hour := rawHour
	classType := caser.String(strings.ToLower(rawClassType))

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
		return false
	}

	slog.Info("class scheduled", "chat_id", update.Message.Chat.ID,
		"day", day, "hour", hour, "class_type", classType)
	h.sendMessage(update.Message.Chat.ID,
		fmt.Sprintf("Class scheduled successfully! %s at %s for %s", classType, hour, day))
	return true
}

func (h *BookingHandler) sendMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := h.api.Send(msg); err != nil {
		slog.Error("Failed to send message",
			"error", err,
			"chat_id", chatID)
	}
}
