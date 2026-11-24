package usecase

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/booking"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

// preflightFixture is a shadow-mode scheduler with an admin (chat 1, no
// classes) and two athletes with classes: chat 7, whose stored session is two
// days old, and chat 8, who has none.
type preflightFixture struct {
	bs       *BookingScheduler
	booker   *sessionBooker
	messages func() map[int64][]string
	metrics  *Metrics
}

const (
	email7 = "seven@example.com"
	email8 = "eight@example.com"
)

func athleteWithClass(t *testing.T, chatID int64, email string) models.User {
	t.Helper()
	u := storedUser(t, chatID, "hunter2")
	u.Email = email
	u.ClassBookingSchedules = []models.ClassBookingSchedule{
		{ID: "c1", Day: "Monday", Hour: "07:00", ClassType: "Wod"},
	}
	return u
}

func newPreflightFixture(t *testing.T) *preflightFixture {
	t.Helper()
	booker := &sessionBooker{
		check: func(s wodbuster.Session) (wodbuster.Session, error) { return s, nil },
		login: func(email string) (wodbuster.Session, error) {
			return wbSession("fresh-"+email, "fresh", time.Now(), time.Now().Add(45*24*time.Hour)), nil
		},
	}
	bs, notifier := withAdmins(t, booker,
		adminUser(t, adminChat, "admin@example.com"),
		athleteWithClass(t, 7, email7),
		athleteWithClass(t, 8, email8))
	bs.SetSessionPreflight("0 11 * * 0", ReuseShadow)
	bs.shadowJitter = 0
	m := NewMetrics(prom.NewRegistry())
	bs.SetMetrics(m)

	old := wbSession("old-7", "old", time.Now().Add(-48*time.Hour), time.Now().Add(40*24*time.Hour))
	st, err := sealSession(testKey, 7, old, time.Now().Add(-8*time.Hour))
	require.NoError(t, err)
	require.NoError(t, bs.storage.SaveSession(context.Background(), st))

	return &preflightFixture{bs: bs, booker: booker, messages: collect(notifier), metrics: m}
}

func (f *preflightFixture) stored(t *testing.T, chatID int64) (wodbuster.Session, models.StoredSession) {
	t.Helper()
	st, ok, err := f.bs.storage.GetSession(context.Background(), chatID)
	require.NoError(t, err)
	require.True(t, ok, "chat %d has no stored session", chatID)
	sess, err := openSession(testKey, st)
	require.NoError(t, err)
	return sess, st
}

func (f *preflightFixture) reuse(result string) float64 {
	return testutil.ToFloat64(f.metrics.sessionReuse.WithLabelValues(string(ReuseShadow), result))
}

// preflightTime is 11:00 on the Sunday of the next opening: the preflight's
// own slot, whatever day the tests run on. time.Now() on a Sunday from 11:50
// is past the run's start, and every preflight would skip.
func preflightTime(bs *BookingScheduler) time.Time {
	return bs.opening.Next(time.Now()).Add(-time.Hour)
}

// From the run's start the run owns the logins: a preflight fired late (a
// suspended host resuming at 11:55) does nothing.
func TestPreflightSkipsWhenTheRunIsDue(t *testing.T) {
	f := newPreflightFixture(t)
	opensAt := f.bs.opening.Next(time.Now())
	runStart := opensAt.Add(-f.bs.opening.StartBefore)

	for _, at := range []time.Time{runStart, runStart.Add(5 * time.Minute), opensAt.Add(time.Minute)} {
		f.bs.preflight(at)
	}
	assert.Empty(t, f.booker.logins)
	assert.Empty(t, f.booker.checks)
	assert.Empty(t, f.messages())

	f.bs.preflight(runStart.Add(-time.Minute))
	assert.Len(t, f.booker.logins, 2, "a minute before the run is still the preflight's")
}

func TestParseReuseMode(t *testing.T) {
	for _, s := range []string{"off", "shadow"} {
		m, err := ParseReuseMode(s)
		require.NoError(t, err)
		assert.Equal(t, ReuseMode(s), m)
	}
	_, err := ParseReuseMode("live")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not built yet", "live must not quietly act like shadow")
	_, err = ParseReuseMode("on")
	assert.Error(t, err)
}

