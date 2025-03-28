package storage

import (
	"context"
	"testing"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage/functionaltest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMongoStorage_Sessions(t *testing.T) {
	ctx := context.Background()
	client, err := functionaltest.CreateMongoContainer(ctx, t)
	require.NoError(t, err)

	t.Run("SaveAndGetSession", func(t *testing.T) {
		// Given
		storage, err := NewMongoStorage("", "test", WithClient(client))
		require.NoError(t, err)

		session := models.UserSession{
			ChatID:          123,
			IsAuthenticated: true,
			Username:        "testuser",
			Token:           "testtoken",
		}

		// When
		storage.SaveSession(session.ChatID, session)
		got, exists := storage.GetSession(session.ChatID)

		// Then
		assert.True(t, exists)
		assert.Equal(t, session, got)
	})

	t.Run("GetNonExistentSession", func(t *testing.T) {
		// Given
		storage, err := NewMongoStorage("", "test", WithClient(client))
		require.NoError(t, err)

		// When
		_, exists := storage.GetSession(999)

		// Then
		assert.False(t, exists)
	})
}

func TestMongoStorage_Classes(t *testing.T) {
	ctx := context.Background()
	client, err := functionaltest.CreateMongoContainer(ctx, t)
	require.NoError(t, err)

	t.Run("SaveAndGetClass", func(t *testing.T) {
		// Given
		storage, err := NewMongoStorage("", "test", WithClient(client))
		require.NoError(t, err)

		class := models.ClassSchedule{
			ID:        "class1",
			Day:       "Monday",
			Hour:      "10:00",
			Available: true,
			BookedBy:  "testuser",
		}

		// When
		storage.SaveClass(class)
		got, exists := storage.GetClass(class.ID)

		// Then
		assert.True(t, exists)
		assert.Equal(t, class, got)
	})

	t.Run("GetNonExistentClass", func(t *testing.T) {
		// Given
		storage, err := NewMongoStorage("", "test", WithClient(client))
		require.NoError(t, err)

		// When
		_, exists := storage.GetClass("nonexistent")

		// Then
		assert.False(t, exists)
	})

	t.Run("GetAllClasses", func(t *testing.T) {
		// Given
		storage, err := NewMongoStorage("", "test", WithClient(client))
		require.NoError(t, err)

		class1 := models.ClassSchedule{
			ID:        "class1",
			Day:       "Monday",
			Hour:      "10:00",
			Available: true,
		}
		class2 := models.ClassSchedule{
			ID:        "class2",
			Day:       "Tuesday",
			Hour:      "11:00",
			Available: true,
		}

		// When
		storage.SaveClass(class1)
		storage.SaveClass(class2)
		classes := storage.GetAllClasses()

		// Then
		assert.Len(t, classes, 2)
		assert.Contains(t, classes, class1)
		assert.Contains(t, classes, class2)
	})
}
