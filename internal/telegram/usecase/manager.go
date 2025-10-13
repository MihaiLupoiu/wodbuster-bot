package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/utils"
)

var (
	ErrUserNotFound                  = errors.New("user not found")
	ErrInvalidEmail                  = errors.New("invalid email")
	ErrInvalidPassword               = errors.New("invalid password")
	ErrInvalidDay                    = errors.New("invalid day")
	ErrInvalidHour                   = errors.New("invalid hour")
	ErrInvalidClassType              = errors.New("invalid class type")
	ErrInvalidBooking                = errors.New("invalid booking")
	ErrInvalidBookingAttempt         = errors.New("invalid booking attempt")
	ErrInvalidBookingAttemptStatus   = errors.New("invalid booking attempt status")
	ErrInvalidBookingAttemptErrorMsg = errors.New("invalid booking attempt error msg")
	ErrInvalidWODBusterLogin         = errors.New("invalid WODBuster login")
)

// Storage defines the interface that all storage implementations must satisfy
type Storage interface {
	SaveUser(ctx context.Context, user models.User) error
	GetUser(ctx context.Context, chatID int64) (models.User, bool)
	SaveClassBookingSchedule(ctx context.Context, chatID int64, class models.ClassBookingSchedule) error
	GetClassBookingSchedules(ctx context.Context, chatID int64) ([]models.ClassBookingSchedule, bool)
	// Booking attempt methods
	SaveBookingAttempt(ctx context.Context, attempt models.BookingAttempt) error
	GetAllPendingBookings(ctx context.Context) ([]models.BookingAttempt, error)
	UpdateBookingStatus(ctx context.Context, attemptID string, status string, errorMsg string) error
}

type APIClient interface {
	LogIn(ctx context.Context, email, password string) (*http.Cookie, error)
	BookClass(ctx context.Context, email, password string, day, classType, hour string) error
}

type Manager struct {
	storage          Storage
	clientAPI        APIClient
	encryptionKey    string
	bookingScheduler *BookingScheduler
	logger           *slog.Logger
}

// NewManager creates a new manager with injected dependencies
func NewManager(
	storage Storage,
	clientAPI APIClient,
	encryptionKey string,
	bookingScheduler *BookingScheduler,
	logger *slog.Logger,
) *Manager {
	return &Manager{
		storage:          storage,
		clientAPI:        clientAPI,
		encryptionKey:    encryptionKey,
		bookingScheduler: bookingScheduler,
		logger:           logger,
	}
}

// StartBookingScheduler starts the Saturday cronjob
func (m *Manager) StartBookingScheduler() error {
	return m.bookingScheduler.Start()
}

// StopBookingScheduler stops the booking scheduler
func (m *Manager) StopBookingScheduler() {
	m.bookingScheduler.Stop()
}

func (m *Manager) IsAuthenticated(ctx context.Context, chatID int64) bool {
	user, exists := m.storage.GetUser(ctx, chatID)
	return exists && user.IsAuthenticated
}

func (m *Manager) GetUser(ctx context.Context, chatID int64) (models.User, bool) {
	return m.storage.GetUser(ctx, chatID)
}

func (m *Manager) LogInAndSave(ctx context.Context, chatID int64, email, password string) error {
	// Test login with WODBuster first to validate credentials and get session cookie
	sessionCookie, err := m.testWODBusterLogin(ctx, email, password)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidWODBusterLogin, err)
	}

	// Encrypt the password before storing
	encryptedPassword, err := utils.EncryptPassword(password, m.encryptionKey)
	if err != nil {
		m.logger.Error("Failed to encrypt password", "error", err, "chat_id", chatID)
		return err
	}

	user := models.User{
		ChatID:                 chatID,
		IsAuthenticated:        true,
		Email:                  email,
		Password:               encryptedPassword,
		ClassBookingSchedules:  []models.ClassBookingSchedule{},
		WODBusterSessionCookie: sessionCookie,
		SessionExpiresAt:       sessionCookie.Expires,
		SessionValid:           true,
		LastLoginTime:          time.Now(),
		CreatedAt:              time.Now(),
		UpdatedAt:              time.Now(),
	}

	m.logger.Info("Successfully validated login and saved user", "chat_id", chatID, "email", email)
	return m.storage.SaveUser(ctx, user)
}

// testWODBusterLogin validates credentials using the injected API client
func (m *Manager) testWODBusterLogin(ctx context.Context, email, password string) (*http.Cookie, error) {
	// Use the injected client to validate credentials and get session cookie
	sessionCookie, err := m.clientAPI.LogIn(ctx, email, password)
	if err != nil {
		return nil, fmt.Errorf("login validation failed: %w", err)
	}

	m.logger.Info("Login validation successful", "email", email)
	return sessionCookie, nil
}

func (m *Manager) GetDecryptedPassword(ctx context.Context, chatID int64) (string, error) {
	user, exists := m.storage.GetUser(ctx, chatID)
	if !exists {
		return "", ErrUserNotFound
	}

	return utils.DecryptPassword(user.Password, m.encryptionKey)
}

func (m *Manager) ScheduleBookClass(ctx context.Context, chatID int64, class models.ClassBookingSchedule) error {
	// Save the class booking schedule to user's profile
	err := m.storage.SaveClassBookingSchedule(ctx, chatID, class)
	if err != nil {
		return err
	}

	// Create a booking attempt for the Saturday cronjob
	bookingAttempt := models.BookingAttempt{
		ID:          fmt.Sprintf("%d-%s-%s-%s-%d", chatID, class.Day, class.Hour, class.ClassType, time.Now().Unix()),
		ChatID:      chatID,
		Day:         class.Day,
		Hour:        class.Hour,
		ClassType:   class.ClassType,
		Status:      "pending",
		AttemptTime: calculateNextSaturday(), // When the booking should be attempted
		RetryCount:  0,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	return m.storage.SaveBookingAttempt(ctx, bookingAttempt)
}

// calculateNextSaturday calculates when the next Saturday 12:00 will be
func calculateNextSaturday() time.Time {
	now := time.Now()

	// Find next Saturday
	daysUntilSaturday := (int(time.Saturday) - int(now.Weekday()) + 7) % 7
	if daysUntilSaturday == 0 && now.Hour() >= 12 {
		daysUntilSaturday = 7 // If it's Saturday and past 12:00, go to next Saturday
	}

	nextSaturday := now.AddDate(0, 0, daysUntilSaturday)

	// Set to 12:00 PM (booking time)
	return time.Date(nextSaturday.Year(), nextSaturday.Month(), nextSaturday.Day(), 12, 0, 0, 0, time.UTC)
}

// GetActiveBookings returns currently active booking attempts
func (m *Manager) GetActiveBookings() map[int64]*BookingContext {
	return m.bookingScheduler.GetActiveBookings()
}

// CancelBooking cancels an active booking attempt
func (m *Manager) CancelBooking(chatID int64) bool {
	return m.bookingScheduler.CancelBooking(chatID)
}

// TestUserSession validates if user has a working session
func (m *Manager) TestUserSession(ctx context.Context, chatID int64) error {
	user, exists := m.storage.GetUser(ctx, chatID)
	if !exists {
		return ErrUserNotFound
	}

	if !user.HasValidSession() {
		return fmt.Errorf("user session is invalid or expired")
	}

	// Could optionally test the session by creating a temporary client and checking
	// if the session cookie still works, but for now just check if it exists and hasn't expired
	return nil
}

// GetScheduleInfo returns information about the next scheduled booking run
func (m *Manager) GetScheduleInfo() string {
	return m.bookingScheduler.GetScheduleInfo()
}