func TestPreflightLogsEveryoneInAndStoresTheSessions(t *testing.T) {
	f := newPreflightFixture(t)

	f.bs.preflight(preflightTime(f.bs))

	assert.ElementsMatch(t, []string{email7, email8}, f.booker.logins,
		"athletes with classes; the admin has none")
	for _, chat := range []int64{7, 8} {
		sess, st := f.stored(t, chat)
		assert.Equal(t, "fresh", sess.Cookies[1].Value, "chat %d", chat)
		assert.WithinDuration(t, time.Now(), st.LoggedInAt, time.Minute)
		assert.True(t, st.TrustedDevice)
	}

	assert.Equal(t, []string{"old-7"}, f.booker.checks, "only the stored session is shadow-checked")
	assert.Equal(t, 1.0, f.reuse(reuseWouldWork))
	assert.Equal(t, 1.0, f.reuse(reuseMissing))
	assert.Equal(t, 1.0, testutil.ToFloat64(f.metrics.preflights.WithLabelValues("all_ok")))

	msgs := f.messages()
	assert.Empty(t, msgs[7])
	assert.Empty(t, msgs[8])
	require.Len(t, msgs[adminChat], 1, "one summary")
	assert.Contains(t, msgs[adminChat][0], "all 2 athletes")
	assert.Contains(t, msgs[adminChat][0], "1 would have worked, 0 refused, 0 inconclusive, 1 missing")
}

func TestPreflightRecordsARefusedStoredSession(t *testing.T) {
	f := newPreflightFixture(t)
	f.booker.check = func(wodbuster.Session) (wodbuster.Session, error) {
		return wodbuster.Session{}, wodbuster.ErrSessionExpired
	}

	f.bs.preflight(preflightTime(f.bs))

	assert.Equal(t, 1.0, f.reuse(reuseWouldFail))
	assert.Equal(t, 1, testutil.CollectAndCount(f.metrics.sessionAgeAtFailure),
		"how old the refused session was is the shadow week's answer")
	sess, _ := f.stored(t, 7)
	assert.Equal(t, "fresh", sess.Cookies[1].Value, "the login replaced it anyway")
}

// One athlete's login failing must not cost the others their sessions, and
// the run must log in again only for that one.
func TestPreflightOneLoginFails(t *testing.T) {
	f := newPreflightFixture(t)
	f.booker.retries = map[string]int{email8: 2}
	f.booker.login = func(email string) (wodbuster.Session, error) {
		if email == email8 {
			return wodbuster.Session{}, errLoginStuck
		}
		return wbSession("fresh-"+email, "fresh", time.Now(), time.Now().Add(45*24*time.Hour)), nil
	}

	f.bs.preflight(preflightTime(f.bs))

	sess7, _ := f.stored(t, 7)
	assert.Equal(t, "fresh", sess7.Cookies[1].Value)
	_, ok, err := f.bs.storage.GetSession(context.Background(), 8)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, 1.0, testutil.ToFloat64(f.metrics.preflights.WithLabelValues("partial")))

	msgs := f.messages()
	require.Len(t, msgs[8], 2, "a warning on the first failed try, and the final word")
	assert.Contains(t, msgs[8][0], "try 1 of 3")
	assert.Contains(t, msgs[8][1], "will try again")
	assert.Contains(t, msgs[8][1], "book your classes by hand")
	assert.Empty(t, msgs[7])
	require.Len(t, msgs[adminChat], 2, "the first failure straight away, then the summary")
	assert.Contains(t, msgs[adminChat][0], "Sunday preflight: a login failed for chat 8")
	assert.Contains(t, msgs[adminChat][1], "1 of 2 athletes logged in")
	assert.Contains(t, msgs[adminChat][1], email8)

	// The run: chat 7 books with the preflight's session, chat 8 logs in.
	f.bs.runWeek(time.Now())
	require.Contains(t, f.booker.runs, email7)
	require.Contains(t, f.booker.runs, email8)
	require.NotNil(t, f.booker.runs[email7].Session)
	assert.Equal(t, "fresh", f.booker.runs[email7].Session.Cookies[1].Value)
	assert.Nil(t, f.booker.runs[email8].Session, "no session: the run logs in as before")
}

