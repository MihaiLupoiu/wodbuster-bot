package wodbuster

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/chromedp/chromedp"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
)

func init() {
	// Try to load from current directory first
	if err := godotenv.Load(); err != nil {
		// If failed, try to load from project root
		projectRoot := filepath.Join("..", "..")
		if err := godotenv.Load(filepath.Join(projectRoot, ".env")); err != nil {
			// Log the error but don't fail - env vars might be set in the environment
			slog.Info("Error loading .env file", "error", err)
		}
	}
}

const testBaseURL = "https://firespain.wodbuster.com"

func setupTestClient(t *testing.T) *Client {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, _ := chromedp.NewExecAllocator(context.Background(), append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Flag("headless", false))...)
	// defer cancel()

	client, err := NewClient(testBaseURL, WithLogger(logger), WithContext(ctx))
	assert.NoError(t, err)
	return client
}

func getTestCredentials(t *testing.T) (string, string) {
	user := os.Getenv("TEST_EMAIL")
	pass := os.Getenv("TEST_PASSWORD")

	if user == "" || pass == "" {
		t.Logf("Environment variables not set. TEST_EMAIL=%s, TEST_PASSWORD=%s", user, pass)
		t.Skip("TEST_EMAIL or TEST_PASSWORD environment variables not set")
	}

	return user, pass
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
	// t.Run("with invalid credentials", func(t *testing.T) {
	// 	client := setupTestClient(t)
	// 	defer client.Close()

	// 	err := client.Login("test_user@gmail.com", "test_pass")
	// 	assert.Error(t, err)
	// 	assert.Contains(t, err.Error(), "login failed")
	// })

	// t.Run("with empty credentials", func(t *testing.T) {
	// 	client := setupTestClient(t)
	// 	defer client.Close()

	// 	err := client.Login("", "")
	// 	assert.Error(t, err)
	// 	assert.Contains(t, err.Error(), "email and password are required")
	// })

	t.Run("with valid credentials", func(t *testing.T) {
		user, pass := getTestCredentials(t)
		client := setupTestClient(t)
		defer client.Close()

		err := client.Login(user, pass)
		assert.NoError(t, err)
	})
}

func TestGetAvailableClasses(t *testing.T) {
	user, pass := getTestCredentials(t)
	client := setupTestClient(t)
	defer client.Close()

	classes, err := client.GetAvailableClasses(user, pass)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to navigate")
	assert.Nil(t, classes)
}

func TestBookClass(t *testing.T) {
	user, pass := getTestCredentials(t)
	client := setupTestClient(t)
	defer client.Close()

	err := client.BookClass(user, pass, "X", "09:00")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to navigate")
}

// func TestRemoveBooking(t *testing.T) {
// 	client := setupTestClient(t)
// 	defer client.Close()

// 	err := client.RemoveBooking("Monday", "10:00")
// 	assert.Error(t, err)
// 	assert.Contains(t, err.Error(), "failed to navigate")
// }
