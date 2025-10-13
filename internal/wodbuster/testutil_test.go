package wodbuster

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

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

// setupTestClient creates a test client with standard configuration
func setupTestClient(t *testing.T) *Client {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Set headless mode to false for testing
	client, err := NewClient(testBaseURL, WithLogger(logger), WithHeadlessMode(false))
	assert.NoError(t, err)
	return client
}

// getTestCredentials retrieves test credentials from environment variables
func getTestCredentials(t *testing.T) (string, string) {
	user := os.Getenv("TEST_EMAIL")
	pass := os.Getenv("TEST_PASSWORD")

	if user == "" || pass == "" {
		t.Logf("Environment variables not set. TEST_EMAIL=%s, TEST_PASSWORD=%s", user, pass)
		t.Skip("TEST_EMAIL or TEST_PASSWORD environment variables not set")
	}

	return user, pass
}

