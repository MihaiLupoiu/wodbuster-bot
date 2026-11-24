package usecase

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

// sessionBooker answers CheckSession and Login from scripts, and records who
// was checked and who logged in.
type sessionBooker struct {
	mu     sync.Mutex
	check  func(wodbuster.Session) (wodbuster.Session, error)
	login  func(email string) (wodbuster.Session, error)
	checks []string // athlete ids
	logins []string // emails
	// authenticates counts the single-attempt logins among logins.
	authenticates int

	// retries[email] failed attempts are reported through onRetry before
	// login is called, the way booking.Service.Login reports them.
	retries map[string]int
	// runs records what each Run was given; runAccepts decides whether a
	// stored session passed to Run works (true) or the run falls back.
	runs       map[string]booking.RunOptions
	runAccepts bool
	// runExpires makes WodBuster refuse the session in the middle of the
	// chase: booking.Service then hands no session back.
	runExpires bool
	events     []string
}

func (b *sessionBooker) event(e string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
}

// Authenticate is one login attempt; the refresh logs in with it.
func (b *sessionBooker) Authenticate(_ context.Context, email, _ string) (wodbuster.Session, error) {
	b.mu.Lock()
	b.logins = append(b.logins, email)
	b.authenticates++
	b.mu.Unlock()
	if b.login == nil {
		return wodbuster.Session{}, errors.New("no login scripted")
	}
	return b.login(email)
}

func (b *sessionBooker) Login(_ context.Context, email, _ string,
	onRetry booking.LoginRetryFunc) (wodbuster.Session, error) {
	b.mu.Lock()
	b.logins = append(b.logins, email)
	n := b.retries[email]
	b.mu.Unlock()
	for i := 1; i <= n && onRetry != nil; i++ {
		onRetry(i, 3, errLoginStuck)
	}
	if b.login == nil {
		return wodbuster.Session{}, errors.New("no login scripted")
	}
	return b.login(email)
}

func (b *sessionBooker) CheckSession(_ context.Context, s wodbuster.Session) (wodbuster.Session, error) {
	b.mu.Lock()
	b.checks = append(b.checks, s.AthleteID)
	b.mu.Unlock()
	if b.check == nil {
		return s, nil // accepted, unchanged
	}
	return b.check(s)
}

func (b *sessionBooker) Run(_ context.Context, email, _ string, targets []booking.Target,
	o booking.RunOptions) ([]booking.Outcome, error) {
	b.event("run " + email)
	b.mu.Lock()
	if b.runs == nil {
		b.runs = map[string]booking.RunOptions{}
	}
	b.runs[email] = o
	accepts, expires := b.runAccepts, b.runExpires
	b.mu.Unlock()

	if expires {
		out := make([]booking.Outcome, len(targets))
		for i := range targets {
			out[i] = booking.Outcome{Class: "Monday 07:00 Wod", Status: "failed",
				Err: fmt.Errorf("booking: %w", wodbuster.ErrSessionExpired)}
		}
		return out, nil
	}
	if o.OnSession != nil {
		if o.Session != nil && accepts {
			o.OnSession(*o.Session, true)
		} else {
			o.OnSession(wbSession("run-"+email, "run-login", time.Now(), time.Now().Add(45*24*time.Hour)), false)
		}
	}
	out := make([]booking.Outcome, len(targets))
	for i := range targets {
		out[i] = booking.Outcome{Class: "Monday 07:00 Wod", Status: "booked"}
	}
	return out, nil
}

func wbSession(athlete, auth string, issued, expires time.Time) wodbuster.Session {
	return wodbuster.Session{
		Box:       "firespain",
		AthleteID: athlete,
		Cookies: []*http.Cookie{
			{Name: "WBSure_1", Value: "x", Path: "/"},
			{Name: wodbuster.AuthCookieName, Value: auth, Path: "/", Expires: expires},
		},
		IssuedAt: issued,
	}
}

