package booking_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/booking"
)

// The opening is the one thing that must not be off by a week.
func TestNextOpening(t *testing.T) {
	madrid, err := time.LoadLocation("Europe/Madrid")
	require.NoError(t, err)

	sunday := func(h, m int) time.Time {
		return time.Date(2026, time.September, 27, h, m, 0, 0, madrid)
	}

	for name, tc := range map[string]struct {
		now  time.Time
		want time.Time
	}{
		"hours before, same day": {sunday(11, 50), sunday(12, 0)},
		"a minute after":         {sunday(12, 1), sunday(12, 0).AddDate(0, 0, 7)},
		"exactly at the opening": {sunday(12, 0), sunday(12, 0).AddDate(0, 0, 7)},
		"the day before":         {sunday(23, 0).AddDate(0, 0, -1), sunday(12, 0)},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := booking.NextOpening(tc.now, time.Sunday, "12:00", madrid)
			require.NoError(t, err)
			assert.True(t, got.Equal(tc.want), "got %s, want %s", got, tc.want)
		})
	}

	// A clock in another zone must not move it: the opening is 12:00 in Madrid.
	got, err := booking.NextOpening(sunday(11, 50).UTC(), time.Sunday, "12:00", madrid)
	require.NoError(t, err)
	assert.True(t, got.Equal(sunday(12, 0)))

	_, err = booking.NextOpening(time.Now(), time.Sunday, "25:00", madrid)
	assert.Error(t, err)
}

// A run must refuse an opening that is days away rather than sit on it until
// somebody else's timeout fires. On 2026-10-04 a late run computed the next
// Sunday, waited, and was killed twenty minutes later by its own context —
// reported to the user as "context deadline exceeded", which says nothing.
func TestRunRefusesAnImplausibleWait(t *testing.T) {
	svc := booking.New("firespain", time.UTC, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := svc.Run(context.Background(), "a@b.c", "pw",
		[]booking.Target{{Weekday: time.Monday, Start: "07:00", Class: "Wod"}},
		booking.RunOptions{OpensAt: time.Now().Add(7 * 24 * time.Hour)})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to wait")
	assert.Contains(t, err.Error(), "computed the wrong opening",
		"the message must point at the cause, not just the symptom")
}
