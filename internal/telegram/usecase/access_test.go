package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage"
)

// A newcomer's credentials are checked and saved, so approving them is all
// that is left; nothing is booked for them until then.
func TestANewcomerIsSavedAsPending(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	m := managerWith(t, store)
	m.SetAdmin(1)

	require.NoError(t, m.LogInAndSave(ctx, 7, "athlete@example.com", "new-password"))

	got, _ := store.GetUser(ctx, 7)
	assert.Equal(t, models.StatusPending, got.Status)
	assert.Equal(t, models.RoleUser, got.Role)
	assert.True(t, got.IsAuthenticated, "the login itself worked")
}

// Logging in again must not undo an approval, nor a disable.
func TestLoggingInAgainKeepsRoleAndStatus(t *testing.T) {
	ctx := context.Background()
	for _, status := range []string{models.StatusActive, models.StatusDisabled} {
		store := storage.NewMemoryStorage()
		u := storedUser(t, 7, "old-password")
		u.Status = status
		require.NoError(t, store.SaveUser(ctx, u))

		m := managerWith(t, store)
		require.NoError(t, m.LogInAndSave(ctx, 7, "athlete@example.com", "new-password"))

		got, _ := store.GetUser(ctx, 7)
		assert.Equal(t, status, got.Status)
		assert.Equal(t, models.RoleUser, got.Role)
	}
}

func TestTheConfiguredAdminIsActiveFromTheirFirstLogin(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	m := managerWith(t, store)
	m.SetAdmin(7)

	require.NoError(t, m.LogInAndSave(ctx, 7, "athlete@example.com", "new-password"))

	got, _ := store.GetUser(ctx, 7)
	assert.Equal(t, models.RoleAdmin, got.Role)
	assert.Equal(t, models.StatusActive, got.Status)
}

// Everyone was using the bot before roles existed, so they stay in; the
// admin from the config becomes the admin. Running it twice changes nothing.
func TestBootstrapMakesExistingUsersActive(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	for _, id := range []int64{1, 2} {
		u := storedUser(t, id, "pw")
		u.Role, u.Status = "", ""
		require.NoError(t, store.SaveUser(ctx, u))
	}
	pending := storedUser(t, 3, "pw")
	pending.Status = models.StatusPending
	require.NoError(t, store.SaveUser(ctx, pending))

	m := managerWith(t, store)
	m.SetAdmin(1)
	require.NoError(t, m.Bootstrap(ctx))
	require.NoError(t, m.Bootstrap(ctx))

	admin, _ := store.GetUser(ctx, 1)
	assert.Equal(t, models.RoleAdmin, admin.Role)
	assert.Equal(t, models.StatusActive, admin.Status)
	user, _ := store.GetUser(ctx, 2)
	assert.Equal(t, models.RoleUser, user.Role)
	assert.Equal(t, models.StatusActive, user.Status)
	stillPending, _ := store.GetUser(ctx, 3)
	assert.Equal(t, models.StatusPending, stillPending.Status, "a status already set is kept")
}

func TestSetUserStatus(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	pending := storedUser(t, 7, "pw")
	pending.Status = models.StatusPending
	require.NoError(t, store.SaveUser(ctx, pending))
	admin := storedUser(t, 1, "pw")
	admin.Role = models.RoleAdmin
	require.NoError(t, store.SaveUser(ctx, admin))
	m := managerWith(t, store)

	got, err := m.SetUserStatus(ctx, 7, models.StatusActive)
	require.NoError(t, err)
	assert.Equal(t, models.StatusActive, got.Status)
	stored, _ := store.GetUser(ctx, 7)
	assert.Equal(t, models.StatusActive, stored.Status)

	_, err = m.SetUserStatus(ctx, 1, models.StatusDisabled)
	assert.ErrorIs(t, err, ErrAdminStatus, "nobody can lock the admins out")

	_, err = m.SetUserStatus(ctx, 99, models.StatusActive)
	assert.ErrorIs(t, err, ErrUserNotFound)

	_, err = m.SetUserStatus(ctx, 7, "banned")
	assert.ErrorIs(t, err, ErrInvalidStatus)
}
