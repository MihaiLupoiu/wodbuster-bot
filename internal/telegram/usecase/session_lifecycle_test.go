package usecase

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/utils"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster/browserauth"
)

var errStore = errors.New("store unavailable")

// flakyStore is the in-memory store with switches that make one call fail.
type flakyStore struct {
	*storage.MemoryStorage
	failListUsers, failGetSession, failSaveUser, failDeleteSession atomic.Bool
}

func flaky(bs *BookingScheduler) *flakyStore {
	s := &flakyStore{MemoryStorage: bs.storage.(*storage.MemoryStorage)}
	bs.storage = s
	return s
}

func (s *flakyStore) ListUsers(ctx context.Context) ([]models.User, error) {
	if s.failListUsers.Load() {
		return nil, errStore
	}
	return s.MemoryStorage.ListUsers(ctx)
}

func (s *flakyStore) GetSession(ctx context.Context, chatID int64) (models.StoredSession, bool, error) {
	if s.failGetSession.Load() {
		return models.StoredSession{}, false, errStore
	}
	return s.MemoryStorage.GetSession(ctx, chatID)
}

func (s *flakyStore) SaveUser(ctx context.Context, u models.User) error {
	if s.failSaveUser.Load() {
		return errStore
	}
	return s.MemoryStorage.SaveUser(ctx, u)
}

func (s *flakyStore) DeleteSession(ctx context.Context, chatID int64) error {
	if s.failDeleteSession.Load() {
		return errStore
	}
	return s.MemoryStorage.DeleteSession(ctx, chatID)
}

func storeSession(t *testing.T, bs *BookingScheduler, chatID int64, auth string, loggedIn time.Time) {
	t.Helper()
	st, err := sealSession(testKey, chatID, wbSession("athlete", auth, loggedIn, time.Now().Add(40*24*time.Hour)), loggedIn)
	require.NoError(t, err)
	require.NoError(t, bs.storage.SaveSession(context.Background(), st))
}

func TestRunBlockerStop(t *testing.T) {
	var j runBlocker
	running, ended := j.stop(time.Millisecond)
	assert.False(t, running, "nothing to stop")
	assert.True(t, ended)

	// A job that ends when cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	end, ok := j.begin(cancel)
	require.True(t, ok)
	go func() { <-ctx.Done(); end() }()
	running, ended = j.stop(time.Second)
	assert.True(t, running)
	assert.True(t, ended)
	end() // a second end is harmless

	// A job that ignores the cancel does not hold the caller past the wait.
	_, cancel2 := context.WithCancel(context.Background())
	end2, ok := j.begin(cancel2)
	require.True(t, ok)
	defer end2()
	start := time.Now()
	running, ended = j.stop(50 * time.Millisecond)
	assert.True(t, running)
	assert.False(t, ended)
	assert.Less(t, time.Since(start), time.Second)
}

// One job at a time: a second is refused, so the run never has a job going
// that it cannot see to stop.
func TestRunBlockerRefusesASecondJob(t *testing.T) {
	var j runBlocker
	_, cancel := context.WithCancel(context.Background())
	end, ok := j.begin(cancel)
	require.True(t, ok)

	_, ok = j.begin(func() {})
	assert.False(t, ok, "the first is still going")

	end()
	end3, ok := j.begin(func() {})
	assert.True(t, ok, "free again once the first ended")
	end3()
}

func TestStopAllWaitsForTheJobsTogether(t *testing.T) {
	var a, b runBlocker
	for _, j := range []*runBlocker{&a, &b} {
		_, cancel := context.WithCancel(context.Background())
		end, ok := j.begin(cancel)
		require.True(t, ok)
		defer end() // ignores the cancel: each stop waits the full time
	}
	start := time.Now()
	res := stopAll(100*time.Millisecond, &a, &b)
	assert.Less(t, time.Since(start), 190*time.Millisecond, "not one after the other")
	for _, r := range res {
		assert.True(t, r.running)
		assert.False(t, r.ended)
	}
}

// blockingCheck is a booker whose CheckSession waits until its context ends.
type blockingCheck struct {
	*sessionBooker
	entered chan struct{}
}

