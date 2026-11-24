package booking

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster/wodbustertest"
)

// fakeService is a Service whose clients talk to srv and whose "browser login"
// hands out srv's session, counting how often it was asked.
func fakeService(t *testing.T, srv *wodbustertest.Server) (*Service, *int) {
	t.Helper()
	s := New("fake", time.UTC, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithLoginAttempts(3, time.Millisecond))
	s.clientOpts = []wodbuster.Option{wodbuster.WithHTTPClient(srv.HTTPClient())}
	logins := 0
	s.login = func(context.Context, string, string) (wodbuster.Session, error) {
		logins++
		sess := srv.Session()
		sess.Cookies[0].Value = "fresh"
		return sess, nil
	}
	return s, &logins
}

func storedSession(srv *wodbustertest.Server) *wodbuster.Session {
	sess := srv.Session()
	sess.Cookies[0].Value = "stored"
	sess.IssuedAt = time.Now().Add(-48 * time.Hour)
	return &sess
}

func renewal(until time.Time) *http.Cookie {
	return &http.Cookie{Name: wodbuster.AuthCookieName, Value: "renewed", Path: "/", Expires: until}
}

func TestTrustDeviceIsOnByDefault(t *testing.T) {
	assert.True(t, New("b", time.UTC, slog.Default()).trustDevice)
	assert.False(t, New("b", time.UTC, slog.Default(), WithTrustDevice(false)).trustDevice)
}

