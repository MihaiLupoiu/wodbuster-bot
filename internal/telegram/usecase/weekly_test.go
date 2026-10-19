package usecase

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/booking"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage"
)

// recordingBooker books whatever it is given and remembers who asked, so a
// test can see which athletes each week's run reached.
type recordingBooker struct {
	mu    sync.Mutex
	calls map[string]int // email -> runs
}

func (b *recordingBooker) Authenticate(context.Context, string, string) error { return nil }

func (b *recordingBooker) Run(_ context.Context, email, _ string, targets []booking.Target,
	_ booking.RunOptions) ([]booking.Outcome, error) {

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls == nil {
		b.calls = map[string]int{}
	}
	b.calls[email]++
	out := make([]booking.Outcome, len(targets))
	for i, t := range targets {
		out[i] = booking.Outcome{Class: t.Weekday.String() + " " + t.Start + " " + t.Class, Status: "booked"}
	}
	return out, nil
}

func (b *recordingBooker) athletes() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for e := range b.calls {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

func athlete(t *testing.T, chatID int64, email string, classes ...models.ClassBookingSchedule) models.User {
	t.Helper()
	u := storedUser(t, chatID, "hunter2")
	u.Email = email
	u.ClassBookingSchedules = classes
	return u
}

var monday7 = models.ClassBookingSchedule{ID: "Monday-07:00-Wod", Day: "Monday", Hour: "07:00", ClassType: "Wod"}
var friday7 = models.ClassBookingSchedule{ID: "Friday-07:00-Wod", Day: "Friday", Hour: "07:00", ClassType: "Wod"}

// 2026-10-18: five athletes, three in the run. The two missing had been tried
// the week before; their attempts ended "failed", nothing set them back to
// "pending", and the run only read pending attempts. A schedule must be booked
// every week, whatever happened the week before.
func TestEveryScheduleIsBookedEveryWeek(t *testing.T) {
	store := storage.NewMemoryStorage()
	ctx := context.Background()
	require.NoError(t, store.SaveUser(ctx, athlete(t, 1, "one@example.com", monday7, friday7)))
	require.NoError(t, store.SaveUser(ctx, athlete(t, 2, "two@example.com", monday7)))
	require.NoError(t, store.SaveUser(ctx, athlete(t, 3, "no-classes@example.com")))
	loggedOut := athlete(t, 4, "logged-out@example.com", monday7)
	loggedOut.IsAuthenticated = false
	require.NoError(t, store.SaveUser(ctx, loggedOut))

	booker := &recordingBooker{}
	notifier := NewMockNotifier(t)
	notifier.EXPECT().Notify(mock.Anything, mock.Anything).Return()
	bs := NewBookingScheduler(store, booker, DefaultOpening(madrid(t)), testKey,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	bs.SetNotifier(notifier)

	loc := madrid(t)
	week1 := time.Date(2026, time.October, 11, 11, 50, 0, 0, loc)
	week2 := week1.AddDate(0, 0, 7)

	bs.runWeek(week1)
	bs.runWeek(week2)

	assert.Equal(t, []string{"one@example.com", "two@example.com"}, booker.athletes(),
		"only authenticated athletes with classes are booked")
	assert.Equal(t, 2, booker.calls["one@example.com"], "booked in both weeks, not just the first")
	assert.Equal(t, 2, booker.calls["two@example.com"])

	// Each week leaves its own history row per class, recorded as a success.
	for _, id := range []string{
		"1-Monday-07:00-Wod-2026-10-11", "1-Friday-07:00-Wod-2026-10-11",
		"1-Monday-07:00-Wod-2026-10-18", "1-Friday-07:00-Wod-2026-10-18",
		"2-Monday-07:00-Wod-2026-10-11", "2-Monday-07:00-Wod-2026-10-18",
	} {
		require.NoError(t, store.UpdateBookingStatus(ctx, id, "success", ""),
			"attempt %s should exist", id)
	}
}

// A restart inside the window runs the week again. It must reuse the week's
// attempt rows, not add a second set.
func TestARerunInTheSameWeekReusesItsAttempts(t *testing.T) {
	store := NewMockStorage(t)
	bs := NewBookingScheduler(store, NewMockBooker(t), DefaultOpening(madrid(t)), testKey,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	opensAt := time.Date(2026, time.October, 18, 12, 0, 0, 0, madrid(t))
	var ids []string
	store.EXPECT().SaveBookingAttempt(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, a models.BookingAttempt) error {
			ids = append(ids, a.ID)
			assert.Equal(t, "pending", a.Status)
			assert.True(t, a.AttemptTime.Equal(opensAt))
			return nil
		})

	users := []models.User{athlete(t, 7, "a@example.com", monday7)}
	bs.weekAttempts(context.Background(), users, opensAt)
	bs.weekAttempts(context.Background(), users, opensAt)

	assert.Equal(t, []string{"7-Monday-07:00-Wod-2026-10-18", "7-Monday-07:00-Wod-2026-10-18"}, ids)
}