func (b *blockingCheck) CheckSession(ctx context.Context, _ wodbuster.Session) (wodbuster.Session, error) {
	close(b.entered)
	<-ctx.Done()
	return wodbuster.Session{}, ctx.Err()
}

// A refresh still going when the run starts (a custom SESSION_REFRESH_CRON)
// is stopped, and the run goes ahead once it has.
func TestTheRunStopsARefreshInProgress(t *testing.T) {
	f := newRefreshFixture(t)
	f.bs.booker = &blockingCheck{sessionBooker: f.booker, entered: make(chan struct{})}

	done := make(chan struct{})
	go func() { defer close(done); f.bs.RefreshSessions() }()
	<-f.bs.booker.(*blockingCheck).entered

	f.bs.runWeek(time.Now())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh did not stop for the run")
	}
	_, ok := f.get(t)
	assert.True(t, ok, "a check cut short teaches nothing; the session is kept")
	assert.Empty(t, f.messages())
}

func expiredFixture(t *testing.T) *refreshFixture {
	t.Helper()
	f := newRefreshFixture(t)
	u := storedUser(t, 8, "pw")
	u.Email = "eight@example.com"
	require.NoError(t, f.bs.storage.SaveUser(context.Background(), u))
	storeSession(t, f.bs, 8, "old-8", time.Now().Add(-72*time.Hour))
	f.booker.check = func(wodbuster.Session) (wodbuster.Session, error) {
		return wodbuster.Session{}, wodbuster.ErrSessionExpired
	}
	return f
}

// One failed login means WodBuster's login is likely broken for everyone: the
// refresh stops trying rather than spend its budget on the same failure.
func TestRefreshStopsLoggingInAfterAFailedLogin(t *testing.T) {
	f := expiredFixture(t)
	f.booker.login = func(string) (wodbuster.Session, error) { return wodbuster.Session{}, errLoginStuck }

	f.bs.RefreshSessions()

	assert.Equal(t, 1, f.booker.authenticates, "one login, and no more")
	for _, chat := range []int64{7, 8} {
		_, ok, err := f.bs.storage.GetSession(context.Background(), chat)
		require.NoError(t, err)
		assert.False(t, ok, "chat %d: a refused session is deleted", chat)
	}
	msgs := f.messages()[adminChat]
	require.Len(t, msgs, 2, "the admins hear about each athlete")
	assert.Contains(t, msgs[1], "not tried")
}

// A wrong password is one athlete's problem, not a broken login.
func TestAWrongPasswordDoesNotStopTheOtherLogins(t *testing.T) {
	f := expiredFixture(t)
	f.booker.login = func(email string) (wodbuster.Session, error) {
		if email == "athlete@example.com" {
			return wodbuster.Session{}, browserauth.ErrRejected
		}
		return wbSession("athlete-8", "fresh-8", time.Now(), time.Time{}), nil
	}

	f.bs.RefreshSessions()

	assert.Equal(t, 2, f.booker.authenticates)
	st, ok, err := f.bs.storage.GetSession(context.Background(), 8)
	require.NoError(t, err)
	require.True(t, ok)
	sess, err := openSession(testKey, st)
	require.NoError(t, err)
	assert.Equal(t, "fresh-8", sess.Cookies[1].Value)
}

func TestRefreshWithABrokenStore(t *testing.T) {
	f := newRefreshFixture(t)
	store := flaky(f.bs)
	f.booker.check = func(s wodbuster.Session) (wodbuster.Session, error) {
		t.Fatal("nothing to check without the users or the session")
		return s, nil
	}

	store.failListUsers.Store(true)
	f.bs.RefreshSessions()

	store.failListUsers.Store(false)
	store.failGetSession.Store(true)
	f.bs.RefreshSessions()
	assert.Empty(t, f.messages(), "a database blip is a log line, not an alarm")
}

