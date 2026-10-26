package usecase

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/utils"
)

func managerWith(t *testing.T, store Storage) *Manager {
	t.Helper()
	booker := NewMockBooker(t)
	booker.EXPECT().Authenticate(context.Background(), "athlete@example.com", "new-password").Return(nil).Maybe()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bs := NewBookingScheduler(store, booker, DefaultOpening(madrid(t)), testKey, log)
	return NewManager(store, booker, testKey, bs, log)
}

// Logging in again used to rebuild the user from scratch and replace the whole
// document, so a password change cost the athlete every class they had set up.
func TestLoggingInAgainKeepsTheSchedule(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	existing := storedUser(t, 7, "old-password")
	existing.ClassBookingSchedules = []models.ClassBookingSchedule{monday7, friday7}
	require.NoError(t, store.SaveUser(ctx, existing))
	created := existing.CreatedAt

	m := managerWith(t, store)
	require.NoError(t, m.LogInAndSave(ctx, 7, "athlete@example.com", "new-password"))

	got, ok := store.GetUser(ctx, 7)
	require.True(t, ok)
	assert.Equal(t, []models.ClassBookingSchedule{monday7, friday7}, got.ClassBookingSchedules)
	assert.Equal(t, created, got.CreatedAt)
	assert.True(t, got.IsAuthenticated)
	pw, err := utils.DecryptPassword(got.Password, testKey)
	require.NoError(t, err)
	assert.Equal(t, "new-password", pw, "the new password replaces the old one")
}

func TestFirstLoginCreatesTheAthlete(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()

	m := managerWith(t, store)
	require.NoError(t, m.LogInAndSave(ctx, 7, "athlete@example.com", "new-password"))

	got, ok := store.GetUser(ctx, 7)
	require.True(t, ok)
	assert.Equal(t, "athlete@example.com", got.Email)
	assert.Empty(t, got.ClassBookingSchedules)
	assert.False(t, got.CreatedAt.IsZero())
}

func TestResetEmptiesTheScheduleAndKeepsTheLogin(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	existing := storedUser(t, 7, "pw")
	existing.ClassBookingSchedules = []models.ClassBookingSchedule{monday7, friday7}
	require.NoError(t, store.SaveUser(ctx, existing))

	m := managerWith(t, store)
	removed, err := m.ResetSchedule(ctx, 7)
	require.NoError(t, err)
	assert.Equal(t, []models.ClassBookingSchedule{monday7, friday7}, removed)

	got, ok := store.GetUser(ctx, 7)
	require.True(t, ok)
	assert.Empty(t, got.ClassBookingSchedules)
	assert.True(t, got.IsAuthenticated, "a reset is not a logout")
	assert.Equal(t, existing.Password, got.Password)

	require.NoError(t, m.ScheduleBookClass(ctx, 7, friday7))
	schedules, _ := store.GetClassBookingSchedules(ctx, 7)
	assert.Equal(t, []models.ClassBookingSchedule{friday7}, schedules, "the schedule starts again from scratch")
}

func TestResetForAnUnknownAthlete(t *testing.T) {
	m := managerWith(t, storage.NewMemoryStorage())
	_, err := m.ResetSchedule(context.Background(), 7)
	assert.ErrorIs(t, err, ErrUserNotFound)
}

// /book only adds to the schedule. Booking the same class twice used to queue
// two attempts and chase the class twice on Sunday.
func TestBookingTheSameClassTwiceKeepsOneEntry(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	require.NoError(t, store.SaveUser(ctx, storedUser(t, 7, "pw")))

	m := managerWith(t, store)
	require.NoError(t, m.ScheduleBookClass(ctx, 7, friday7))
	require.NoError(t, m.ScheduleBookClass(ctx, 7, friday7))

	got, _ := store.GetClassBookingSchedules(ctx, 7)
	assert.Equal(t, []models.ClassBookingSchedule{friday7}, got)
}
