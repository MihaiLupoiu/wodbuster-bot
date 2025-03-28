package storage

import (
	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
)

// Storage defines the interface that all storage implementations must satisfy
type Storage interface {
	SaveSession(chatID int64, session models.UserSession)
	GetSession(chatID int64) (models.UserSession, bool)
	SaveClass(class models.ClassSchedule)
	GetClass(classID string) (models.ClassSchedule, bool)
	GetAllClasses() []models.ClassSchedule
}
