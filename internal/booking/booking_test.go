package booking_test

import (
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
