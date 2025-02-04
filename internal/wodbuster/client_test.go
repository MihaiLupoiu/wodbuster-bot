package wodbuster

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func setupTestClient(t *testing.T) *Client {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	client, err := NewClient(logger)
	assert.NoError(t, err)
	return client
}

func TestNewClient(t *testing.T) {
	client := setupTestClient(t)
	assert.NotNil(t, client)
	defer client.Close()
}

func TestLogin(t *testing.T) {
	client := setupTestClient(t)
	defer client.Close()

	err := client.Login("test_user", "test_pass")
	assert.Error(t, err) // Expected error since method is not fully implemented
}

func TestGetAvailableClasses(t *testing.T) {
	client := setupTestClient(t)
	defer client.Close()

	classes, err := client.GetAvailableClasses()
	assert.Error(t, err) // Expected error since method is not fully implemented
	assert.Nil(t, classes)
}

func TestBookClass(t *testing.T) {
	client := setupTestClient(t)
	defer client.Close()

	err := client.BookClass("Monday", "10:00")
	assert.Error(t, err) // Expected error since method is not fully implemented
}

func TestRemoveBooking(t *testing.T) {
	client := setupTestClient(t)
	defer client.Close()

	err := client.RemoveBooking("Monday", "10:00")
	assert.Error(t, err) // Expected error since method is not fully implemented
}
