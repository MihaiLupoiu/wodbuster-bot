package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

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
	// Booking attempt methods
	SaveBookingAttempt(ctx context.Context, attempt models.BookingAttempt) error
	GetAllPendingBookings(ctx context.Context) ([]models.BookingAttempt, error)
	UpdateBookingStatus(ctx context.Context, attemptID string, status string, errorMsg string) error
}

type APIClient interface {
	LogIn(ctx context.Context, email, password string) (string, error)
	BookClass(ctx context.Context, email, password string, day, hour string) error
}

type Manager struct {
	storage          Storage
	clientAPI        APIClient
	encryptionKey    string
	sessionManager   *SessionManager
	bookingScheduler *BookingScheduler
	logger           *slog.Logger
}

// NewManager creates a new manager with injected dependencies
func NewManager(
	storage Storage,
	clientAPI APIClient,
	encryptionKey string,
	sessionManager *SessionManager,
	bookingScheduler *BookingScheduler,
	logger *slog.Logger,
) *Manager {
	return &Manager{
		storage:          storage,
		clientAPI:        clientAPI,
		encryptionKey:    encryptionKey,
		sessionManager:   sessionManager,
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
	m.sessionManager.CloseAllClients()
}

func (m *Manager) IsAuthenticated(ctx context.Context, chatID int64) bool {
	user, exists := m.storage.GetUser(ctx, chatID)
	return exists && user.IsAuthenticated
}

func (m *Manager) GetUser(ctx context.Context, chatID int64) (models.User, bool) {
	return m.storage.GetUser(ctx, chatID)
}

func (m *Manager) LogInAndSave(ctx context.Context, chatID int64, email, password string) error {
	// Test login with WODBuster first to validate credentials
	if err := m.testWODBusterLogin(ctx, chatID, email, password); err != nil {
		return fmt.Errorf("WODBuster login validation failed: %w", err)
	}

	// Encrypt the password before storing
	encryptedPassword, err := utils.EncryptPassword(password, m.encryptionKey)
	if err != nil {
		m.logger.Error("Failed to encrypt password", "error", err, "chat_id", chatID)
		return err
	}

	user := models.User{
		ChatID:                chatID,
		IsAuthenticated:       true, // Set to true only after successful WODBuster login
		Email:                 email,
		Password:              encryptedPassword,
		ClassBookingSchedules: []models.ClassBookingSchedule{},
		SessionValid:          false, // Will be set when session is created
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}

	return m.storage.SaveUser(ctx, user)
}

// testWODBusterLogin tests if credentials work with WODBuster
func (m *Manager) testWODBusterLogin(ctx context.Context, chatID int64, email, password string) error {
	// This will create a session and validate the login
	return m.sessionManager.EnsureUserSessionReady(ctx, chatID)
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
	return m.sessionManager.EnsureUserSessionReady(ctx, chatID)
}

// GetScheduleInfo returns information about the next scheduled booking run
func (m *Manager) GetScheduleInfo() string {
	return m.bookingScheduler.GetScheduleInfo()
}
