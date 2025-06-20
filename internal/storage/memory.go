package storage

import (
	"context"
	"sync"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
)

type MemoryStorage struct {
	users map[int64]models.User
	mu    sync.RWMutex
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		users: make(map[int64]models.User),
	}
}

func (s *MemoryStorage) SaveUser(_ context.Context, user models.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[user.ChatID] = user
	return nil
}

func (s *MemoryStorage) GetUser(_ context.Context, chatID int64) (models.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, exists := s.users[chatID]
	return user, exists
}

func (s *MemoryStorage) SaveClassBookingSchedule(_ context.Context, chatID int64, class models.ClassBookingSchedule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.users[chatID]
	u.ClassBookingSchedules = append(u.ClassBookingSchedules, class)
	s.users[chatID] = u
	return nil
}

func (s *MemoryStorage) GetClassBookingSchedules(_ context.Context, chatID int64) ([]models.ClassBookingSchedule, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, exists := s.users[chatID]
	if !exists {
		return nil, false
	}
	return user.ClassBookingSchedules, true
}
