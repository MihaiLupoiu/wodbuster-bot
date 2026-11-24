package storage

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
)

type MemoryStorage struct {
	users    map[int64]models.User
	bookings map[string]models.BookingAttempt
	sessions map[int64]models.StoredSession
	mu       sync.RWMutex
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		users:    make(map[int64]models.User),
		bookings: make(map[string]models.BookingAttempt),
		sessions: make(map[int64]models.StoredSession),
	}
}

func (m *MemoryStorage) SaveUser(ctx context.Context, user models.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	user.UpdatedAt = time.Now()
	m.users[user.ChatID] = user
	return nil
}

func (m *MemoryStorage) GetUser(ctx context.Context, chatID int64) (models.User, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, exists := m.users[chatID]
	return user, exists
}

func (m *MemoryStorage) SaveClassBookingSchedule(ctx context.Context, chatID int64, class models.ClassBookingSchedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	user, exists := m.users[chatID]
	if !exists {
		return fmt.Errorf("user with chat ID %d not found", chatID)
	}

	// Check if class already exists and update it, otherwise append
	found := false
	for i, existingClass := range user.ClassBookingSchedules {
		if existingClass.ID == class.ID {
			user.ClassBookingSchedules[i] = class
			found = true
			break
		}
	}

	if !found {
		user.ClassBookingSchedules = append(user.ClassBookingSchedules, class)
	}

	user.UpdatedAt = time.Now()
	m.users[chatID] = user
	return nil
}

func (m *MemoryStorage) GetClassBookingSchedules(ctx context.Context, chatID int64) ([]models.ClassBookingSchedule, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, exists := m.users[chatID]
	if !exists {
		return nil, false
	}

	return user.ClassBookingSchedules, true
}

// BookingAttempt methods
func (m *MemoryStorage) SaveBookingAttempt(ctx context.Context, attempt models.BookingAttempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	attempt.UpdatedAt = time.Now()
	m.bookings[attempt.ID] = attempt
	return nil
}

func (m *MemoryStorage) ListUsers(ctx context.Context) ([]models.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	users := make([]models.User, 0, len(m.users))
	for _, u := range m.users {
		users = append(users, u)
	}
	return users, nil
}

func (m *MemoryStorage) UpdateBookingStatus(ctx context.Context, attemptID string, status string, errorMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	booking, exists := m.bookings[attemptID]
	if !exists {
		return fmt.Errorf("booking attempt %s not found", attemptID)
	}

	booking.Status = status
	booking.ErrorMsg = errorMsg
	booking.UpdatedAt = time.Now()
	m.bookings[attemptID] = booking

	return nil
}

// SaveSession stores the athlete's session, replacing any older one.
func (m *MemoryStorage) SaveSession(ctx context.Context, s models.StoredSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s.UpdatedAt = time.Now()
	m.sessions[s.ChatID] = s
	return nil
}

// GetSession returns the athlete's stored session; ok is false when there is none.
func (m *MemoryStorage) GetSession(ctx context.Context, chatID int64) (models.StoredSession, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.sessions[chatID]
	return s, ok, nil
}

// DeleteSession forgets the athlete's session; a missing one is fine.
func (m *MemoryStorage) DeleteSession(ctx context.Context, chatID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.sessions, chatID)
	return nil
}

// ReplaceSession stores s only if the stored session's UpdatedAt is still
// prevUpdatedAt; see MongoStorage.ReplaceSession.
func (m *MemoryStorage) ReplaceSession(ctx context.Context, prevUpdatedAt time.Time, s models.StoredSession) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, ok := m.sessions[s.ChatID]
	if !ok || !cur.UpdatedAt.Equal(prevUpdatedAt) {
		return false, nil
	}
	s.UpdatedAt = time.Now()
	m.sessions[s.ChatID] = s
	return true, nil
}

// DeleteSessionIfUnchanged is DeleteSession with ReplaceSession's condition.
func (m *MemoryStorage) DeleteSessionIfUnchanged(ctx context.Context, chatID int64, prevUpdatedAt time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, ok := m.sessions[chatID]
	if !ok || !cur.UpdatedAt.Equal(prevUpdatedAt) {
		return false, nil
	}
	delete(m.sessions, chatID)
	return true, nil
}
