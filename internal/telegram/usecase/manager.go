package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/booking"
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
	// ListUsers is every athlete; the weekly run books from their schedules.
	ListUsers(ctx context.Context) ([]models.User, error)
	// Booking attempts are the history of each weekly run, one per class and week.
	SaveBookingAttempt(ctx context.Context, attempt models.BookingAttempt) error
	UpdateBookingStatus(ctx context.Context, attemptID string, status string, errorMsg string) error
}

// Booker is the bot's view of pkg/wodbuster, implemented by internal/booking.
//
// Authenticate answers one question — do these credentials work — and keeps
// nothing: a WodBuster session is a session cookie, so it will not survive
// until the opening. Every run logs in again, which is why the password is
// stored (encrypted) and the session is not.
type Booker interface {
	Authenticate(ctx context.Context, email, password string) error
	Run(ctx context.Context, email, password string, targets []booking.Target,
		o booking.RunOptions) ([]booking.Outcome, error)
}

type Manager struct {
	storage          Storage
	booker           Booker
	encryptionKey    string
	bookingScheduler *BookingScheduler
	logger           *slog.Logger
}

// NewManager creates a new manager with injected dependencies
func NewManager(
	storage Storage,
	booker Booker,
	encryptionKey string,
	bookingScheduler *BookingScheduler,
	logger *slog.Logger,
) *Manager {
	return &Manager{
		storage:          storage,
		booker:           booker,
		encryptionKey:    encryptionKey,
		bookingScheduler: bookingScheduler,
		logger:           logger,
	}
}

// StartBookingScheduler starts the weekly cronjob
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
	// A real login against the real site, so that bad credentials are caught
	// here and not at noon on Sunday.
	if err := m.booker.Authenticate(ctx, email, password); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidWODBusterLogin, err)
	}

	encryptedPassword, err := utils.EncryptPassword(password, m.encryptionKey)
	if err != nil {
		m.logger.Error("Failed to encrypt password", "error", err, "chat_id", chatID)
		return err
	}

	now := time.Now()
	user := models.User{
		ChatID:                chatID,
		ClassBookingSchedules: []models.ClassBookingSchedule{},
		CreatedAt:             now,
	}
	// Logging in again — a changed password, a /login to be sure — must not
	// cost the athlete their classes. It used to: the user was rebuilt from
	// scratch and the save replaced the whole document.
	if existing, ok := m.storage.GetUser(ctx, chatID); ok {
		user = existing
	}
	user.IsAuthenticated = true
	user.Email = email
	user.Password = encryptedPassword
	user.LastLoginTime = now
	user.UpdatedAt = now

	m.logger.Info("Successfully validated login and saved user", "chat_id", chatID, "email", email)
	return m.storage.SaveUser(ctx, user)
}

func (m *Manager) GetDecryptedPassword(ctx context.Context, chatID int64) (string, error) {
	user, exists := m.storage.GetUser(ctx, chatID)
	if !exists {
		return "", ErrUserNotFound
	}

	return utils.DecryptPassword(user.Password, m.encryptionKey)
}

// ScheduleBookClass adds a class to the athlete's weekly schedule. Every
// Sunday run books whatever is on the schedule; there is nothing else to queue.
func (m *Manager) ScheduleBookClass(ctx context.Context, chatID int64, class models.ClassBookingSchedule) error {
	return m.storage.SaveClassBookingSchedule(ctx, chatID, class)
}

// GetActiveBookings returns currently active booking attempts
func (m *Manager) GetActiveBookings() map[int64]*BookingContext {
	return m.bookingScheduler.GetActiveBookings()
}

// CancelBooking cancels an active booking attempt
func (m *Manager) CancelBooking(chatID int64) bool {
	return m.bookingScheduler.CancelBooking(chatID)
}

// TestUserSession checks that the stored credentials still work, by logging in
// with them. There is no stored session to test any more — and "can I log in
// right now" is the question that actually matters before an opening.
func (m *Manager) TestUserSession(ctx context.Context, chatID int64) error {
	user, exists := m.storage.GetUser(ctx, chatID)
	if !exists {
		return ErrUserNotFound
	}
	password, err := utils.DecryptPassword(user.Password, m.encryptionKey)
	if err != nil {
		return fmt.Errorf("could not read the stored password: %w", err)
	}
	return m.booker.Authenticate(ctx, user.Email, password)
}

// Rehearse books nothing and reports what the run would find right now.
func (m *Manager) Rehearse(ctx context.Context, chatID int64) ([]booking.Outcome, error) {
	return m.bookingScheduler.Rehearse(ctx, chatID)
}

// GetScheduleInfo returns information about the next scheduled booking run
func (m *Manager) GetScheduleInfo() string {
	return m.bookingScheduler.GetScheduleInfo()
}
