package storage

import (
	"context"
	"testing"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage/functionaltest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMongoStorage(t *testing.T) {
	ctx := context.Background()
	dbName := "test_users"
	client, uri, err := functionaltest.CreateMongoContainer(ctx, t, dbName)
	require.NoError(t, err)

	t.Run("SaveAndGetUser", func(t *testing.T) {
		// Given
		storage, err := NewMongoStorage(uri, dbName, WithClient(client))
		require.NoError(t, err)

		user := models.User{
			ChatID:          123,
			IsAuthenticated: true,
			Email:           "test@example.com",
			Password:        "password123",
			Cookie:          "session-cookie",
		}

		// When
		err = storage.SaveUser(ctx, user)
		require.NoError(t, err)

		// Then
		got, exists := storage.GetUser(ctx, user.ChatID)
		assert.True(t, exists)
		assert.Equal(t, user, got)
	})

	t.Run("GetNonExistentUser", func(t *testing.T) {
		// Given
		storage, err := NewMongoStorage(uri, dbName, WithClient(client))
		require.NoError(t, err)

		// When
		_, exists := storage.GetUser(ctx, 999)

		// Then
		assert.False(t, exists)
	})

	t.Run("SaveAndGetClassBookingSchedule", func(t *testing.T) {
		// Given
		storage, err := NewMongoStorage(uri, dbName, WithClient(client))
		require.NoError(t, err)

		user := models.User{
			ChatID:          456,
			IsAuthenticated: true,
			Email:           "test@example.com",
			Password:        "password123",
		}
		err = storage.SaveUser(ctx, user)
		require.NoError(t, err)

		class := models.ClassBookingSchedule{
			ID:        "Monday-10:00-WOD",
			ClassType: "WOD",
			Day:       "Monday",
			Hour:      "10:00",
		}

		// When
		err = storage.SaveClassBookingSchedule(ctx, user.ChatID, class)
		require.NoError(t, err)

		// Then
		schedules, exists := storage.GetClassBookingSchedules(ctx, user.ChatID)
		assert.True(t, exists)
		assert.Len(t, schedules, 1)
		assert.Equal(t, class, schedules[0])
	})

	t.Run("SaveMultipleClassBookingSchedules", func(t *testing.T) {
		// Given
		storage, err := NewMongoStorage(uri, dbName, WithClient(client))
		require.NoError(t, err)

		user := models.User{
			ChatID:          789,
			IsAuthenticated: true,
			Email:           "test@example.com",
			Password:        "password123",
		}
		err = storage.SaveUser(ctx, user)
		require.NoError(t, err)

		class1 := models.ClassBookingSchedule{
			ID:        "Monday-10:00-WOD",
			ClassType: "WOD",
			Day:       "Monday",
			Hour:      "10:00",
		}
		class2 := models.ClassBookingSchedule{
			ID:        "Tuesday-11:00-Open",
			ClassType: "Open",
			Day:       "Tuesday",
			Hour:      "11:00",
		}

		// When
		err = storage.SaveClassBookingSchedule(ctx, user.ChatID, class1)
		require.NoError(t, err)
		err = storage.SaveClassBookingSchedule(ctx, user.ChatID, class2)
		require.NoError(t, err)

		// Then
		schedules, exists := storage.GetClassBookingSchedules(ctx, user.ChatID)
		assert.True(t, exists)
		assert.Len(t, schedules, 2)
		assert.Contains(t, schedules, class1)
		assert.Contains(t, schedules, class2)
	})

	t.Run("SaveClassBookingScheduleForNonExistentUser", func(t *testing.T) {
		// Given
		storage, err := NewMongoStorage(uri, dbName, WithClient(client))
		require.NoError(t, err)

		class := models.ClassBookingSchedule{
			ID:        "Monday-10:00-WOD",
			ClassType: "WOD",
			Day:       "Monday",
			Hour:      "10:00",
		}

		// When
		err = storage.SaveClassBookingSchedule(ctx, 999, class)

		// Then
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "user with chatID 999 does not exist")
	})

	t.Run("GetClassBookingSchedulesForNonExistentUser", func(t *testing.T) {
		// Given
		storage, err := NewMongoStorage(uri, dbName, WithClient(client))
		require.NoError(t, err)

		// When
		schedules, exists := storage.GetClassBookingSchedules(ctx, 999)

		// Then
		assert.False(t, exists)
		assert.Nil(t, schedules)
	})
}
