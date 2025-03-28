package storage

import "github.com/MihaiLupoiu/wodbuster-bot/internal/models"

// MockStorage is a mock implementation of Storage interface for testing
type MockStorage struct {
	sessions map[int64]models.UserSession
	classes  map[string]models.ClassSchedule
}

func NewMockStorage() *MockStorage {
	return &MockStorage{
		sessions: make(map[int64]models.UserSession),
		classes:  make(map[string]models.ClassSchedule),
	}
}

func (s *MockStorage) SaveSession(chatID int64, session models.UserSession) {
	s.sessions[chatID] = session
}

func (s *MockStorage) GetSession(chatID int64) (models.UserSession, bool) {
	session, exists := s.sessions[chatID]
	return session, exists
}

func (s *MockStorage) SaveClass(class models.ClassSchedule) {
	s.classes[class.ID] = class
}

func (s *MockStorage) GetClass(classID string) (models.ClassSchedule, bool) {
	class, exists := s.classes[classID]
	return class, exists
}

func (s *MockStorage) GetAllClasses() []models.ClassSchedule {
	classes := make([]models.ClassSchedule, 0, len(s.classes))
	for _, class := range s.classes {
		classes = append(classes, class)
	}
	return classes
}
