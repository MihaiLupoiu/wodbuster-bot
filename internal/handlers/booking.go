package handlers

import (
	"fmt"
	"log"
	"strings"
	
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"telegram-class-bot/internal/models"
)

func handleBooking(bot Bot, update tgbotapi.Update) {
	args := strings.Split(update.Message.Text, " ")
	if len(args) != 2 {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, 
			"Please provide class ID: /book <class_id>")
		bot.Send(msg)
		return
	}

	classID := args[1]
	session := userSessions[update.Message.Chat.ID]

	// Call your booking API
	err := bookClass(classID, session.Token)
	if err != nil {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, 
			"Failed to book class. Please try again.")
		bot.Send(msg)
		return
	}

	msg := tgbotapi.NewMessage(update.Message.Chat.ID, 
		fmt.Sprintf("Successfully booked class %s!", classID))
	bot.Send(msg)
}

func sendAvailableSchedule(bot Bot) {
	// Implement API call to get available schedule
	schedule, err := getAvailableClasses()
	if err != nil {
		log.Printf("Error getting schedule: %v", err)
		return
	}

	// Send schedule to all authenticated users
	for chatID, session := range userSessions {
		if session.IsAuthenticated {
			msg := formatScheduleMessage(schedule)
			bot.Send(tgbotapi.NewMessage(chatID, msg))
		}
	}
}

func formatScheduleMessage(schedule []models.ClassSchedule) string {
	// Format the schedule into a readable message
	var sb strings.Builder
	sb.WriteString("Available Classes:\n\n")
	
	for _, class := range schedule {
		sb.WriteString(fmt.Sprintf("ID: %s\nTime: %s\n\n", 
			class.ID, class.DateTime))
	}
	
	sb.WriteString("\nTo book a class, use /book <class_id>")
	return sb.String()
}
func bookClass(classID string, token string) error {
	// TODO: Implement actual API call
	// This is a mock implementation
	return nil
}

func getAvailableClasses() ([]models.ClassSchedule, error) {
	// TODO: Implement actual API call
	// This is a mock implementation
	return []models.ClassSchedule{
		{
			ID:        "class123",
			DateTime:  "2025-02-04 10:00",
			Available: true,
		},
	}, nil
}