// refreshFixture is a scheduler over an in-memory store with an admin (chat 1)
// and an athlete (chat 7) who has a stored session two days old.
type refreshFixture struct {
	bs       *BookingScheduler
	booker   *sessionBooker
	messages func() map[int64][]string
	metrics  *Metrics
	stored   models.StoredSession
	sess     wodbuster.Session
}

func newRefreshFixture(t *testing.T) *refreshFixture {
	t.Helper()
	booker := &sessionBooker{}
	athlete := storedUser(t, 7, "hunter2")
	athlete.Email = "athlete@example.com"
	bs, notifier := withAdmins(t, booker, adminUser(t, adminChat, "admin@example.com"), athlete)
	bs.refreshGap, bs.refreshGapJitter, bs.refreshStartJitter = 0, 0, 0
	// The refresh only runs where sessions are kept.
	bs.SetSessionPreflight("", ReuseShadow)
	m := NewMetrics(prom.NewRegistry())
	bs.SetMetrics(m)

	issued := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	sess := wbSession("athlete-7", "old", issued, time.Now().Add(40*24*time.Hour).UTC().Truncate(time.Second))
	st, err := sealSession(testKey, 7, sess, time.Now().Add(-12*time.Hour))
	require.NoError(t, err)
	require.NoError(t, bs.storage.SaveSession(context.Background(), st))
	st, _, _ = bs.storage.GetSession(context.Background(), 7)

	return &refreshFixture{bs: bs, booker: booker, messages: collect(notifier), metrics: m,
		stored: st, sess: sess}
}

func (f *refreshFixture) get(t *testing.T) (models.StoredSession, bool) {
	t.Helper()
	st, ok, err := f.bs.storage.GetSession(context.Background(), 7)
	require.NoError(t, err)
	return st, ok
}

func (f *refreshFixture) checks(result string) float64 {
	return testutil.ToFloat64(f.metrics.sessionChecks.WithLabelValues(sourceRefresh, result))
}

func TestSealAndOpenSession(t *testing.T) {
	exp := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	issued := time.Date(2026, 11, 16, 15, 5, 0, 0, time.UTC)
	used := time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC)
	sess := wbSession("a", "secret-cookie", issued, exp)

	st, err := sealSession(testKey, 7, sess, used)
	require.NoError(t, err)
	assert.NotContains(t, st.Encrypted, "secret-cookie", "never stored in plain text")
	assert.Equal(t, int64(7), st.ChatID)
	assert.True(t, st.LoggedInAt.Equal(issued))
	assert.True(t, st.LastUsedAt.Equal(used))
	assert.True(t, st.AuthExpiresAt.Equal(exp))
	assert.True(t, st.TrustedDevice, "an expiry means remember-this-device")

	back, err := openSession(testKey, st)
	require.NoError(t, err)
	assert.Equal(t, "a", back.AthleteID)
	assert.True(t, back.AuthExpiry().Equal(exp))

	_, err = openSession("another-32-character-secret-key!", st)
	assert.Error(t, err, "the wrong key cannot open it")

	once := wbSession("a", "v", issued, time.Time{})
	st, err = sealSession(testKey, 7, once, used)
	require.NoError(t, err)
	assert.False(t, st.TrustedDevice, "a session cookie means just-once")
	assert.True(t, st.AuthExpiresAt.IsZero())
}

