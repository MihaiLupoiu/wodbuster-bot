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
	mu       sync.RWMutex
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		users:    make(map[int64]models.User),
		bookings: make(map[string]models.BookingAttempt),
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

func (m *MemoryStorage) GetAllPendingBookings(ctx context.Context) ([]models.BookingAttempt, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var pending []models.BookingAttempt
	for _, booking := range m.bookings {
		if booking.Status == "pending" {
			pending = append(pending, booking)
		}
	}

	return pending, nil
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