// A stored payload that decrypts but is not a session is as useless as one
// that does not decrypt.
func TestUndecodableSessionsAreReplaced(t *testing.T) {
	for _, payload := range []string{"not json", "{}"} {
		t.Run(payload, func(t *testing.T) {
			f := newRefreshFixture(t)
			enc, err := utils.EncryptPassword(payload, testKey)
			require.NoError(t, err)
			broken := f.stored
			broken.Encrypted = enc
			require.NoError(t, f.bs.storage.SaveSession(context.Background(), broken))
			f.booker.login = func(string) (wodbuster.Session, error) {
				return wbSession("athlete-7", "fresh", time.Now(), time.Time{}), nil
			}

			f.bs.RefreshSessions()

			assert.Equal(t, 1.0, f.checks(checkUnreadable))
			st, ok := f.get(t)
			require.True(t, ok)
			sess, err := openSession(testKey, st)
			require.NoError(t, err)
			assert.Equal(t, "fresh", sess.Cookies[1].Value)
		})
	}
}

func shadowScheduler(t *testing.T) *BookingScheduler {
	t.Helper()
	bs, _ := withAdmins(t, &sessionBooker{}, storedUser(t, 7, "pw"))
	bs.SetSessionPreflight("", ReuseShadow)
	return bs
}

// With no preflight on record, a session up to freshLoginWindow old counts as
// this morning's.
func TestRunSessionWindowWithoutAPreflight(t *testing.T) {
	bs := shadowScheduler(t)
	now := time.Now()

	storeSession(t, bs, 7, "recent", now.Add(-freshLoginWindow+time.Minute))
	assert.NotNil(t, bs.runSession(context.Background(), 7, now))

	storeSession(t, bs, 7, "older", now.Add(-freshLoginWindow-time.Minute))
	assert.Nil(t, bs.runSession(context.Background(), 7, now))
}

// A preflight moved earlier than two hours before the run still has its
// sessions used: what counts is logging in after it started.
func TestRunSessionWindowFollowsThePreflight(t *testing.T) {
	bs := shadowScheduler(t)
	now := time.Now()
	bs.preflightStarted.Store(now.Add(-3 * time.Hour).UnixNano())

	storeSession(t, bs, 7, "by-the-preflight", now.Add(-170*time.Minute))
	assert.NotNil(t, bs.runSession(context.Background(), 7, now))

	storeSession(t, bs, 7, "before-the-preflight", now.Add(-190*time.Minute))
	assert.Nil(t, bs.runSession(context.Background(), 7, now))

	// Last week's preflight says nothing about this week.
	bs.preflightStarted.Store(now.Add(-7 * 24 * time.Hour).UnixNano())
	storeSession(t, bs, 7, "by-the-preflight", now.Add(-170*time.Minute))
	assert.Nil(t, bs.runSession(context.Background(), 7, now))
}

// Anything wrong with the stored session means the run logs in, as before.
func TestRunSessionFallsBackToALogin(t *testing.T) {
	bs := shadowScheduler(t)
	storeSession(t, bs, 7, "fresh", time.Now())

	store := flaky(bs)
	store.failGetSession.Store(true)
	assert.Nil(t, bs.runSession(context.Background(), 7, time.Now()))
	store.failGetSession.Store(false)

	st, _, _ := bs.storage.GetSession(context.Background(), 7)
	st.Encrypted = "not-a-sealed-session"
	require.NoError(t, bs.storage.SaveSession(context.Background(), st))
	assert.Nil(t, bs.runSession(context.Background(), 7, time.Now()))
}

func TestPreflightCannotReadTheUsers(t *testing.T) {
	f := newPreflightFixture(t)
	flaky(f.bs).failListUsers.Store(true)

	f.bs.preflight(preflightTime(f.bs))

	assert.Equal(t, 1.0, testutil.ToFloat64(f.metrics.preflights.WithLabelValues("failed")))
	assert.Empty(t, f.booker.logins)
}

// A Sunday with nobody to book for still leaves a trace, so an alert on
// "no preflight this week" does not fire.
func TestPreflightWithNobodyToBookFor(t *testing.T) {
	bs, _ := withAdmins(t, &sessionBooker{}, adminUser(t, adminChat, "admin@example.com"))
	bs.SetSessionPreflight("", ReuseShadow)
	m := NewMetrics(prom.NewRegistry())
	bs.SetMetrics(m)

	bs.preflight(preflightTime(bs))

	assert.Equal(t, 1.0, testutil.ToFloat64(m.preflights.WithLabelValues("nobody")))
}