func TestAuthenticateReturnsTheSessionAndTriesOnce(t *testing.T) {
	s := quietService(3)
	var calls *int
	s.login, calls = scriptedLogin()
	sess, err := s.Authenticate(context.Background(), "a@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, "a", sess.AthleteID)
	assert.Equal(t, 1, *calls)

	s.login, calls = scriptedLogin(errStuck)
	_, err = s.Authenticate(context.Background(), "a@example.com", "pw")
	assert.ErrorIs(t, err, errStuck)
	assert.Equal(t, 1, *calls, "/login answers on the first failure")
}

// The preflight must log in exactly as the run does.
func TestLoginRetriesLikeTheRun(t *testing.T) {
	s := quietService(3)
	var calls *int
	s.login, calls = scriptedLogin(errStuck, errStuck)
	retries := 0
	sess, err := s.Login(context.Background(), "a@example.com", "pw",
		func(int, int, error) { retries++ })
	require.NoError(t, err)
	assert.Equal(t, "a", sess.AthleteID)
	assert.Equal(t, 3, *calls)
	assert.Equal(t, 2, retries)
}

func TestCheckSessionReturnsTheRenewedSession(t *testing.T) {
	srv := wodbustertest.New(t)
	s, logins := fakeService(t, srv)
	until := time.Now().Add(45 * 24 * time.Hour).UTC().Truncate(time.Second)
	srv.RenewCookies(renewal(until))

	got, err := s.CheckSession(context.Background(), *storedSession(srv))
	require.NoError(t, err)
	assert.True(t, got.AuthExpiry().Equal(until), "the renewed .WBAuth is what gets stored")
	assert.Equal(t, 0, *logins, "checking a session never opens a browser")
	assert.Equal(t, 1, srv.Count("LoadClass"), "one schedule read")
}

func TestCheckSessionSaysWhyItFailed(t *testing.T) {
	srv := wodbustertest.New(t)
	s, _ := fakeService(t, srv)

	srv.ExpireSession(true)
	_, err := s.CheckSession(context.Background(), *storedSession(srv))
	assert.ErrorIs(t, err, wodbuster.ErrSessionExpired)

	srv.ExpireSession(false)
	srv.Challenge(true)
	_, err = s.CheckSession(context.Background(), *storedSession(srv))
	assert.ErrorIs(t, err, wodbuster.ErrChallenge)
	assert.False(t, errors.Is(err, wodbuster.ErrSessionExpired))
}

// cookieValuesSent is the cookie each call to handler carried, which tells
// which session the run booked with.
func cookieValuesSent(srv *wodbustertest.Server, handler string) []string {
	var out []string
	for _, r := range srv.Requests() {
		if r.Handler == handler && len(r.Cookies) > 0 {
			out = append(out, r.Cookies[0].Value)
		}
	}
	return out
}

func runOnce(t *testing.T, s *Service, srv *wodbustertest.Server, o RunOptions) []Outcome {
	t.Helper()
	srv.AddClass(wodbustertest.Class{
		Date: wodbuster.NextWeekday(time.Now().UTC(), time.Monday), Start: "07:00", Name: "Wod"})
	o.GiveUpAfter = 2 * time.Second
	out, err := s.Run(context.Background(), "a@example.com", "pw",
		[]Target{{Weekday: time.Monday, Start: "07:00", Class: "Wod"}}, o)
	require.NoError(t, err)
	require.Len(t, out, 1)
	return out
}

func TestRunUsesAWorkingStoredSession(t *testing.T) {
	srv := wodbustertest.New(t)
	s, logins := fakeService(t, srv)
	until := time.Now().Add(45 * 24 * time.Hour).UTC().Truncate(time.Second)
	srv.RenewCookies(renewal(until))

	var got wodbuster.Session
	var reused bool
	calls := 0
	out := runOnce(t, s, srv, RunOptions{
		Session: storedSession(srv),
		OnSession: func(sess wodbuster.Session, r bool) {
			calls++
			got, reused = sess, r
		},
	})

	assert.Equal(t, "booked", out[0].Status)
	assert.Equal(t, 0, *logins, "no browser when the stored session works")
	assert.Equal(t, []string{"stored"}, cookieValuesSent(srv, "Calendario_Inscribir"))
	assert.Equal(t, 1, calls)
	assert.True(t, reused)
	assert.True(t, got.AuthExpiry().Equal(until), "the caller gets the renewed session to store")
}

func TestRunLogsInWhenTheStoredSessionIsRefused(t *testing.T) {
	srv := wodbustertest.New(t)
	s, logins := fakeService(t, srv)

	// The fake server knows nothing of cookies, so refuse the stored session
	// by expiring the server for exactly the one check, then recovering when
	// the login runs.
	srv.ExpireSession(true)
	s.login = func(context.Context, string, string) (wodbuster.Session, error) {
		*logins++
		srv.ExpireSession(false)
		sess := srv.Session()
		sess.Cookies[0].Value = "fresh"
		return sess, nil
	}

	var reused *bool
	out := runOnce(t, s, srv, RunOptions{
		Session:   storedSession(srv),
		OnSession: func(_ wodbuster.Session, r bool) { reused = &r },
	})

	assert.Equal(t, "booked", out[0].Status)
	assert.Equal(t, 1, *logins)
	assert.Equal(t, []string{"fresh"}, cookieValuesSent(srv, "Calendario_Inscribir"))
	require.NotNil(t, reused)
	assert.False(t, *reused)
}

func TestRunWithoutAStoredSessionLogsInAsBefore(t *testing.T) {
	srv := wodbustertest.New(t)
	s, logins := fakeService(t, srv)
	var reused *bool
	out := runOnce(t, s, srv, RunOptions{OnSession: func(_ wodbuster.Session, r bool) { reused = &r }})
	assert.Equal(t, "booked", out[0].Status)
	assert.Equal(t, 1, *logins)
	require.NotNil(t, reused)
	assert.False(t, *reused)
}

// A run whose login fails has no session to hand back.
func TestOnSessionIsNotCalledWhenTheLoginFails(t *testing.T) {
	srv := wodbustertest.New(t)
	s, _ := fakeService(t, srv)
	s.login, _ = scriptedLogin(errStuck, errStuck, errStuck)
	called := false
	_, err := s.Run(context.Background(), "a@example.com", "pw",
		[]Target{{Weekday: time.Monday, Start: "07:00", Class: "Wod"}},
		RunOptions{OnSession: func(wodbuster.Session, bool) { called = true }})
	assert.ErrorIs(t, err, errStuck)
	assert.False(t, called)
}
