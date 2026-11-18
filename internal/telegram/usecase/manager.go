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
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
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
	ErrInvalidStatus                 = errors.New("invalid status")
	ErrAdminStatus                   = errors.New("an admin's status cannot be changed")
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
	// Sessions are each athlete's stored WodBuster login, one per chat id,
	// kept apart from the user. GetSession's ok is false when there is none;
	// an error means the store could not answer.
	SaveSession(ctx context.Context, s models.StoredSession) error
	GetSession(ctx context.Context, chatID int64) (models.StoredSession, bool, error)
	DeleteSession(ctx context.Context, chatID int64) error
}

// Booker is the bot's view of pkg/wodbuster, implemented by internal/booking.
//
// Authenticate logs in once and returns the session. The password is still
// stored (encrypted): the browser login stays the fallback whenever a stored
// session stops working.
type Booker interface {
	Authenticate(ctx context.Context, email, password string) (wodbuster.Session, error)
	// Login logs in the way a run does: retries, the login gate, onRetry
	// hearing about each failure that will be tried again.
	Login(ctx context.Context, email, password string,
		onRetry func(attempt, of int, err error)) (wodbuster.Session, error)
	// CheckSession asks WodBuster whether it still accepts sess, with one
	// schedule read, and returns the session as renewed by the server.
	// wodbuster.ErrSessionExpired means refused; ErrChallenge and other
	// errors mean nothing was learned.
	CheckSession(ctx context.Context, sess wodbuster.Session) (wodbuster.Session, error)
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

// Bootstrap gives every user a role and a status. Users saved before those
// fields existed were all using the bot, so they become active users. Admins
// are made in the database, by setting role to "admin"; nothing here creates
// one. Run once at startup; it changes nothing the second time.
func (m *Manager) Bootstrap(ctx context.Context) error {
	users, err := m.storage.ListUsers(ctx)
	if err != nil {
		return fmt.Errorf("could not read the users: %w", err)
	}
	updated := 0
	for _, u := range users {
		before := u
		if u.Status == "" {
			u.Status = models.StatusActive
		}
		if u.Role == "" {
			u.Role = models.RoleUser
		}
		if u.Role == before.Role && u.Status == before.Status {
			continue
		}
		u.UpdatedAt = time.Now()
		if err := m.storage.SaveUser(ctx, u); err != nil {
			return fmt.Errorf("could not update chat %d: %w", u.ChatID, err)
		}
		m.logger.Info("user role and status set", "chat_id", u.ChatID,
			"role", u.Role, "status", u.Status, "was_role", before.Role, "was_status", before.Status)
		updated++
	}
	m.logger.Info("users bootstrapped", "users", len(users), "updated", updated,
		"admins", len(models.ActiveAdmins(users)))
	return nil
}

// ListUsers is every user, for the admin's /users and /announcement.
func (m *Manager) ListUsers(ctx context.Context) ([]models.User, error) {
	return m.storage.ListUsers(ctx)
}

// SetUserStatus is an admin approving, disabling or re-enabling an account.
// Admins are left alone: an admin disabling another, or themselves, is the one
// way the bot could end up with nobody able to approve anyone.
func (m *Manager) SetUserStatus(ctx context.Context, chatID int64, status string) (models.User, error) {
	if !models.ValidStatus(status) {
		return models.User{}, fmt.Errorf("%w: %q", ErrInvalidStatus, status)
	}
	user, ok := m.storage.GetUser(ctx, chatID)
	if !ok {
		return models.User{}, ErrUserNotFound
	}
	if user.IsAdmin() {
		return user, ErrAdminStatus
	}
	if user.Status == status {
		return user, nil
	}
	was := user.Status
	user.Status = status
	user.UpdatedAt = time.Now()
	if err := m.storage.SaveUser(ctx, user); err != nil {
		return user, err
	}
	m.logger.Info("user status changed", "chat_id", chatID, "email", user.Email,
		"from", was, "to", status)
	// A disabled account is not booked for, so its session is a credential
	// nobody needs. Approving it again later stores a new one at the next
	// /login or Sunday preflight.
	if status == models.StatusDisabled {
		if err := m.storage.DeleteSession(ctx, chatID); err != nil {
			m.logger.Error("could not delete the disabled user's session", "chat_id", chatID, "error", err)
		}
	}
	return user, nil
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
	sess, err := m.booker.Authenticate(ctx, email, password)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidWODBusterLogin, err)
	}

	encryptedPassword, err := utils.EncryptPassword(password, m.encryptionKey)
	if err != nil {
		m.logger.Error("Failed to encrypt password", "error", err, "chat_id", chatID)
		return err
	}

	now := time.Now()
	// A newcomer's login is checked and kept, but nothing is booked for them
	// until an admin approves the account.
	user := models.User{
		ChatID:                chatID,
		ClassBookingSchedules: []models.ClassBookingSchedule{},
		Role:                  models.RoleUser,
		Status:                models.StatusPending,
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
	if err := m.storage.SaveUser(ctx, user); err != nil {
		return err
	}
	m.storeLoginSession(ctx, chatID, sess)
	return nil
}

// storeLoginSession keeps the session /login just made, replacing any older
// one: a new login means the old session belongs to an old password or an old
// device. Failing to store it is not the athlete's problem: the login worked,
// and the next run logs in on its own.
func (m *Manager) storeLoginSession(ctx context.Context, chatID int64, sess wodbuster.Session) {
	if err := sess.Valid(); err != nil {
		m.logger.Error("the login returned no usable session; nothing stored", "chat_id", chatID, "error", err)
		return
	}
	st, err := sealSession(m.encryptionKey, chatID, sess, time.Now())
	if err == nil {
		err = m.storage.SaveSession(ctx, st)
	}
	if err != nil {
		m.logger.Error("could not store the session from /login", "chat_id", chatID, "error", err)
		return
	}
	m.logger.Info("session stored from /login", "chat_id", chatID,
		"auth_expires_at", st.AuthExpiresAt, "trusted_device", st.TrustedDevice)
}

// GetSession is the athlete's stored session, for /status and /users. It
// never leaves this package decrypted.
func (m *Manager) GetSession(ctx context.Context, chatID int64) (models.StoredSession, bool, error) {
	return m.storage.GetSession(ctx, chatID)
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

// ResetSchedule empties the athlete's weekly schedule so they can set it up
// again with /book, and returns what was on it. It only touches the schedule:
// classes already booked on WodBuster stay booked.
func (m *Manager) ResetSchedule(ctx context.Context, chatID int64) ([]models.ClassBookingSchedule, error) {
	user, ok := m.storage.GetUser(ctx, chatID)
	if !ok {
		return nil, ErrUserNotFound
	}
	removed := user.ClassBookingSchedules
	user.ClassBookingSchedules = []models.ClassBookingSchedule{}
	if err := m.storage.SaveUser(ctx, user); err != nil {
		return nil, err
	}
	return removed, nil
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
	_, err = m.booker.Authenticate(ctx, user.Email, password)
	return err
}

// Rehearse books nothing and reports what the run would find right now.
func (m *Manager) Rehearse(ctx context.Context, chatID int64) ([]booking.Outcome, error) {
	return m.bookingScheduler.Rehearse(ctx, chatID)
}

// GetScheduleInfo returns information about the next scheduled booking run
func (m *Manager) GetScheduleInfo() string {
	return m.bookingScheduler.GetScheduleInfo()
}
