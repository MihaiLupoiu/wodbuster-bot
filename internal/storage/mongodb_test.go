package storage

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage/functionaltest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMongoStorage(t *testing.T) {
	ctx := context.Background()
	dbName := "test_users"
	_, uri, err := functionaltest.CreateMongoContainer(ctx, t, dbName)
	require.NoError(t, err)

	t.Run("SaveAndGetUser", func(t *testing.T) {
		// Given
		storage, err := NewMongoStorage(uri, dbName)
		require.NoError(t, err)
		defer storage.Close()

		user := models.User{
			ChatID:          123,
			IsAuthenticated: true,
			Email:           "test@example.com",
			Password:        "password123",
			WODBusterSessionCookie: &http.Cookie{
				Name:     ".WBAuth",
				Value:    "session-cookie",
				Path:     "/",
				Domain:   "wodbuster.com",
				Expires:  time.Now().Add(24 * time.Hour),
				Secure:   true,
				HttpOnly: true,
			},
		}

		// When
		err = storage.SaveUser(ctx, user)
		require.NoError(t, err)

		// Then
		got, exists := storage.GetUser(ctx, user.ChatID)
		assert.True(t, exists)
		assert.Equal(t, user.ChatID, got.ChatID)
		assert.Equal(t, user.IsAuthenticated, got.IsAuthenticated)
		assert.Equal(t, user.Email, got.Email)
		assert.Equal(t, user.Password, got.Password)
		assert.Equal(t, user.WODBusterSessionCookie.Name, got.WODBusterSessionCookie.Name)
		assert.Equal(t, user.WODBusterSessionCookie.Value, got.WODBusterSessionCookie.Value)
		assert.Equal(t, user.WODBusterSessionCookie.Path, got.WODBusterSessionCookie.Path)
		assert.Equal(t, user.WODBusterSessionCookie.Domain, got.WODBusterSessionCookie.Domain)
		assert.Equal(t, user.WODBusterSessionCookie.Secure, got.WODBusterSessionCookie.Secure)
		assert.Equal(t, user.WODBusterSessionCookie.HttpOnly, got.WODBusterSessionCookie.HttpOnly)
	})

	t.Run("GetNonExistentUser", func(t *testing.T) {
		// Given
		storage, err := NewMongoStorage(uri, dbName)
		require.NoError(t, err)
		defer storage.Close()

		// When
		_, exists := storage.GetUser(ctx, 999)

		// Then
		assert.False(t, exists)
	})

	t.Run("SaveAndGetClassBookingSchedule", func(t *testing.T) {
		// Given
		storage, err := NewMongoStorage(uri, dbName)
		require.NoError(t, err)
		defer storage.Close()

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
		storage, err := NewMongoStorage(uri, dbName)
		require.NoError(t, err)
		defer storage.Close()

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
		storage, err := NewMongoStorage(uri, dbName)
		require.NoError(t, err)
		defer storage.Close()

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
		assert.Contains(t, err.Error(), "user with chat ID 999 not found")
	})

	t.Run("GetClassBookingSchedulesForNonExistentUser", func(t *testing.T) {
		// Given
		storage, err := NewMongoStorage(uri, dbName)
		require.NoError(t, err)
		defer storage.Close()

		// When
		schedules, exists := storage.GetClassBookingSchedules(ctx, 999)

		// Then
		assert.False(t, exists)
		assert.Nil(t, schedules)
	})
}
