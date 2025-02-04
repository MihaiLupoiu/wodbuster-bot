package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestHandleLogin(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		wantErr  bool
		expected string
	}{
		{
			name:     "valid login",
			message:  "/login testuser password123",
			wantErr:  false,
			expected: "Successfully logged in!",
		},
		{
			name:     "invalid format",
			message:  "/login testuser",
			wantErr:  true,
			expected: "Please provide username and password",
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
			
			HandleLogin(bot, update)

			// Verify the results
			if tt.wantErr {
				assert.Contains(t, bot.lastMessage, tt.expected)
			} else {
				assert.Contains(t, bot.lastMessage, tt.expected)
			}
		})
	}
}

// MockBot implements the necessary bot interface for testing
type MockBot struct {
	lastMessage string
	lastChatID  int64
}

func (m *MockBot) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	msg, ok := c.(tgbotapi.MessageConfig)
	if ok {
		m.lastMessage = msg.Text
		m.lastChatID = msg.ChatID
	}
	return tgbotapi.Message{}, nil
}