// When every login fails, the admins hear once straight away and once in the
// summary, however many athletes failed.
func TestPreflightEveryLoginFails(t *testing.T) {
	f := newPreflightFixture(t)
	f.booker.retries = map[string]int{email7: 1, email8: 1}
	f.booker.login = func(string) (wodbuster.Session, error) { return wodbuster.Session{}, errLoginStuck }

	f.bs.preflight(preflightTime(f.bs))

	assert.Equal(t, 1.0, testutil.ToFloat64(f.metrics.preflights.WithLabelValues("failed")))
	msgs := f.messages()[adminChat]
	require.Len(t, msgs, 2)
	assert.Contains(t, msgs[0], "a login failed")
	assert.Contains(t, msgs[1], "0 of 2 athletes logged in")
}

func TestPreflightAllOKMessageCanBeTurnedOff(t *testing.T) {
	f := newPreflightFixture(t)
	f.bs.SetPreflightAllOKMessage(false)

	f.bs.preflight(preflightTime(f.bs))

	assert.Equal(t, 1.0, testutil.ToFloat64(f.metrics.preflights.WithLabelValues("all_ok")))
	assert.Empty(t, f.messages()[adminChat], "all well, and the admins asked for quiet")
}

// A session the shadow check found refused is gone even when the login after
// it fails: nothing should mistake it for a working one.
func TestPreflightDeletesARefusedSession(t *testing.T) {
	f := newPreflightFixture(t)
	f.booker.check = func(wodbuster.Session) (wodbuster.Session, error) {
		return wodbuster.Session{}, wodbuster.ErrSessionExpired
	}
	f.booker.login = func(string) (wodbuster.Session, error) { return wodbuster.Session{}, errLoginStuck }

	f.bs.preflight(preflightTime(f.bs))

	_, ok, err := f.bs.storage.GetSession(context.Background(), 7)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, 1.0, f.reuse(reuseWouldFail))
}

// A rehearsal neither books with the stored session nor replaces it.
func TestARehearsalLeavesTheStoredSessionAlone(t *testing.T) {
	f := newPreflightFixture(t)
	f.bs.preflight(preflightTime(f.bs))
	_, before := f.stored(t, 7)

	_, err := f.bs.Rehearse(context.Background(), 7)
	require.NoError(t, err)

	require.Contains(t, f.booker.runs, email7)
	assert.Nil(t, f.booker.runs[email7].Session)
	assert.Nil(t, f.booker.runs[email7].OnSession)
	_, after := f.stored(t, 7)
	assert.Equal(t, before.Encrypted, after.Encrypted)
}

func TestSessionGaugesComeFromTheStore(t *testing.T) {
	f := newRefreshFixture(t)
	f.bs.SetSessionPreflight("", ReuseShadow)

	require.NoError(t, f.bs.Start(), "a restart sets the gauges straight away")
	defer f.bs.Stop()
	require.Eventually(t, func() bool { return testutil.ToFloat64(f.metrics.sessionsStored) == 1 },
		5*time.Second, 10*time.Millisecond)
	assert.Equal(t, 1.0, testutil.ToFloat64(f.metrics.reuseMode.WithLabelValues("shadow")))
	f.bs.gaugesMu.Lock() // Start's update is done; the store below is swapped safely
	f.bs.gaugesMu.Unlock()

	// A read that fails leaves them as they were rather than dropping the
	// athlete and setting off the "session lost" alerts.
	flaky(f.bs).failGetSession.Store(true)
	f.bs.updateSessionGauges(context.Background())
	assert.Equal(t, 1.0, testutil.ToFloat64(f.metrics.sessionsStored))
}

// Every login is made with "remember this device"; the daily check keeps the
// session it makes instead of leaving one more remembered device behind.
func TestLoginCheckKeepsTheAdminsSession(t *testing.T) {
	for _, mode := range []ReuseMode{ReuseShadow, ReuseOff} {
		t.Run(string(mode), func(t *testing.T) {
			booker := NewMockBooker(t)
			bs, notifier := withAdmins(t, booker, adminUser(t, adminChat, "first@example.com"))
			collect(notifier)
			bs.SetSessionPreflight("", mode)
			booker.EXPECT().Authenticate(mock.Anything, "first@example.com", "hunter2").
				Return(wbSession("admin", "from-check", time.Now(), time.Time{}), nil)

			bs.CheckLogin()

			_, ok, err := bs.storage.GetSession(context.Background(), adminChat)
			require.NoError(t, err)
			assert.Equal(t, mode == ReuseShadow, ok, "stored only when sessions are kept at all")
		})
	}
}

