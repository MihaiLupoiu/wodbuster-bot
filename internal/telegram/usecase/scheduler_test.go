package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/booking"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/utils"
)

const testKey = "a-32-character-secret-key-123456"

func madrid(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Madrid")
	require.NoError(t, err)
	return loc
}

// The bot used to fire on Saturday, a day on which nothing publishes. Two live
// runs settled it: Sunday 12:00, and the run starts ten minutes earlier.
func TestOpeningCronSpec(t *testing.T) {
	o := DefaultOpening(madrid(t))
	assert.Equal(t, time.Sunday, o.Weekday)
	assert.Equal(t, "50 11 * * 0", o.cronSpec())
}

func TestOpeningNextIsInTheGymsZone(t *testing.T) {
	loc := madrid(t)
	o := DefaultOpening(loc)

	// A Saturday evening in another zone still resolves to Sunday noon Madrid.
	now := time.Date(2026, time.October, 3, 20, 0, 0, 0, time.UTC)
	next := o.Next(now)
	assert.Equal(t, time.Sunday, next.In(loc).Weekday())
	assert.Equal(t, 12, next.In(loc).Hour())
	assert.Equal(t, 0, next.In(loc).Minute())
}

func schedulerFor(t *testing.T, booker Booker) (*BookingScheduler, *MockStorage, *MockNotifier) {
	t.Helper()
	store := NewMockStorage(t)
	notifier := NewMockNotifier(t)
	bs := NewBookingScheduler(store, booker, DefaultOpening(madrid(t)), testKey,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	bs.SetNotifier(notifier)
	return bs, store, notifier
}

func storedUser(t *testing.T, chatID int64, password string) models.User {
	t.Helper()
	enc, err := utils.EncryptPassword(password, testKey)
	require.NoError(t, err)
	return models.User{ChatID: chatID, Email: "athlete@example.com", Password: enc, IsAuthenticated: true}
}

// The whole point of the rework: one login per athlete, every class of theirs
// in a single run, with the day names turned into weekdays the library groks.
func TestRunForAthletePassesEveryClassInOneRun(t *testing.T) {
	booker := NewMockBooker(t)
	bs, store, notifier := schedulerFor(t, booker)

	attempts := []models.BookingAttempt{
		{ID: "a1", ChatID: 7, Day: "Monday", Hour: "07:00", ClassType: "Wod"},
		{ID: "a2", ChatID: 7, Day: "wednesday", Hour: "07:00", ClassType: "Wod"},
	}
	store.EXPECT().GetUser(mock.Anything, int64(7)).Return(storedUser(t, 7, "hunter2"), true)
	store.EXPECT().UpdateBookingStatus(mock.Anything, mock.Anything, "active", "").Return(nil).Twice()
	store.EXPECT().UpdateBookingStatus(mock.Anything, "a1", "success", "").Return(nil)
	store.EXPECT().UpdateBookingStatus(mock.Anything, "a2", "success", "").Return(nil)
	notifier.EXPECT().Notify(int64(7), mock.Anything).Return()

	booker.EXPECT().
		Run(mock.Anything, "athlete@example.com", "hunter2", mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _, _ string, targets []booking.Target,
			o booking.RunOptions) ([]booking.Outcome, error) {

			require.Len(t, targets, 2)
			assert.Equal(t, time.Monday, targets[0].Weekday)
			assert.Equal(t, time.Wednesday, targets[1].Weekday, "day names are case-insensitive")
			assert.True(t, targets[0].Waitlist, "the bot always takes a waiting-list place")
			assert.False(t, o.DryRun)
			return []booking.Outcome{
				{Class: "Monday 07:00 Wod", Status: "booked"},
				{Class: "Wednesday 07:00 Wod", Status: "waitlisted"},
			}, nil
		})

	out := bs.runForAthlete(context.Background(), 7, attempts,
		RunSettings{OpensAt: time.Now().Add(time.Hour)})
	require.Len(t, out, 2)
}

// A failed run must say so to the athlete and leave a reason in storage, not
// disappear into the logs at noon on a Sunday.
func TestRunForAthleteReportsFailure(t *testing.T) {
	booker := NewMockBooker(t)
	bs, store, notifier := schedulerFor(t, booker)

	store.EXPECT().GetUser(mock.Anything, int64(7)).Return(storedUser(t, 7, "hunter2"), true)
	store.EXPECT().UpdateBookingStatus(mock.Anything, "a1", "active", "").Return(nil)
	store.EXPECT().UpdateBookingStatus(mock.Anything, "a1", "failed", mock.Anything).Return(nil)
	notifier.EXPECT().Notify(int64(7), mock.MatchedBy(func(s string) bool {
		return len(s) > 0 && s[0] == 0xE2 // starts with the ❌ rune
	})).Return()

	booker.EXPECT().Run(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("login failed"))

	out := bs.runForAthlete(context.Background(), 7,
		[]models.BookingAttempt{{ID: "a1", ChatID: 7, Day: "Monday", Hour: "07:00", ClassType: "Wod"}},
		RunSettings{})
	assert.Nil(t, out)
}

// A rehearsal books nothing and does not wait for the opening.
func TestRehearseIsADryRunThatActsNow(t *testing.T) {
	booker := NewMockBooker(t)
	bs, store, _ := schedulerFor(t, booker)

	store.EXPECT().GetClassBookingSchedules(mock.Anything, int64(7)).
		Return([]models.ClassBookingSchedule{{Day: "Friday", Hour: "19:00", ClassType: "Open box"}}, true)
	store.EXPECT().GetUser(mock.Anything, int64(7)).Return(storedUser(t, 7, "hunter2"), true)

	booker.EXPECT().Run(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _, _ string, targets []booking.Target,
			o booking.RunOptions) ([]booking.Outcome, error) {

			assert.True(t, o.DryRun, "a rehearsal must not book")
			assert.True(t, o.OpensAt.IsZero(), "a rehearsal must not wait for Sunday")
			return []booking.Outcome{{Class: "Friday 19:00 Open box", Status: "dry-run"}}, nil
		})

	out, err := bs.Rehearse(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "dry-run", out[0].Status)
}

func TestRehearseWithoutClasses(t *testing.T) {
	bs, store, _ := schedulerFor(t, NewMockBooker(t))
	store.EXPECT().GetClassBookingSchedules(mock.Anything, int64(7)).Return(nil, false)

	_, err := bs.Rehearse(context.Background(), 7)
	assert.Error(t, err)
}
