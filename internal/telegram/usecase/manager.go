package usecase

import (
	"context"
	"errors"
	"log/slog"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/utils"
)

var (
	ErrUserNotFound = errors.New("user not found")
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
	storage       Storage
	clientAPI     APIClient
	encryptionKey string
}

func NewManager(storage Storage, clientAPI APIClient, encryptionKey string) *Manager {
	return &Manager{
		storage:       storage,
		clientAPI:     clientAPI,
		encryptionKey: encryptionKey,
	}
}

func (m *Manager) IsAuthenticated(ctx context.Context, chatID int64) bool {
	user, exists := m.storage.GetUser(ctx, chatID)
	return exists && user.IsAuthenticated
}

func (m *Manager) GetUser(ctx context.Context, chatID int64) (models.User, bool) {
	return m.storage.GetUser(ctx, chatID)
}

func (m *Manager) LogInAndSave(ctx context.Context, chatID int64, email, password string) error {
	// Encrypt the password before storing
	encryptedPassword, err := utils.EncryptPassword(password, m.encryptionKey)
	if err != nil {
		slog.Error("Failed to encrypt password", "error", err, "chat_id", chatID)
		return err
	}

	user := models.User{
		ChatID:                chatID,
		IsAuthenticated:       false,
		Email:                 email,
		Password:              encryptedPassword,
		Cookie:                "",
		ClassBookingSchedules: []models.ClassBookingSchedule{},
	}

	return m.storage.SaveUser(ctx, user)
}

func (m *Manager) GetDecryptedPassword(ctx context.Context, chatID int64) (string, error) {
	user, exists := m.storage.GetUser(ctx, chatID)
	if !exists {
		return "", ErrUserNotFound
	}

	return utils.DecryptPassword(user.Password, m.encryptionKey)
}

func (m *Manager) ScheduleBookClass(ctx context.Context, chatID int64, class models.ClassBookingSchedule) error {
	err := m.storage.SaveClassBookingSchedule(ctx, chatID, class)
	if err != nil {
		return err
	}

	return nil
}
