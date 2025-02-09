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
	client, err := NewClient(testBaseURL, WithLogger(logger))
	assert.NoError(t, err)
	return client
}

func TestNewClient(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	t.Run("with valid URL", func(t *testing.T) {
		client, err := NewClient(testBaseURL, WithLogger(logger))
		assert.NoError(t, err)
		assert.NotNil(t, client)
		client.Close()
	})

	t.Run("with empty URL", func(t *testing.T) {
		client, err := NewClient("")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingBaseURL)
		assert.Nil(t, client)
	})
}

func TestNewClientWithOptions(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		opts    []Option
		wantErr bool
	}{
		{
			name:    "with custom context",
			baseURL: testBaseURL,
			opts: []Option{
				WithContext(context.Background()),
				WithLogger(slog.Default()),
			},
			wantErr: false,
		},
		{
			name:    "with custom logger",
			baseURL: testBaseURL,
			opts: []Option{
				WithLogger(slog.Default()),
			},
			wantErr: false,
		},
		{
			name:    "with empty baseURL",
			baseURL: "",
			opts:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.baseURL, tt.opts...)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, client)
			assert.NotNil(t, client.ctx)
			assert.NotNil(t, client.cancel)
			assert.NotNil(t, client.logger)
			client.Close()
		})
	}
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
	assert.Contains(t, err.Error(), "failed to navigate")
	assert.Nil(t, classes)
}

func TestBookClass(t *testing.T) {
	client := setupTestClient(t)
	defer client.Close()

	err := client.BookClass("Monday", "10:00")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to navigate")
}

func TestRemoveBooking(t *testing.T) {
	client := setupTestClient(t)
	defer client.Close()

	err := client.RemoveBooking("Monday", "10:00")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to navigate")
}
