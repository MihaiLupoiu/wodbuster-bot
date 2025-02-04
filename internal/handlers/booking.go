package handlers

import (
	"fmt"
	"log/slog"
	"strings"
	
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"telegram-class-bot/internal/models"
)

func HandleBooking(bot Bot, update tgbotapi.Update) {
	args := strings.Split(update.Message.Text, " ")
	if len(args) != 3 {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, 
			"Please provide day and hour: /book <day> <hour> (e.g., /book Monday 10:00)")
		bot.Send(msg)
		return
	}

	day := strings.Title(strings.ToLower(args[1]))
	hour := args[2]
	session := userSessions[update.Message.Chat.ID]

	// Validate day
	validDays := []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
	if !contains(validDays, day) {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, 
			"Invalid day. Please use: Monday, Tuesday, Wednesday, Thursday, Friday, Saturday, or Sunday")
		bot.Send(msg)
		return
	}

	// Call your booking API
	if err := bookClass(day, hour, session.Username, session.Token); err != nil {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, 
			"Failed to book class. Please try again.")
		bot.Send(msg)
		return
	}

	msg := tgbotapi.NewMessage(update.Message.Chat.ID, 
		fmt.Sprintf("Successfully booked class for %s at %s!", day, hour))
	bot.Send(msg)
}

func HandleRemoveBooking(bot Bot, update tgbotapi.Update) {
	args := strings.Split(update.Message.Text, " ")
	if len(args) != 3 {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, 
			"Please provide day and hour: /remove <day> <hour> (e.g., /remove Monday 10:00)")
		bot.Send(msg)
		return
	}

	day := strings.Title(strings.ToLower(args[1]))
	hour := args[2]
	session := userSessions[update.Message.Chat.ID]

	// Call your remove booking API
	if err := removeBooking(day, hour, session.Username, session.Token); err != nil {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, 
			"Failed to remove booking. Please try again.")
		bot.Send(msg)
		return
	}

	msg := tgbotapi.NewMessage(update.Message.Chat.ID, 
		fmt.Sprintf("Successfully removed booking for %s at %s!", day, hour))
	bot.Send(msg)
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func SendAvailableSchedule(bot Bot) {
	// Implement API call to get available schedule
	schedule, err := getAvailableClasses()
	if err != nil {
		slog.Error("Failed to get class schedule", 
			"error", err,
			"function", "SendAvailableSchedule")
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
	var sb strings.Builder
	sb.WriteString("Class Schedule:\n\n")
	
	// Group classes by day
	scheduleByDay := make(map[string][]models.ClassSchedule)
	for _, class := range schedule {
		scheduleByDay[class.Day] = append(scheduleByDay[class.Day], class)
	}
	
	// Sort days
	days := []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
	for _, day := range days {
		if classes, exists := scheduleByDay[day]; exists {
			sb.WriteString(fmt.Sprintf("=== %s ===\n", day))
			for _, class := range classes {
				status := "Available"
				if !class.Available {
					status = fmt.Sprintf("Booked by %s", class.BookedBy)
				}
				sb.WriteString(fmt.Sprintf("Time: %s - %s\n", class.Hour, status))
			}
			sb.WriteString("\n")
		}
	}
	
	sb.WriteString("\nTo book a class, use /book <day> <hour>")
	sb.WriteString("\nTo remove your booking, use /remove <day> <hour>")
	return sb.String()
}
func bookClass(day string, hour string, username string, token string) error {
	// TODO: Implement actual API call
	// This is a mock implementation
	return nil
}

func removeBooking(day string, hour string, username string, token string) error {
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
			Day:       "Monday",
			Hour:      "10:00",
			Available: true,
			BookedBy:  "",
		},
	}, nil
}