func TestRefreshStoresTheRenewedSession(t *testing.T) {
	f := newRefreshFixture(t)
	renewedUntil := time.Now().Add(45 * 24 * time.Hour).UTC().Truncate(time.Second)
	f.booker.check = func(s wodbuster.Session) (wodbuster.Session, error) {
		s.Cookies[1] = &http.Cookie{Name: wodbuster.AuthCookieName, Value: "renewed", Path: "/", Expires: renewedUntil}
		return s, nil
	}

	f.bs.RefreshSessions()

	st, ok := f.get(t)
	require.True(t, ok)
	assert.True(t, st.AuthExpiresAt.Equal(renewedUntil))
	assert.WithinDuration(t, time.Now(), st.LastUsedAt, time.Minute)
	assert.True(t, st.LoggedInAt.Equal(f.stored.LoggedInAt), "the login time does not move")
	sess, err := openSession(testKey, st)
	require.NoError(t, err)
	assert.Equal(t, "renewed", sess.Cookies[1].Value)

	assert.Empty(t, f.booker.logins, "a working session needs no browser")
	assert.Empty(t, f.messages(), "a working refresh is quiet")
	assert.Equal(t, 1.0, f.checks(checkOK))
	assert.Equal(t, 1.0, testutil.ToFloat64(f.metrics.sessionsStored))
	assert.InDelta(t, float64(f.stored.LoggedInAt.Unix()), testutil.ToFloat64(f.metrics.sessionOldestLogin), 1)
	assert.InDelta(t, float64(renewedUntil.Unix()),
		testutil.ToFloat64(f.metrics.sessionAuthExpiry.WithLabelValues("7")), 1)
	assert.InDelta(t, float64(time.Now().Unix()), testutil.ToFloat64(f.metrics.refreshLastSuccess), 5,
		"a refresh that went through everyone is recorded")
}

func TestRefusedSessionIsReplacedByALogin(t *testing.T) {
	f := newRefreshFixture(t)
	f.booker.check = func(wodbuster.Session) (wodbuster.Session, error) {
		return wodbuster.Session{}, wodbuster.ErrSessionExpired
	}
	fresh := wbSession("athlete-7", "fresh", time.Now(), time.Now().Add(45*24*time.Hour))
	f.booker.login = func(string) (wodbuster.Session, error) { return fresh, nil }

	f.bs.RefreshSessions()

	assert.Equal(t, []string{"athlete@example.com"}, f.booker.logins)
	st, ok := f.get(t)
	require.True(t, ok)
	sess, err := openSession(testKey, st)
	require.NoError(t, err)
	assert.Equal(t, "fresh", sess.Cookies[1].Value)
	assert.Empty(t, f.messages(), "a lost session that a login replaces is quiet")
	assert.Equal(t, 1.0, f.checks(checkExpired))
	assert.Equal(t, 1, testutil.CollectAndCount(f.metrics.sessionAgeAtFailure),
		"the age it died at is recorded")
	assert.Equal(t, 1, testutil.CollectAndCount(f.metrics.sessionIdleAtFailure),
		"and the idle gap it did not survive")
	assert.Equal(t, 1.0, testutil.ToFloat64(f.metrics.sessionsStored))
}

func TestRefusedSessionAndFailedLoginTellTheAdmins(t *testing.T) {
	f := newRefreshFixture(t)
	f.booker.check = func(wodbuster.Session) (wodbuster.Session, error) {
		return wodbuster.Session{}, wodbuster.ErrSessionExpired
	}
	f.booker.login = func(string) (wodbuster.Session, error) { return wodbuster.Session{}, errLoginStuck }

	f.bs.RefreshSessions()

	_, ok := f.get(t)
	assert.False(t, ok, "a refused session is deleted")
	msgs := f.messages()
	require.Len(t, msgs[adminChat], 1)
	assert.Contains(t, msgs[adminChat][0], "athlete@example.com")
	assert.Contains(t, msgs[adminChat][0], "stuck: login_page", "the reason is in the message")
	assert.Empty(t, msgs[7], "the athlete is not woken at 03:00; Sunday's run tries again")
	assert.Equal(t, 0.0, testutil.ToFloat64(f.metrics.sessionsStored))
	assert.Equal(t, 0, testutil.CollectAndCount(f.metrics.sessionAuthExpiry), "no expiry for a deleted session")
}

// A challenge says nothing about the session. Throwing it away, or logging in
// through the same challenge, would only make things worse.
func TestChallengeKeepsTheSession(t *testing.T) {
	f := newRefreshFixture(t)
	f.booker.check = func(wodbuster.Session) (wodbuster.Session, error) {
		return wodbuster.Session{}, wodbuster.ErrChallenge
	}

	f.bs.RefreshSessions()

	st, ok := f.get(t)
	require.True(t, ok)
	assert.Equal(t, f.stored.Encrypted, st.Encrypted, "unchanged")
	assert.Empty(t, f.booker.logins)
	assert.Empty(t, f.messages())
	assert.Equal(t, 1.0, f.checks(checkChallenge))
	assert.Equal(t, 1.0, testutil.ToFloat64(f.metrics.sessionsStored))
}

