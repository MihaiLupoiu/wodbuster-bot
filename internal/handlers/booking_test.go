package handlers

import (
	"testing"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestHandleBooking(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		chatID        int64
		expectedMsg   string
		authenticated bool
	}{
		{
			name:          "invalid format",
			input:         "/book Monday",
			chatID:        123,
			expectedMsg:   "Please provide day and hour: /book <day> <hour> (e.g., /book Monday 10:00)",
			authenticated: true,
		},
		{
			name:          "invalid day",
			input:         "/book InvalidDay 10:00",
			chatID:        123,
			expectedMsg:   "Invalid day. Please use: Monday, Tuesday, Wednesday, Thursday, Friday, Saturday, or Sunday",
			authenticated: true,
		},
		{
			name:          "valid booking",
			input:         "/book Monday 10:00",
			chatID:        123,
			expectedMsg:   "Successfully booked class for Monday at 10:00!",
			authenticated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockBot := NewMockBot(t)
			mockBot.EXPECT().Send(mock.MatchedBy(func(msg tgbotapi.MessageConfig) bool {
				assert.Equal(t, tt.expectedMsg, msg.Text)
				assert.Equal(t, tt.chatID, msg.ChatID)
				return true
			})).Return(tgbotapi.Message{}, nil)

			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{
						ID: tt.chatID,
					},
					Text: tt.input,
				},
			}

			if tt.authenticated {
				userSessions[tt.chatID] = models.UserSession{
					IsAuthenticated: true,
					Username:        "testuser",
					Token:           "testtoken",
				}
			}

			HandleBooking(mockBot, update)
		})
	}
}

func TestHandleRemoveBooking(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		chatID        int64
		expectedMsg   string
		authenticated bool
	}{
		{
			name:          "invalid format",
			input:         "/remove Monday",
			chatID:        123,
			expectedMsg:   "Please provide day and hour: /remove <day> <hour> (e.g., /remove Monday 10:00)",
			authenticated: true,
		},
		{
			name:          "valid removal",
			input:         "/remove Monday 10:00",
			chatID:        123,
			expectedMsg:   "Successfully removed booking for Monday at 10:00!",
			authenticated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockBot := NewMockBot(t)
			mockBot.EXPECT().Send(mock.MatchedBy(func(msg tgbotapi.MessageConfig) bool {
				assert.Equal(t, tt.expectedMsg, msg.Text)
				assert.Equal(t, tt.chatID, msg.ChatID)
				return true
			})).Return(tgbotapi.Message{}, nil)

			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{
						ID: tt.chatID,
					},
					Text: tt.input,
				},
			}

			if tt.authenticated {
				userSessions[tt.chatID] = models.UserSession{
					IsAuthenticated: true,
					Username:        "testuser",
					Token:           "testtoken",
				}
			}

			HandleRemoveBooking(mockBot, update)
		})
	}
}

func TestFormatScheduleMessage(t *testing.T) {
	schedule := []models.ClassSchedule{
		{
			Day:       "Monday",
			Hour:      "10:00",
			Available: true,
		},
		{
			Day:       "Monday",
			Hour:      "11:00",
			Available: false,
			BookedBy:  "user1",
		},
	}

	expected := "Class Schedule:\n\n=== Monday ===\nTime: 10:00 - Available\nTime: 11:00 - Booked by user1\n\n\nTo book a class, use /book <day> <hour>\nTo remove your booking, use /remove <day> <hour>"

	result := formatScheduleMessage(schedule)
	if result != expected {
		t.Errorf("formatScheduleMessage() returned unexpected format\nexpected: %q\ngot: %q", expected, result)
	}
}
