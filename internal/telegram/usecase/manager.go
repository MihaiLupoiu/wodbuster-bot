package usecase

import (
	"context"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
)

// Storage defines the interface that all storage implementations must satisfy
type Storage interface {
	SaveUser(ctx context.Context, user models.User) error
	GetUser(ctx context.Context, chatID int64) (models.User, bool)
	SaveClassBookingSchedule(ctx context.Context, chatID int64, class models.ClassBookingSchedule) error
	GetClassBookingSchedules(ctx context.Context, chatID int64) ([]models.ClassBookingSchedule, bool)
}

type APIClient interface {
	LogIn(ctx context.Context, email, password string) (string, error)
	BookClass(ctx context.Context, email, password string, day, hour string) error
}

type Manager struct {
	storage   Storage
	clientAPI APIClient
}

func NewManager(storage Storage, cientAPI APIClient) *Manager {
	return &Manager{
		storage:   storage,
		clientAPI: cientAPI,
	}
}

func (m *Manager) IsAuthenticated(ctx context.Context, chatID int64) bool {
	user, exists := m.storage.GetUser(ctx, chatID)
	return exists && user.IsAuthenticated
}

func (m *Manager) GetUser(ctx context.Context, chatID int64) (models.User, bool) {
	return m.storage.GetUser(ctx, chatID)
}

func (m *Manager) LogInAndSave(ctx context.Context, chatID int64, email, password string) {
	user := models.User{
		ChatID:                chatID,
		IsAuthenticated:       false,
		Email:                 email,
		Password:              password,
		Cookie:                "",
		ClassBookingSchedules: []models.ClassBookingSchedule{},
	}
	m.storage.SaveUser(ctx, user)
}

func (m *Manager) ScheduleBookClass(ctx context.Context, chatID int64, class models.ClassBookingSchedule) error {
	err := m.storage.SaveClassBookingSchedule(ctx, chatID, class)
	if err != nil {
		return err
	}

	return nil
}