func TestInconclusiveCheckKeepsTheSession(t *testing.T) {
	f := newRefreshFixture(t)
	f.booker.check = func(wodbuster.Session) (wodbuster.Session, error) {
		return wodbuster.Session{}, errors.New("dial tcp: i/o timeout")
	}

	f.bs.RefreshSessions()

	st, ok := f.get(t)
	require.True(t, ok)
	assert.Equal(t, f.stored.Encrypted, st.Encrypted)
	assert.Empty(t, f.booker.logins)
	assert.Equal(t, 1.0, f.checks(checkInconclusive))
}

func TestUnreadableSessionIsReplaced(t *testing.T) {
	f := newRefreshFixture(t)
	broken := f.stored
	broken.Encrypted = "not-a-sealed-session"
	require.NoError(t, f.bs.storage.SaveSession(context.Background(), broken))
	f.booker.check = func(wodbuster.Session) (wodbuster.Session, error) {
		t.Fatal("an unreadable session cannot be checked")
		return wodbuster.Session{}, nil
	}
	f.booker.login = func(string) (wodbuster.Session, error) {
		return wbSession("athlete-7", "fresh", time.Now(), time.Time{}), nil
	}

	f.bs.RefreshSessions()

	st, ok := f.get(t)
	require.True(t, ok)
	_, err := openSession(testKey, st)
	assert.NoError(t, err)
	assert.Equal(t, 1.0, f.checks(checkUnreadable))
}

// Only active athletes with a stored session are checked. A disabled one is
// not booked for, so its session is not worth a request.
func TestRefreshOnlyChecksActiveAthletesWithASession(t *testing.T) {
	f := newRefreshFixture(t)
	ctx := context.Background()

	disabled := storedUser(t, 8, "pw")
	disabled.Status = models.StatusDisabled
	require.NoError(t, f.bs.storage.SaveUser(ctx, disabled))
	st, err := sealSession(testKey, 8, wbSession("athlete-8", "v", time.Now(), time.Time{}), time.Now())
	require.NoError(t, err)
	require.NoError(t, f.bs.storage.SaveSession(ctx, st))
	// Chat 9 is active but has never had a session stored.
	require.NoError(t, f.bs.storage.SaveUser(ctx, storedUser(t, 9, "pw")))

	f.booker.check = func(s wodbuster.Session) (wodbuster.Session, error) { return s, nil }
	f.bs.RefreshSessions()

	assert.Equal(t, []string{"athlete-7"}, f.booker.checks)
	assert.Empty(t, f.booker.logins)
	_, ok, err := f.bs.storage.GetSession(ctx, 8)
	require.NoError(t, err)
	assert.False(t, ok, "a disabled athlete's leftover session is deleted")
}

