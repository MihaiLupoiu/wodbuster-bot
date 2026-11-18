package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

const adminChat = int64(1)

var errLoginStuck = errors.New("browserauth: login did not finish in time (stuck: login_page)")

// withAdmins is a scheduler over a real in-memory store holding these users,
// so that finding the admins goes through the same ListUsers the bot uses.
func withAdmins(t *testing.T, booker Booker, users ...models.User) (*BookingScheduler, *MockNotifier) {
	t.Helper()
	store := storage.NewMemoryStorage()
	for _, u := range users {
		require.NoError(t, store.SaveUser(context.Background(), u))
	}
	notifier := NewMockNotifier(t)
	bs := NewBookingScheduler(store, booker, DefaultOpening(madrid(t)), testKey,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	bs.SetNotifier(notifier)
	return bs, notifier
}

func adminUser(t *testing.T, chatID int64, email string) models.User {
	t.Helper()
	u := storedUser(t, chatID, "hunter2")
	u.Email, u.Role = email, models.RoleAdmin
	return u
}

// collect records every message, by chat.
func collect(notifier *MockNotifier) func() map[int64][]string {
	var mu sync.Mutex
	got := map[int64][]string{}
	notifier.EXPECT().Notify(mock.Anything, mock.Anything).Run(func(chatID int64, text string) {
		mu.Lock()
		defer mu.Unlock()
		got[chatID] = append(got[chatID], text)
	}).Return().Maybe()
	return func() map[int64][]string {
		mu.Lock()
		defer mu.Unlock()
		return got
	}
}

// The daily check is what would have caught 2026-11-15 two days early. It logs
// in as the first admin and tells every admin, every day it fails, and once
// when it works again.
func TestLoginCheckWarnsTheAdminsUntilItWorksAgain(t *testing.T) {
	booker := NewMockBooker(t)
	bs, notifier := withAdmins(t, booker,
		adminUser(t, 2, "second@example.com"),
		adminUser(t, adminChat, "first@example.com"),
		storedUser(t, 7, "pw"))
	sent := collect(notifier)

	booker.EXPECT().Authenticate(mock.Anything, "first@example.com", "hunter2").
		Return(wodbuster.Session{}, errLoginStuck).Times(2)
	bs.CheckLogin()
	bs.CheckLogin()
	require.Len(t, sent()[adminChat], 2, "every failing day is reported")
	require.Len(t, sent()[2], 2, "to every admin")
	assert.Empty(t, sent()[7], "not to users")
	assert.Contains(t, sent()[adminChat][0], "Daily login check failed for first@example.com")
	assert.Contains(t, sent()[adminChat][0], "login_page")

	booker.ExpectedCalls = nil
	booker.EXPECT().Authenticate(mock.Anything, "first@example.com", "hunter2").Return(wodbuster.Session{}, nil).Times(2)
	bs.CheckLogin()
	bs.CheckLogin()
	require.Len(t, sent()[adminChat], 3, "recovery is reported once, and a passing day after it says nothing")
	assert.Contains(t, sent()[adminChat][2], "works again")
}

// No admin: no account to check with, and nobody to tell.
func TestLoginCheckDoesNothingWithoutAnAdmin(t *testing.T) {
	disabledAdmin := adminUser(t, adminChat, "off@example.com")
	disabledAdmin.Status = models.StatusDisabled
	bs, _ := withAdmins(t, NewMockBooker(t), storedUser(t, 7, "pw"), disabledAdmin)

	bs.CheckLogin() // no booker or notifier call expected
}

// A run in progress owns the logins; the check must not queue behind it or
// add a login to the minutes before the opening.
func TestLoginCheckStaysOutOfABookingRun(t *testing.T) {
	bs, _ := withAdmins(t, NewMockBooker(t), adminUser(t, adminChat, "a@example.com"))
	bs.track(7, models.BookingAttempt{Day: "Monday"}, func() {})

	bs.CheckLogin() // no booker or notifier call expected
}

func TestNoCronSpecMeansNoLoginCheck(t *testing.T) {
	bs, _, _ := schedulerFor(t, NewMockBooker(t))
	require.NoError(t, bs.Start())
	defer bs.Stop()
	assert.Len(t, bs.cron.Entries(), 1, "only the weekly run")
}

// With two cron entries, "next run" must still be the Sunday one, not
// whichever entry happens to come first.
func TestNextRunIsTheWeeklyRunNotTheLoginCheck(t *testing.T) {
	bs, _, _ := schedulerFor(t, NewMockBooker(t))
	bs.SetLoginCheck("* * * * *")
	require.NoError(t, bs.Start())
	defer bs.Stop()

	require.Len(t, bs.cron.Entries(), 2)
	next := bs.GetNextRunTime().In(madrid(t))
	assert.Equal(t, time.Sunday, next.Weekday())
	assert.Equal(t, "11:50", next.Format("15:04"))
}

// 2026-11-15: athletes heard nothing until their last try failed, after the
// opening. Now each hears on the first failure, and the admins hear once for
// the whole run.
func TestLoginTroubleWarnsEachAthleteOnceAndTheAdminsOncePerRun(t *testing.T) {
	bs, notifier := withAdmins(t, NewMockBooker(t), adminUser(t, adminChat, "a@example.com"))
	sent := collect(notifier)
	opensAt := time.Date(2026, time.November, 15, 12, 0, 0, 0, madrid(t))

	var once sync.Once
	for _, chatID := range []int64{7, 8} {
		bs.loginTrouble(chatID, opensAt, 1, 3, errLoginStuck, &once)
		bs.loginTrouble(chatID, opensAt, 2, 3, errLoginStuck, &once)
	}

	got := sent()
	require.Len(t, got[7], 1, "the athlete is told once, on the first failure")
	assert.Contains(t, got[7][0], "try 1 of 3")
	assert.Contains(t, got[7][0], "by 12:00", "in the gym's time")
	assert.Contains(t, got[7][0], "book your classes by hand")
	require.Len(t, got[8], 1)
	require.Len(t, got[adminChat], 1, "the admin is told once per run")
	assert.Contains(t, got[adminChat][0], "chat 7")
}

// An admin's own run already tells them, as an athlete; a second copy says
// nothing new. Other admins still hear about it.
func TestAnAdminIsNotToldTwiceAboutTheirOwnRun(t *testing.T) {
	bs, notifier := withAdmins(t, NewMockBooker(t),
		adminUser(t, adminChat, "a@example.com"), adminUser(t, 2, "b@example.com"))
	sent := collect(notifier)

	var once sync.Once
	bs.loginTrouble(adminChat, time.Now(), 1, 3, errLoginStuck, &once)

	assert.Len(t, sent()[adminChat], 1, "only the athlete's warning")
	assert.Len(t, sent()[2], 1, "the other admin is told")
}

func TestAFailedRunIsReportedToTheAdminsToo(t *testing.T) {
	bs, notifier := withAdmins(t, NewMockBooker(t), adminUser(t, adminChat, "a@example.com"))
	sent := collect(notifier)

	bs.fail(t.Context(), 7, []models.BookingAttempt{{ChatID: 7}}, errLoginStuck)

	require.Len(t, sent()[7], 1)
	assert.Contains(t, sent()[7][0], "Book your classes by hand")
	require.Len(t, sent()[adminChat], 1)
	assert.True(t, strings.Contains(sent()[adminChat][0], "chat 7") &&
		strings.Contains(sent()[adminChat][0], "login_page"))
}

func TestNoAdminMeansFailuresReachOnlyTheAthlete(t *testing.T) {
	bs, notifier := withAdmins(t, NewMockBooker(t), storedUser(t, 7, "pw"))
	sent := collect(notifier)

	bs.fail(t.Context(), 7, []models.BookingAttempt{{ChatID: 7}}, errLoginStuck)

	assert.Equal(t, []int64{7}, keys(sent()))
}

func keys(m map[int64][]string) []int64 {
	var out []int64
	for k := range m {
		out = append(out, k)
	}
	return out
}