// Shadow means the days-old session is checked and never booked with, even
// when the check says it works.
func TestShadowNeverBooksWithTheOldSession(t *testing.T) {
	f := newPreflightFixture(t)
	f.booker.runAccepts = true

	// No preflight this week: the stored session is the two-day-old one.
	f.bs.runWeek(time.Now())

	require.Contains(t, f.booker.runs, email7)
	assert.Nil(t, f.booker.runs[email7].Session, "a days-old session is not used to book in shadow mode")
	sess, _ := f.stored(t, 7)
	assert.Equal(t, "run-login", sess.Cookies[1].Value, "the run's own session is kept")
	assert.Equal(t, 0.0, f.reuse(reuseUsed))
	assert.Equal(t, 0.0, f.reuse(reuseFallback), "nothing was offered, so nothing fell back")
}

func TestRunUsesThePreflightSessionAndCountsIt(t *testing.T) {
	f := newPreflightFixture(t)
	f.booker.runAccepts = true
	f.bs.preflight(preflightTime(f.bs))

	f.bs.runWeek(time.Now())
	assert.Equal(t, 2.0, f.reuse(reuseUsed))

	f2 := newPreflightFixture(t)
	f2.booker.runAccepts = false
	f2.bs.preflight(preflightTime(f2.bs))
	f2.bs.runWeek(time.Now())
	assert.Equal(t, 2.0, f2.reuse(reuseFallback))
	sess, _ := f2.stored(t, 7)
	assert.Equal(t, "run-login", sess.Cookies[1].Value, "the session the run ended with is stored")
}

// The run's cron fires at 11:50 whatever the preflight is doing. A preflight
// still logging in then is stopped, and the run waits for it to stop.
func TestThePreflightNeverOverlapsTheRun(t *testing.T) {
	f := newPreflightFixture(t)
	entered := make(chan struct{})
	var once sync.Once
	f.booker.login = nil
	blocking := &blockingLogin{entered: entered, once: &once, booker: f.booker}
	f.bs.booker = blocking

	done := make(chan struct{})
	go func() { defer close(done); f.bs.preflight(preflightTime(f.bs)) }()
	<-entered

	f.bs.runWeek(time.Now())
	<-done

	events := f.booker.events
	require.NotEmpty(t, events)
	// Every login the preflight started had returned before any run began.
	lastLogin, firstRun := -1, len(events)
	for i, e := range events {
		if strings.HasPrefix(e, "login returned") {
			lastLogin = i
		}
		if strings.HasPrefix(e, "run ") && i < firstRun {
			firstRun = i
		}
	}
	assert.Less(t, lastLogin, firstRun, "events: %v", events)
	for _, m := range f.messages()[7] {
		assert.NotContains(t, m, "Logging in to WodBuster failed",
			"a preflight cut short by the run tells the athlete nothing")
	}
}

// blockingLogin is a booker whose Login waits until its context ends, as a
// stuck browser login does.
type blockingLogin struct {
	entered chan struct{}
	once    *sync.Once
	booker  *sessionBooker
}

func (b *blockingLogin) Login(ctx context.Context, email, _ string,
	_ booking.LoginRetryFunc) (wodbuster.Session, error) {
	b.booker.event("login " + email)
	b.once.Do(func() { close(b.entered) })
	<-ctx.Done()
	b.booker.event("login returned " + email)
	return wodbuster.Session{}, ctx.Err()
}

func (b *blockingLogin) Authenticate(ctx context.Context, e, p string) (wodbuster.Session, error) {
	return b.booker.Authenticate(ctx, e, p)
}

func (b *blockingLogin) CheckSession(ctx context.Context, s wodbuster.Session) (wodbuster.Session, error) {
	return b.booker.CheckSession(ctx, s)
}