// The refresh holds a session for the length of a check or a login. Whatever
// happens to the athlete in that time wins over what the refresh writes.
func TestRefreshLosesToChangesMadeDuringIt(t *testing.T) {
	ctx := context.Background()
	disable := func(f *refreshFixture) {
		u, _ := f.bs.storage.GetUser(ctx, 7)
		u.Status = models.StatusDisabled
		require.NoError(t, f.bs.storage.SaveUser(ctx, u))
		require.NoError(t, f.bs.storage.DeleteSession(ctx, 7)) // what /disable does
	}
	relogin := func(f *refreshFixture) {
		st, err := sealSession(testKey, 7, wbSession("athlete-7", "from-/login", time.Now(), time.Time{}), time.Now())
		require.NoError(t, err)
		// Memory timestamps can tie with the fixture's within a clock tick.
		time.Sleep(time.Millisecond)
		require.NoError(t, f.bs.storage.SaveSession(ctx, st))
	}
	expired := func(wodbuster.Session) (wodbuster.Session, error) {
		return wodbuster.Session{}, wodbuster.ErrSessionExpired
	}

	for _, tc := range []struct {
		name     string
		expired  bool                  // the check refuses, so the refresh logs in
		meantime func(*refreshFixture) // done during the check, or during the login
		want     string                // the .WBAuth stored after; "" for none
	}{
		{"disabled during the check", false, disable, ""},
		{"/login during the check", false, relogin, "from-/login"},
		{"disabled during the login", true, disable, ""},
		{"/login during the login", true, relogin, "from-/login"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRefreshFixture(t)
			if tc.expired {
				f.booker.check = expired
				f.booker.login = func(string) (wodbuster.Session, error) {
					tc.meantime(f)
					return wbSession("athlete-7", "from-refresh", time.Now(), time.Time{}), nil
				}
			} else {
				f.booker.check = func(s wodbuster.Session) (wodbuster.Session, error) {
					tc.meantime(f)
					return s, nil
				}
			}

			f.bs.RefreshSessions()

			st, ok := f.get(t)
			if tc.want == "" {
				assert.False(t, ok, "the refresh brought back a deleted session")
				return
			}
			require.True(t, ok)
			sess, err := openSession(testKey, st)
			require.NoError(t, err)
			assert.Equal(t, tc.want, sess.Cookies[1].Value)
		})
	}
}

// An athlete disabled while the refresh is busy with someone else is not
// checked at all: the list read at the start is not trusted.
func TestRefreshReadsEachAthleteAgain(t *testing.T) {
	t.Run("disabled during another's check", testRefreshRereadDuringCheck)
	t.Run("disabled during the gap", testRefreshRereadDuringGap)
}

// testRefreshRereadDuringGap disables chat 8 once the refresh has read them
// before the gap; only the read after the gap can see it.
func testRefreshRereadDuringGap(t *testing.T) {
	f := newRefreshFixture(t)
	ctx := context.Background()
	u := storedUser(t, 8, "pw")
	require.NoError(t, f.bs.storage.SaveUser(ctx, u))
	st, err := sealSession(testKey, 8, wbSession("athlete-8", "v", time.Now(), time.Time{}), time.Now())
	require.NoError(t, err)
	require.NoError(t, f.bs.storage.SaveSession(ctx, st))

	hook := &disableOnRead{MemoryStorage: f.bs.storage.(*storage.MemoryStorage), chatID: 8, after: 1}
	f.bs.storage = hook
	f.bs.refreshGap = time.Millisecond
	f.bs.RefreshSessions()

	assert.Equal(t, []string{"athlete-7"}, f.booker.checks)
	assert.Equal(t, 2, hook.reads(), "read before and after the gap")
}

func testRefreshRereadDuringCheck(t *testing.T) {
	f := newRefreshFixture(t)
	ctx := context.Background()
	u := storedUser(t, 8, "pw")
	require.NoError(t, f.bs.storage.SaveUser(ctx, u))
	st, err := sealSession(testKey, 8, wbSession("athlete-8", "v", time.Now(), time.Time{}), time.Now())
	require.NoError(t, err)
	require.NoError(t, f.bs.storage.SaveSession(ctx, st))

	f.booker.check = func(s wodbuster.Session) (wodbuster.Session, error) {
		if s.AthleteID == "athlete-7" {
			u.Status = models.StatusDisabled
			require.NoError(t, f.bs.storage.SaveUser(ctx, u))
		}
		return s, nil
	}
	f.bs.RefreshSessions()

	assert.Equal(t, []string{"athlete-7"}, f.booker.checks)
}

func TestRefreshWaitsForTheBookingRun(t *testing.T) {
	f := newRefreshFixture(t)
	f.booker.check = func(wodbuster.Session) (wodbuster.Session, error) {
		t.Fatal("no session check during a booking run")
		return wodbuster.Session{}, nil
	}
	f.bs.weekRunning.Store(true)

	f.bs.RefreshSessions()
	assert.Empty(t, f.booker.checks)
}

