package handlers

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/mock"
)

func TestLoginHandler_Handle(t *testing.T) {
	const testChatID int64 = 123

	tests := []struct {
		name       string
		input      string
		setupMocks func(*MockLogInBotAPI, *MockLogInManager)
	}{
		{
			name:  "successful login",
			input: "/login testuser@email.com password123",
			setupMocks: func(api *MockLogInBotAPI, manager *MockLogInManager) {
				manager.EXPECT().LogInAndSave(mock.Anything, testChatID, "testuser@email.com", "password123")
				api.EXPECT().Send(mock.MatchedBy(func(c tgbotapi.Chattable) bool {
					msg, ok := c.(tgbotapi.MessageConfig)
					return ok && msg.Text == "Login successful! You can now use /book and /remove commands."
				})).Return(tgbotapi.Message{}, nil)
			},
		},
		{
			name:  "invalid format",
			input: "/login testuser",
			setupMocks: func(api *MockLogInBotAPI, manager *MockLogInManager) {
				api.EXPECT().Send(mock.MatchedBy(func(c tgbotapi.Chattable) bool {
					msg, ok := c.(tgbotapi.MessageConfig)
					return ok && msg.Text == "Please provide email and password: /login email password"
				})).Return(tgbotapi.Message{}, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := NewMockLogInBotAPI(t)
			manager := NewMockLogInManager(t)

			handler := NewLoginHandler(api, manager)

			tt.setupMocks(api, manager)

			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: testChatID},
					Text: tt.input,
				},
			}

			handler.Handle(update)
		})
	}
}
