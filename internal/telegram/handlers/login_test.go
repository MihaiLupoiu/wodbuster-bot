package handlers

import (
	"testing"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/session"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockBotAPI struct {
	mock.Mock
}

func (m *MockBotAPI) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	args := m.Called(c)
	return args.Get(0).(tgbotapi.Message), args.Error(1)
}

var _ BotAPI = (*MockBotAPI)(nil) // Verify MockBotAPI implements BotAPI

type MockWodbuster struct {
	mock.Mock
}

func (m *MockWodbuster) Login(username, password string) error {
	args := m.Called(username, password)
	return args.Error(0)
}

type MockLogger struct {
	mock.Mock
}

func (m *MockLogger) Error(msg string, args ...interface{}) {
	m.Called(msg, args)
}

func TestLoginHandler_Handle(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		setupMocks   func(*MockBotAPI, *MockWodbuster)
		expectedAuth bool
	}{
		{
			name:  "successful login",
			input: "/login testuser password123",
			setupMocks: func(api *MockBotAPI, wod *MockWodbuster) {
				wod.On("Login", "testuser", "password123").Return(nil)
				api.On("Send", mock.Anything).Return(tgbotapi.Message{}, nil)
			},
			expectedAuth: true,
		},
		{
			name:  "invalid format",
			input: "/login testuser",
			setupMocks: func(api *MockBotAPI, wod *MockWodbuster) {
				api.On("Send", mock.Anything).Return(tgbotapi.Message{}, nil)
			},
			expectedAuth: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &MockBotAPI{}
			wod := &MockWodbuster{}
			logger := &MockLogger{}
			mockStorage := storage.NewMockStorage()
			sessions := session.NewManager(mockStorage)

			handler := NewLoginHandler(api, wod, logger, sessions)

			tt.setupMocks(api, wod)

			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 123},
					Text: tt.input,
				},
			}

			handler.Handle(update)

			assert.Equal(t, tt.expectedAuth, sessions.IsAuthenticated(123))
			api.AssertExpectations(t)
			wod.AssertExpectations(t)
		})
	}
}
