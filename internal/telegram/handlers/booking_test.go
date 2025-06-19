package handlers

import (
	"testing"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/mock"
)

func TestBookingHandler_Handle(t *testing.T) {
	const testChatID int64 = 123

	tests := []struct {
		name       string
		input      string
		isAuth     bool
		setupMocks func(*MockBookingBotAPI, *MockBookingManager)
	}{
		{
			name:   "successful booking",
			input:  "/book Monday 10:00 wod",
			isAuth: true,
			setupMocks: func(api *MockBookingBotAPI, manager *MockBookingManager) {
				manager.EXPECT().IsAuthenticated(mock.Anything, testChatID).Return(true)
				manager.EXPECT().ScheduleBookClass(mock.Anything, testChatID, models.ClassBookingSchedule{
					ID:        "Monday-10:00-Wod",
					Day:       "Monday",
					Hour:      "10:00",
					ClassType: "Wod",
				}).Return(nil)
				api.EXPECT().Send(mock.Anything).Return(tgbotapi.Message{}, nil)
			},
		},
		{
			name:   "not authenticated",
			input:  "/book Monday 10:00 wod",
			isAuth: false,
			setupMocks: func(api *MockBookingBotAPI, manager *MockBookingManager) {
				manager.EXPECT().IsAuthenticated(mock.Anything, testChatID).Return(false)
				api.EXPECT().Send(mock.MatchedBy(func(c tgbotapi.Chattable) bool {
					msg, ok := c.(tgbotapi.MessageConfig)
					return ok && msg.Text == "Please login first using /login command"
				})).Return(tgbotapi.Message{}, nil)
			},
		},
		{
			name:   "invalid format",
			input:  "/book Monday",
			isAuth: true,
			setupMocks: func(api *MockBookingBotAPI, manager *MockBookingManager) {
				manager.EXPECT().IsAuthenticated(mock.Anything, testChatID).Return(true)
				api.EXPECT().Send(mock.MatchedBy(func(c tgbotapi.Chattable) bool {
					msg, ok := c.(tgbotapi.MessageConfig)
					return ok && msg.Text == "Please provide day and hour: /book <day> <hour> <class-type> (e.g., /book Monday 10:00 wod)"
				})).Return(tgbotapi.Message{}, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := NewMockBookingBotAPI(t)
			manager := NewMockBookingManager(t)

			handler := NewBookingHandler(api, manager)

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
