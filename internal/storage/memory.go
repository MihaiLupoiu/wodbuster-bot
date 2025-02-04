package storage

import (
	"sync"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
)

type MemoryStorage struct {
	sessions map[int64]models.UserSession
	classes  map[string]models.ClassSchedule
	mu       sync.RWMutex
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		sessions: make(map[int64]models.UserSession),
		classes:  make(map[string]models.ClassSchedule),
	}
}

func (s *MemoryStorage) SaveSession(chatID int64, session models.UserSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[chatID] = session
}

func (s *MemoryStorage) GetSession(chatID int64) (models.UserSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, exists := s.sessions[chatID]
	return session, exists
}

func (s *MemoryStorage) SaveClass(class models.ClassSchedule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.classes[class.ID] = class
}

func (s *MemoryStorage) GetClass(classID string) (models.ClassSchedule, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	class, exists := s.classes[classID]
	return class, exists
}

func (s *MemoryStorage) GetAllClasses() []models.ClassSchedule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	classes := make([]models.ClassSchedule, 0, len(s.classes))
	for _, class := range s.classes {
		classes = append(classes, class)
	}
	return classes
}
