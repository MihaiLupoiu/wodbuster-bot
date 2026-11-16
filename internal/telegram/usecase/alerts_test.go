package usecase

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
)

const adminChat = int64(1)

var errLoginStuck = errors.New("browserauth: login did not finish in time (stuck: login_page)")

// The daily check is what would have caught 2026-11-15 two days early: it
// warns the admin every day the login fails, and once when it works again.
func TestLoginCheckWarnsTheAdminUntilItWorksAgain(t *testing.T) {
	booker := NewMockBooker(t)
	bs, store, notifier := schedulerFor(t, booker)
	bs.SetAdmin(adminChat)

	store.EXPECT().GetUser(mock.Anything, adminChat).Return(storedUser(t, adminChat, "hunter2"), true)
	gets := booker.EXPECT().Authenticate(mock.Anything, "athlete@example.com", "hunter2")

	var sent []string
	notifier.EXPECT().Notify(adminChat, mock.Anything).Run(func(_ int64, text string) {
		sent = append(sent, text)
	}).Return()

	gets.Return(errLoginStuck).Times(2)
	bs.CheckLogin()
	bs.CheckLogin()
	require.Len(t, sent, 2, "every failing day is reported")
	assert.Contains(t, sent[0], "Daily login check failed")
	assert.Contains(t, sent[0], "login_page")

	booker.ExpectedCalls = nil
	booker.EXPECT().Authenticate(mock.Anything, "athlete@example.com", "hunter2").Return(nil).Times(2)
	bs.CheckLogin()
	bs.CheckLogin()
	require.Len(t, sent, 3, "recovery is reported once, and a passing day after it says nothing")
	assert.Contains(t, sent[2], "works again")
}

func TestLoginCheckAsksTheAdminToLogInWhenItHasNoCredentials(t *testing.T) {
	booker := NewMockBooker(t)
	bs, store, notifier := schedulerFor(t, booker)
	bs.SetAdmin(adminChat)

	store.EXPECT().GetUser(mock.Anything, adminChat).Return(models.User{}, false)
	notifier.EXPECT().Notify(adminChat, mock.MatchedBy(func(text string) bool {
		return strings.Contains(text, "/login")
	})).Return()

	bs.CheckLogin()
}

// A run in progress owns the logins; the check must not queue behind it or
// add a login to the minutes before the opening.
func TestLoginCheckStaysOutOfABookingRun(t *testing.T) {
	booker := NewMockBooker(t)
	bs, _, _ := schedulerFor(t, booker)
	bs.SetAdmin(adminChat)
	bs.track(7, models.BookingAttempt{Day: "Monday"}, func() {})

	bs.CheckLogin() // no storage, booker or notifier call expected
}

func TestNoAdminMeansNoLoginCheck(t *testing.T) {
	bs, _, _ := schedulerFor(t, NewMockBooker(t))
	bs.SetLoginCheck("0 10 * * *")
	require.NoError(t, bs.Start())
	defer bs.Stop()
	assert.Len(t, bs.cron.Entries(), 1, "only the weekly run")
}

// With two cron entries, "next run" must still be the Sunday one, not
// whichever entry happens to come first.
func TestNextRunIsTheWeeklyRunNotTheLoginCheck(t *testing.T) {
	bs, _, _ := schedulerFor(t, NewMockBooker(t))
	bs.SetAdmin(adminChat)
	bs.SetLoginCheck("* * * * *")
	require.NoError(t, bs.Start())
	defer bs.Stop()

	require.Len(t, bs.cron.Entries(), 2)
	next := bs.GetNextRunTime().In(madrid(t))
	assert.Equal(t, time.Sunday, next.Weekday())
	assert.Equal(t, "11:50", next.Format("15:04"))
}

// 2026-11-15: athletes heard nothing until their last try failed, after the
// opening. Now each hears on the first failure, and the admin hears once for
// the whole run.
func TestLoginTroubleWarnsEachAthleteOnceAndTheAdminOncePerRun(t *testing.T) {
	bs, _, notifier := schedulerFor(t, NewMockBooker(t))
	bs.SetAdmin(adminChat)
	opensAt := time.Date(2026, time.November, 15, 12, 0, 0, 0, madrid(t))

	var mu sync.Mutex
	got := map[int64][]string{}
	notifier.EXPECT().Notify(mock.Anything, mock.Anything).Run(func(chatID int64, text string) {
		mu.Lock()
		defer mu.Unlock()
		got[chatID] = append(got[chatID], text)
	}).Return()

	var once sync.Once
	for _, chatID := range []int64{7, 8} {
		bs.loginTrouble(chatID, opensAt, 1, 3, errLoginStuck, &once)
		bs.loginTrouble(chatID, opensAt, 2, 3, errLoginStuck, &once)
	}

	require.Len(t, got[7], 1, "the athlete is told once, on the first failure")
	assert.Contains(t, got[7][0], "try 1 of 3")
	assert.Contains(t, got[7][0], "by 12:00", "in the gym's time")
	assert.Contains(t, got[7][0], "book your classes by hand")
	require.Len(t, got[8], 1)
	require.Len(t, got[adminChat], 1, "the admin is told once per run")
	assert.Contains(t, got[adminChat][0], "chat 7")
}

// The admin's own run already tells the admin, as an athlete; a second copy
// says nothing new.
func TestTheAdminIsNotToldTwiceAboutTheirOwnRun(t *testing.T) {
	bs, _, notifier := schedulerFor(t, NewMockBooker(t))
	bs.SetAdmin(adminChat)
	notifier.EXPECT().Notify(adminChat, mock.Anything).Return().Once()

	var once sync.Once
	bs.loginTrouble(adminChat, time.Now(), 1, 3, errLoginStuck, &once)
}

func TestAFailedRunIsReportedToTheAdminToo(t *testing.T) {
	bs, store, notifier := schedulerFor(t, NewMockBooker(t))
	bs.SetAdmin(adminChat)

	store.EXPECT().UpdateBookingStatus(mock.Anything, "a1", "failed", mock.Anything).Return(nil)
	notifier.EXPECT().Notify(int64(7), mock.MatchedBy(func(text string) bool {
		return strings.Contains(text, "Book your classes by hand")
	})).Return()
	notifier.EXPECT().Notify(adminChat, mock.MatchedBy(func(text string) bool {
		return strings.Contains(text, "chat 7") && strings.Contains(text, "login_page")
	})).Return()

	bs.fail(t.Context(), 7, []models.BookingAttempt{{ID: "a1"}}, errLoginStuck)
}
