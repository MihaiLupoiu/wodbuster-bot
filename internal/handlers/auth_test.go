package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
			mockBot := NewMockBot(t)
			mockBot.On("Send", mock.MatchedBy(func(msg tgbotapi.MessageConfig) bool {
				if tt.wantErr {
					assert.Contains(t, msg.Text, tt.expected)
				} else {
					assert.Contains(t, msg.Text, tt.expected)
				}
				return true
			})).Return(tgbotapi.Message{}, nil)
			
			HandleLogin(mockBot, update)
		})
	}
}

