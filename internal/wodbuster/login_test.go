package wodbuster

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogin(t *testing.T) {
	// t.Run("with invalid credentials", func(t *testing.T) {
	// 	client := setupTestClient(t)
	// 	defer client.Close()
	//
	// 	err := client.LogIn(context.Background(), "test_user@gmail.com", "test_pass")
	// 	assert.Error(t, err)
	// 	assert.Contains(t, err.Error(), "login failed")
	// })

	// t.Run("with empty credentials", func(t *testing.T) {
	// 	client := setupTestClient(t)
	// 	defer client.Close()
	//
	// 	err := client.LogIn(context.Background(), "", "")
	// 	assert.Error(t, err)
	// 	assert.Contains(t, err.Error(), "email and password are required")
	// })

	t.Run("with valid credentials", func(t *testing.T) {
		user, pass := getTestCredentials(t)
		client := setupTestClient(t)
		defer client.Close()

		cookie, err := client.LogIn(context.Background(), user, pass)
		assert.NoError(t, err)
		assert.NotEmpty(t, cookie)
	})
}