func (b *blockingLogin) Run(ctx context.Context, e, p string, t []booking.Target,
	o booking.RunOptions) ([]booking.Outcome, error) {
	return b.booker.Run(ctx, e, p, t, o)
}

func TestPreflightDoesNothingWhenReuseIsOff(t *testing.T) {
	f := newPreflightFixture(t)
	f.bs.SetSessionPreflight("0 11 * * 0", ReuseOff)

	f.bs.preflight(preflightTime(f.bs))
	assert.Empty(t, f.booker.logins)
	assert.Empty(t, f.booker.checks)

	f.bs.runWeek(time.Now())
	assert.Nil(t, f.booker.runs[email7].Session)
	assert.Nil(t, f.booker.runs[email7].OnSession, "off is the old behaviour: sessions are not touched")
}

func TestPreflightSkipsDuringTheRun(t *testing.T) {
	f := newPreflightFixture(t)
	f.bs.weekRunning.Store(true)
	f.bs.preflight(preflightTime(f.bs))
	assert.Empty(t, f.booker.logins)
}

func TestPreflightIsScheduledOnlyWhenReuseIsOn(t *testing.T) {
	on, _ := withAdmins(t, &sessionBooker{})
	on.SetSessionPreflight("0 11 * * 0", ReuseShadow)
	require.NoError(t, on.Start())
	defer on.Stop()
	assert.Len(t, on.cron.Entries(), 2, "the weekly run and the preflight")

	off, _ := withAdmins(t, &sessionBooker{})
	off.SetSessionPreflight("0 11 * * 0", ReuseOff)
	require.NoError(t, off.Start())
	defer off.Stop()
	assert.Len(t, off.cron.Entries(), 1, "only the weekly run")
}

// The shadow check deletes a session only when WodBuster refused it, or it
// cannot be read. A challenge or a network error says nothing about it.
func TestShadowCheckDeletesOnlyARefusedSession(t *testing.T) {
	for _, tc := range []struct {
		name    string
		check   error
		corrupt bool
		result  string
		kept    bool
	}{
		{"accepted", nil, false, reuseWouldWork, true},
		{"refused", wodbuster.ErrSessionExpired, false, reuseWouldFail, false},
		{"challenge", wodbuster.ErrChallenge, false, reuseInconclusive, true},
		{"network", errors.New("connection reset"), false, reuseInconclusive, true},
		{"unreadable", nil, true, reuseWouldFail, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPreflightFixture(t)
			f.booker.check = func(s wodbuster.Session) (wodbuster.Session, error) { return s, tc.check }
			if tc.corrupt {
				_, st := f.stored(t, 7)
				st.Encrypted = "not a sealed session"
				require.NoError(t, f.bs.storage.SaveSession(context.Background(), st))
			}
			u, _ := f.bs.storage.GetUser(context.Background(), 7)

			assert.Equal(t, tc.result, f.bs.shadowCheck(context.Background(), u))

			_, ok, err := f.bs.storage.GetSession(context.Background(), 7)
			require.NoError(t, err)
			assert.Equal(t, tc.kept, ok)
			assert.Equal(t, 1.0, f.reuse(tc.result))
		})
	}
}

// WodBuster refusing the preflight's session in the middle of the chase: the
// run hands nothing back, so nothing is stored over the session the refresh
// will find refused and replace, and the athlete hears why the classes failed.
func TestASessionRefusedMidRunIsNotStored(t *testing.T) {
	f := newPreflightFixture(t)
	f.bs.preflight(preflightTime(f.bs))
	_, before := f.stored(t, 7)
	f.booker.runExpires = true

	f.bs.runWeek(time.Now())

	require.NotNil(t, f.booker.runs[email7].Session, "the preflight's session was offered")
	_, after := f.stored(t, 7)
	assert.Equal(t, before.Encrypted, after.Encrypted)
	assert.Equal(t, 0.0, f.reuse(reuseUsed)+f.reuse(reuseFallback), "no session came back to count")
	results := f.messages()[7]
	require.NotEmpty(t, results)
	assert.Contains(t, results[len(results)-1], "session")
}
