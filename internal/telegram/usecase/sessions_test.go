package usecase

import (
	"context"
	"errors"
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
}

func (b *sessionBooker) Authenticate(context.Context, string, string) (wodbuster.Session, error) {
	return wodbuster.Session{}, nil
}

func (b *sessionBooker) Login(_ context.Context, email, _ string,
	_ func(int, int, error)) (wodbuster.Session, error) {
	b.mu.Lock()
	b.logins = append(b.logins, email)
	b.mu.Unlock()
	if b.login == nil {
		return wodbuster.Session{}, errors.New("no login scripted")
	}
	return b.login(email)
}

func (b *sessionBooker) CheckSession(_ context.Context, s wodbuster.Session) (wodbuster.Session, error) {
	b.mu.Lock()
	b.checks = append(b.checks, s.AthleteID)
	b.mu.Unlock()
	return b.check(s)
}

func (b *sessionBooker) Run(context.Context, string, string, []booking.Target,
	booking.RunOptions) ([]booking.Outcome, error) {
	return nil, nil
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
	bs.refreshGap = 0
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
	assert.InDelta(t, 48*3600, testutil.ToFloat64(f.metrics.sessionMaxAge), 60)
	assert.InDelta(t, time.Until(renewedUntil).Seconds(),
		testutil.ToFloat64(f.metrics.sessionAuthExpiry.WithLabelValues("7")), 60)
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
}

func TestRefreshWaitsForTheBookingRun(t *testing.T) {
	f := newRefreshFixture(t)
	f.booker.check = func(wodbuster.Session) (wodbuster.Session, error) {
		t.Fatal("no session check during a booking run")
		return wodbuster.Session{}, nil
	}
	f.bs.activeBookingsMux.Lock()
	f.bs.activeBookings[7] = &BookingContext{ChatID: 7, Cancel: func() {}}
	f.bs.activeBookingsMux.Unlock()

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
	require.NoError(t, bs.Start())
	defer bs.Stop()
	assert.Len(t, bs.cron.Entries(), 2, "the weekly run and the session refresh")

	bad, _ := withAdmins(t, &sessionBooker{})
	bad.SetSessionRefresh("not a cron spec")
	err := bad.Start()
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "session refresh"))
}
