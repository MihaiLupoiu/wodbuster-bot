package usecase

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

func managerLoggingIn(t *testing.T, store Storage, sess wodbuster.Session) *Manager {
	t.Helper()
	booker := NewMockBooker(t)
	booker.EXPECT().Authenticate(mock.Anything, "athlete@example.com", "pw").Return(sess, nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bs := NewBookingScheduler(store, booker, DefaultOpening(madrid(t)), testKey, log)
	return NewManager(store, booker, testKey, bs, log)
}

// /login is the first place a session can come from: storing it means the
// refresh has something to keep alive before the first Sunday.
func TestLoginStoresTheSession(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	exp := time.Now().Add(45 * 24 * time.Hour).UTC().Truncate(time.Second)
	m := managerLoggingIn(t, store, wbSession("a7", "from-login", time.Now(), exp))

	require.NoError(t, m.LogInAndSave(ctx, 7, "athlete@example.com", "pw"))

	st, ok, err := m.GetSession(ctx, 7)
	require.NoError(t, err)
	require.True(t, ok)
	assert.True(t, st.AuthExpiresAt.Equal(exp))
	assert.True(t, st.TrustedDevice)
	sess, err := openSession(testKey, st)
	require.NoError(t, err)
	assert.Equal(t, "from-login", sess.Cookies[1].Value)
}

func TestANewLoginReplacesTheOldSession(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	old, err := sealSession(testKey, 7, wbSession("a7", "old", time.Now().Add(-72*time.Hour), time.Time{}), time.Now())
	require.NoError(t, err)
	require.NoError(t, store.SaveSession(ctx, old))

	m := managerLoggingIn(t, store, wbSession("a7", "new", time.Now(), time.Now().Add(time.Hour)))
	require.NoError(t, m.LogInAndSave(ctx, 7, "athlete@example.com", "pw"))

	st, ok, err := m.GetSession(ctx, 7)
	require.NoError(t, err)
	require.True(t, ok)
	sess, err := openSession(testKey, st)
	require.NoError(t, err)
	assert.Equal(t, "new", sess.Cookies[1].Value)
}

// A login that hands back nothing usable still saves the athlete; there is
// just no session to keep.
func TestAnEmptySessionIsNotStored(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	m := managerLoggingIn(t, store, wodbuster.Session{})

	require.NoError(t, m.LogInAndSave(ctx, 7, "athlete@example.com", "pw"))

	_, ok := store.GetUser(ctx, 7)
	assert.True(t, ok)
	_, ok, err := m.GetSession(ctx, 7)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestDisablingAUserDeletesTheirSession(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	require.NoError(t, store.SaveUser(ctx, storedUser(t, 7, "pw")))
	st, err := sealSession(testKey, 7, wbSession("a7", "v", time.Now(), time.Time{}), time.Now())
	require.NoError(t, err)
	require.NoError(t, store.SaveSession(ctx, st))
	m := managerWith(t, store)

	// Approving keeps it; only disabling throws it away.
	_, err = m.SetUserStatus(ctx, 7, models.StatusPending)
	require.NoError(t, err)
	_, ok, _ := m.GetSession(ctx, 7)
	assert.True(t, ok)

	_, err = m.SetUserStatus(ctx, 7, models.StatusDisabled)
	require.NoError(t, err)
	_, ok, err = m.GetSession(ctx, 7)
	require.NoError(t, err)
	assert.False(t, ok, "a disabled account keeps no credential")
}
