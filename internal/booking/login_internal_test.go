package booking

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster/browserauth"
)

// scriptedLogin fails with each of errs in turn, then succeeds.
func scriptedLogin(errs ...error) (func(context.Context, string, string) (wodbuster.Session, error), *int) {
	calls := 0
	return func(context.Context, string, string) (wodbuster.Session, error) {
		calls++
		if calls <= len(errs) {
			return wodbuster.Session{}, errs[calls-1]
		}
		return wodbuster.Session{Box: "firespain", AthleteID: "a"}, nil
	}, &calls
}

func quietService(attempts int) *Service {
	s := New("firespain", time.UTC, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithLoginAttempts(attempts, time.Millisecond))
	return s
}

var errStuck = errors.New("browserauth: login did not finish in time (wrong credentials?)")

// 2026-10-18: a login sat on the login page after the device prompt until it
// timed out, and the athlete got nothing. The same login had failed that way
// before and worked on the next try.
func TestAStuckLoginIsTriedAgain(t *testing.T) {
	s := quietService(3)
	var calls *int
	s.login, calls = scriptedLogin(errStuck, errStuck)

	sess, err := s.loginWithRetry(context.Background(), s.log, "a@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, "a", sess.AthleteID)
	assert.Equal(t, 3, *calls)
}

func TestLoginGivesUpAfterItsAttempts(t *testing.T) {
	s := quietService(3)
	var calls *int
	s.login, calls = scriptedLogin(errStuck, errStuck, errStuck, errStuck)

	_, err := s.loginWithRetry(context.Background(), s.log, "a@example.com", "pw")
	assert.ErrorIs(t, err, errStuck)
	assert.Equal(t, 3, *calls)
}

// Wrong credentials stay wrong; trying them again only risks the account.
func TestRejectedCredentialsAreNotRetried(t *testing.T) {
	s := quietService(3)
	var calls *int
	s.login, calls = scriptedLogin(browserauth.ErrRejected)

	_, err := s.loginWithRetry(context.Background(), s.log, "a@example.com", "pw")
	assert.ErrorIs(t, err, browserauth.ErrRejected)
	assert.Equal(t, 1, *calls)
}

func TestLoginRetryStopsWithTheRun(t *testing.T) {
	s := New("firespain", time.UTC, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithLoginAttempts(3, time.Hour))
	var calls *int
	s.login, calls = scriptedLogin(errStuck, errStuck)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := s.loginWithRetry(ctx, s.log, "a@example.com", "pw")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ErrorIs(t, err, errStuck, "the login's own error is kept")
	assert.Equal(t, 1, *calls)
}

// Every run starts at 11:50, so every athlete's browser used to hit the login
// in the same second. Logins now go one at a time, spaced by the gap.
func TestLoginsGoOneAtATimeWithAGap(t *testing.T) {
	const gap = 60 * time.Millisecond
	g := newLoginGate(gap)

	type span struct{ start, end time.Time }
	spans := make(chan span, 3)
	done := make(chan struct{})
	for i := 0; i < 3; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_, err := g.acquire(context.Background())
			assert.NoError(t, err)
			sp := span{start: time.Now()}
			time.Sleep(10 * time.Millisecond) // the login itself
			sp.end = time.Now()
			g.release()
			spans <- sp
		}()
	}
	for i := 0; i < 3; i++ {
		<-done
	}
	close(spans)

	var all []span
	for sp := range spans {
		all = append(all, sp)
	}
	require.Len(t, all, 3)
	// Sorted by start, each login starts at least gap after the previous ended.
	sort.Slice(all, func(i, j int) bool { return all[i].start.Before(all[j].start) })
	for i := 1; i < len(all); i++ {
		assert.GreaterOrEqual(t, all[i].start.Sub(all[i-1].end), gap,
			"login %d started too soon after login %d", i, i-1)
	}
}

func TestTheFirstLoginDoesNotWait(t *testing.T) {
	g := newLoginGate(time.Hour)
	waited, err := g.acquire(context.Background())
	require.NoError(t, err)
	assert.Less(t, waited, 50*time.Millisecond)
	g.release()
}

// A run that gives up while queued must not keep the slot from the others.
func TestACancelledWaitReleasesTheSlot(t *testing.T) {
	g := newLoginGate(time.Hour)
	_, err := g.acquire(context.Background())
	require.NoError(t, err)
	g.release() // the next login now owes an hour's gap

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = g.acquire(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	select {
	case g.slot <- struct{}{}:
		<-g.slot
	default:
		t.Fatal("the cancelled wait kept the slot")
	}
}
