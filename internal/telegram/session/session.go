package session

import (
	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage"
)

type Manager struct {
	storage storage.Storage
}

func NewManager(store storage.Storage) *Manager {
	return &Manager{
		storage: store,
	}
}

func (m *Manager) IsAuthenticated(chatID int64) bool {
	session, exists := m.storage.GetSession(chatID)
	return exists && session.IsAuthenticated
}

func (m *Manager) SetAuthenticated(chatID int64, status bool, username, password string) {
	session := models.UserSession{
		ChatID:          chatID,
		IsAuthenticated: status,
		Username:        username,
		Password:        password,
	}
	m.storage.SaveSession(chatID, session)
}
