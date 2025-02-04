package handlers

import (
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"telegram-class-bot/internal/models"
)

func TestHandleBooking(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		wantErr  bool
		expected string
	}{
		{
			name:     "valid booking",
			message:  "/book class123",
			wantErr:  false,
			expected: "Successfully booked class",
		},
		{
			name:     "invalid format",
			message:  "/book",
			wantErr:  true,
			expected: "Please provide class ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock update
			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Text: tt.message,
					Chat: &tgbotapi.Chat{
						ID: 123456,
					},
				},
			}

			// Create a mock bot
			bot := &MockBot{}
			
			handleBooking(bot, update)

			// Verify the results
			if tt.wantErr && !strings.Contains(bot.lastMessage, tt.expected) {
				t.Errorf("expected error message containing %q, got %q", tt.expected, bot.lastMessage)
			}
		})
	}
}

func TestFormatScheduleMessage(t *testing.T) {
	schedule := []models.ClassSchedule{
		{
			ID:       "class123",
			DateTime: "2025-02-04 10:00",
			Available: true,
		},
	}

	result := formatScheduleMessage(schedule)
	expected := "Available Classes:"

	if !strings.Contains(result, expected) {
		t.Errorf("expected message to contain %q", expected)
	}
	if !strings.Contains(result, "class123") {
		t.Errorf("expected message to contain class ID")
	}
}