func TestTheTestCommandKeepsItsSession(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	u := storedUser(t, 7, "pw")
	require.NoError(t, store.SaveUser(ctx, u))
	m := managerLoggingIn(t, store, wbSession("a7", "from-test", time.Now(), time.Time{}))

	require.NoError(t, m.TestUserSession(ctx, 7))

	st, ok, err := store.GetSession(ctx, 7)
	require.NoError(t, err)
	require.True(t, ok)
	sess, err := openSession(testKey, st)
	require.NoError(t, err)
	assert.Equal(t, "from-test", sess.Cookies[1].Value)
}

// SESSION_REUSE=off keeps no sessions: no refresh, nothing stored by /login.
func TestReuseOffKeepsNoSessions(t *testing.T) {
	bs, _ := withAdmins(t, &sessionBooker{})
	bs.SetSessionRefresh("0 3,15 * * *")
	require.NoError(t, bs.Start())
	defer bs.Stop()
	assert.Len(t, bs.cron.Entries(), 1, "only the weekly run")

	ctx := context.Background()
	store := storage.NewMemoryStorage()
	require.NoError(t, store.SaveUser(ctx, storedUser(t, 7, "old-pw")))
	m := managerLoggingIn(t, store, wbSession("a7", "v", time.Now(), time.Time{}))
	m.bookingScheduler.SetSessionPreflight("", ReuseOff)
	require.NoError(t, m.LogInAndSave(ctx, 7, "athlete@example.com", "pw"))
	_, ok, err := store.GetSession(ctx, 7)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestABadPreflightCronStopsTheStart(t *testing.T) {
	bs, _ := withAdmins(t, &sessionBooker{})
	bs.SetSessionPreflight("not a cron spec", ReuseShadow)
	err := bs.Start()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session preflight")
}

func TestALoginThatCannotBeSavedStoresNoSession(t *testing.T) {
	ctx := context.Background()
	bs, _ := withAdmins(t, &sessionBooker{}, storedUser(t, 7, "old-pw"))
	store := flaky(bs)
	store.failSaveUser.Store(true)
	m := managerLoggingIn(t, store, wbSession("a7", "v", time.Now(), time.Time{}))

	require.Error(t, m.LogInAndSave(ctx, 7, "athlete@example.com", "pw"))
	_, ok, err := store.GetSession(ctx, 7)
	require.NoError(t, err)
	assert.False(t, ok)
}

// Disabling must work even if the session cannot be deleted; the refresh
// deletes it later.
func TestDisablingSucceedsWhenTheSessionCannotBeDeleted(t *testing.T) {
	ctx := context.Background()
	bs, _ := withAdmins(t, &sessionBooker{}, storedUser(t, 7, "pw"))
	store := flaky(bs)
	store.failDeleteSession.Store(true)
	m := managerWith(t, store)

	u, err := m.SetUserStatus(ctx, 7, models.StatusDisabled)
	require.NoError(t, err)
	assert.Equal(t, models.StatusDisabled, u.Status)
}

// disableDuringSave is the in-memory store where an admin's /disable lands
// in the middle of a session save: after the saver read the athlete as
// active, before its write. /disable saves the user, then deletes the session.
type disableDuringSave struct {
	*storage.MemoryStorage
	chatID int64
}

func (s *disableDuringSave) SaveSession(ctx context.Context, st models.StoredSession) error {
	if st.ChatID == s.chatID {
		u, _ := s.MemoryStorage.GetUser(ctx, st.ChatID)
		u.Status = models.StatusDisabled
		_ = s.MemoryStorage.SaveUser(ctx, u)
		_ = s.MemoryStorage.DeleteSession(ctx, st.ChatID)
	}
	return s.MemoryStorage.SaveSession(ctx, st)
}

// Every session write the athlete did not ask for loses to a /disable,
// however the two interleave.
func TestADisableAlwaysWinsOverASave(t *testing.T) {
	ctx := context.Background()
	st, err := sealSession(testKey, 7, wbSession("a7", "v", time.Now(), time.Time{}), time.Now())
	require.NoError(t, err)

	t.Run("disabled before", func(t *testing.T) {
		u := storedUser(t, 7, "pw")
		u.Status = models.StatusDisabled
		bs, _ := withAdmins(t, &sessionBooker{}, u)
		saved, err := bs.saveForActive(ctx, st)
		require.NoError(t, err)
		assert.False(t, saved)
		_, ok, _ := bs.storage.GetSession(ctx, 7)
		assert.False(t, ok)
	})
	t.Run("disabled during", func(t *testing.T) {
		bs, _ := withAdmins(t, &sessionBooker{}, storedUser(t, 7, "pw"))
		bs.storage = &disableDuringSave{MemoryStorage: bs.storage.(*storage.MemoryStorage), chatID: 7}
		saved, err := bs.saveForActive(ctx, st)
		require.NoError(t, err)
		assert.False(t, saved)
		_, ok, _ := bs.storage.GetSession(ctx, 7)
		assert.False(t, ok, "the save that landed after the delete is undone")
	})
	t.Run("gone", func(t *testing.T) {
		bs, _ := withAdmins(t, &sessionBooker{})
		saved, err := bs.saveForActive(ctx, st)
		require.NoError(t, err)
		assert.False(t, saved)
	})
	t.Run("active", func(t *testing.T) {
		bs, _ := withAdmins(t, &sessionBooker{}, storedUser(t, 7, "pw"))
		saved, err := bs.saveForActive(ctx, st)
		require.NoError(t, err)
		assert.True(t, saved)
	})
}

// A /disable while the preflight is logging the athlete in: the session it
// gets is not kept.
func TestThePreflightKeepsNoSessionForAnAthleteDisabledMeanwhile(t *testing.T) {
	f := newPreflightFixture(t)
	f.bs.storage = &disableDuringSave{MemoryStorage: f.bs.storage.(*storage.MemoryStorage), chatID: 8}

	f.bs.preflight(preflightTime(f.bs))

	_, ok, err := f.bs.storage.GetSession(context.Background(), 8)
	require.NoError(t, err)
	assert.False(t, ok)
	f.stored(t, 7) // the others are untouched
	assert.Equal(t, 1.0, testutil.ToFloat64(f.metrics.preflights.WithLabelValues("partial")))
}

// The login check and /test store their session only for an athlete still
// active when it is written.
func TestAFreshSessionIsNotKeptForAnAthleteDisabledMeanwhile(t *testing.T) {
	bs, _ := withAdmins(t, &sessionBooker{}, storedUser(t, 7, "pw"))
	bs.SetSessionPreflight("", ReuseShadow)
	bs.storage = &disableDuringSave{MemoryStorage: bs.storage.(*storage.MemoryStorage), chatID: 7}
	u, _ := bs.storage.GetUser(context.Background(), 7)

	bs.storeFreshSession(context.Background(), u, wbSession("a7", "v", time.Now(), time.Time{}), sourceTest)

	_, ok, err := bs.storage.GetSession(context.Background(), 7)
	require.NoError(t, err)
	assert.False(t, ok)
}

// SESSION_REUSE=off deletes the sessions shadow weeks left behind, and keeps
// the session gauges out of it.
func TestReuseOffClearsLeftoverSessions(t *testing.T) {
	bs, _ := withAdmins(t, &sessionBooker{}, storedUser(t, 7, "pw"))
	m := NewMetrics(prom.NewRegistry())
	bs.SetMetrics(m)
	storeSession(t, bs, 7, "left-over", time.Now().Add(-24*time.Hour))

	require.NoError(t, bs.Start())
	defer bs.Stop()
	require.Eventually(t, func() bool {
		_, ok, err := bs.storage.GetSession(context.Background(), 7)
		return err == nil && !ok
	}, 5*time.Second, 10*time.Millisecond)

	bs.updateSessionGauges(context.Background())
	assert.Equal(t, 0, testutil.CollectAndCount(m.sessionAuthExpiry), "no session gauges in off mode")
	assert.Equal(t, 1.0, testutil.ToFloat64(m.reuseMode.WithLabelValues("off")))
}
