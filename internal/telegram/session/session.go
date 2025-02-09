package session

import "sync"

type Manager struct {
    sessions map[int64]bool
    mu       sync.RWMutex
}

func NewManager() *Manager {
    return &Manager{
        sessions: make(map[int64]bool),
    }
}

func (m *Manager) IsAuthenticated(chatID int64) bool {
    m.mu.RLock()
    defer m.mu.RUnlock()
    authenticated, exists := m.sessions[chatID]
    return exists && authenticated
}

func (m *Manager) SetAuthenticated(chatID int64, status bool) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.sessions[chatID] = status
}
