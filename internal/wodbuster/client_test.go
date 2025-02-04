package wodbuster

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

const testBaseURL = "http://localhost:8080"

func setupTestClient(t *testing.T) *Client {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	client, err := NewClient(logger, testBaseURL)
	assert.NoError(t, err)
	return client
}

func TestNewClient(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	t.Run("with valid URL", func(t *testing.T) {
		client, err := NewClient(logger, testBaseURL)
		assert.NoError(t, err)
		assert.NotNil(t, client)
		client.Close()
	})

	t.Run("with empty URL", func(t *testing.T) {
		client, err := NewClient(logger, "")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingBaseURL)
		assert.Nil(t, client)
	})
}

func TestLogin(t *testing.T) {
	t.Run("with valid credentials", func(t *testing.T) {
		client := setupTestClient(t)
		defer client.Close()

		// This will fail in tests since we're not running a real server
		err := client.Login("test_user", "test_pass")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "login failed")
	})

	t.Run("with empty credentials", func(t *testing.T) {
		client := setupTestClient(t)
		defer client.Close()

		err := client.Login("", "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "login failed")
	})
}

func TestGetAvailableClasses(t *testing.T) {
	client := setupTestClient(t)
	defer client.Close()

	classes, err := client.GetAvailableClasses()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNotImplemented)
	assert.Nil(t, classes)
}

func TestBookClass(t *testing.T) {
	client := setupTestClient(t)
	defer client.Close()

	err := client.BookClass("Monday", "10:00")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestRemoveBooking(t *testing.T) {
	client := setupTestClient(t)
	defer client.Close()

	err := client.RemoveBooking("Monday", "10:00")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNotImplemented)
}
