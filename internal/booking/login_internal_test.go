package booking

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