// A rehearsal is tracked like a run in progress, but writes no session: the
// refresh at 03:00 or 15:00 must not be lost to a /rehearse.
func TestARehearsalDoesNotCancelTheRefresh(t *testing.T) {
	f := newRefreshFixture(t)
	f.bs.track(9, models.BookingAttempt{Day: "Monday"}, func() {})
	defer f.bs.untrack(9)

	f.bs.RefreshSessions()
	assert.Equal(t, []string{"athlete-7"}, f.booker.checks)
}

// Two refreshes never run side by side (a custom SESSION_REFRESH_CRON that
// fires again before the last one ended): the run could stop only one.
func TestASecondRefreshIsRefusedWhileOneIsGoing(t *testing.T) {
	f := newRefreshFixture(t)
	end, ok := f.bs.refreshJob.begin(func() {})
	require.True(t, ok)
	defer end()

	f.bs.RefreshSessions()
	assert.Empty(t, f.booker.checks)
}

// Six athletes' checks reach WodBuster one after another, not in the same second.
func TestRefreshSpacesOutTheChecks(t *testing.T) {
	f := newRefreshFixture(t)
	ctx := context.Background()
	u := storedUser(t, 8, "pw")
	require.NoError(t, f.bs.storage.SaveUser(ctx, u))
	st, err := sealSession(testKey, 8, wbSession("athlete-8", "v", time.Now(), time.Time{}), time.Now())
	require.NoError(t, err)
	require.NoError(t, f.bs.storage.SaveSession(ctx, st))

	f.bs.refreshGap = 50 * time.Millisecond
	var at []time.Time
	f.booker.check = func(s wodbuster.Session) (wodbuster.Session, error) {
		at = append(at, time.Now())
		return s, nil
	}
	f.bs.RefreshSessions()

	require.Len(t, at, 2)
	assert.GreaterOrEqual(t, at[1].Sub(at[0]), 50*time.Millisecond)
}

func TestSessionRefreshIsScheduled(t *testing.T) {
	bs, _ := withAdmins(t, &sessionBooker{})
	bs.SetSessionRefresh("0 3,15 * * *")
	bs.SetSessionPreflight("", ReuseShadow)
	require.NoError(t, bs.Start())
	defer bs.Stop()
	assert.Len(t, bs.cron.Entries(), 2, "the weekly run and the session refresh")

	bad, _ := withAdmins(t, &sessionBooker{})
	bad.SetSessionRefresh("not a cron spec")
	bad.SetSessionPreflight("", ReuseShadow)
	err := bad.Start()
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "session refresh"))
}

// The refresh must not hit WodBuster on a perfectly regular timer.
func TestJitterStaysInRangeAndVaries(t *testing.T) {
	assert.Zero(t, jitter(0))
	seen := map[time.Duration]bool{}
	for range 200 {
		d := jitter(time.Minute)
		require.GreaterOrEqual(t, d, time.Duration(0))
		require.Less(t, d, time.Minute)
		seen[d] = true
	}
	assert.Greater(t, len(seen), 100, "not the same delay every time")

	bs := NewBookingScheduler(nil, nil, DefaultOpening(time.UTC), testKey, nil)
	assert.Equal(t, 5*time.Minute, bs.refreshStartJitter, "defaults are on")
	assert.Equal(t, 20*time.Second, bs.refreshGapJitter)
}

// disableOnRead disables chatID right after its user has been read `after`
// times, as an admin's /disable landing between two reads would.
type disableOnRead struct {
	*storage.MemoryStorage
	chatID int64
	after  int

	mu sync.Mutex
	n  int
}

func (s *disableOnRead) GetUser(ctx context.Context, chatID int64) (models.User, bool) {
	u, ok := s.MemoryStorage.GetUser(ctx, chatID)
	if chatID != s.chatID {
		return u, ok
	}
	s.mu.Lock()
	s.n++
	n := s.n
	s.mu.Unlock()
	if n == s.after {
		d := u
		d.Status = models.StatusDisabled
		_ = s.MemoryStorage.SaveUser(ctx, d)
	}
	return u, ok
}

func (s *disableOnRead) reads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}
