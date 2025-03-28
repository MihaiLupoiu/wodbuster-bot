package handlers

import (
	"testing"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/session"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/mock"
)

func (m *MockWodbuster) BookClass(username, password, day, hour string) error {
	args := m.Called(username, password, day, hour)
	return args.Error(0)
}

func TestBookingHandler_Handle(t *testing.T) {
	const (
		testUsername = "testuser"
		testPassword = "password"
	)

	tests := []struct {
		name       string
		input      string
		isAuth     bool
		setupMocks func(*MockBotAPI, *MockWodbuster)
	}{
		{
			name:   "successful booking",
			input:  "/book Monday 10:00",
			isAuth: true,
			setupMocks: func(api *MockBotAPI, wod *MockWodbuster) {
				wod.On("BookClass", testUsername, testPassword, "Monday", "10:00").Return(nil)
				api.On("Send", mock.Anything).Return(tgbotapi.Message{}, nil)
			},
		},
		{
			name:   "not authenticated",
			input:  "/book Monday 10:00",
			isAuth: false,
			setupMocks: func(api *MockBotAPI, wod *MockWodbuster) {
				api.On("Send", mock.Anything).Return(tgbotapi.Message{}, nil)
			},
		},
		{
			name:   "invalid format",
			input:  "/book Monday",
			isAuth: true,
			setupMocks: func(api *MockBotAPI, wod *MockWodbuster) {
				api.On("Send", mock.Anything).Return(tgbotapi.Message{}, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &MockBotAPI{}
			wod := &MockWodbuster{}
			logger := &MockLogger{}
			mockStorage := storage.NewMockStorage()
			sessions := session.NewManager(mockStorage)

			if tt.isAuth {
				sessions.SetAuthenticated(123, true, testUsername, testPassword)
			}

			handler := NewBookingHandler(api, wod, logger, sessions)

			tt.setupMocks(api, wod)

			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 123},
					Text: tt.input,
				},
			}

			handler.Handle(update)

			api.AssertExpectations(t)
			wod.AssertExpectations(t)
		})
	}
}
