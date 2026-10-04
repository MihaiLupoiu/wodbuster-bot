package usecase

import (
	"errors"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/booking"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

// "full" is the gym being busy and needs nobody woken up. Everything else means
// something is wrong with the bot, the account or the site, which is the whole
// point of splitting them.
func TestFailureReason(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"class full":    {wodbuster.ErrClassFull, "full"},
		"quota":         {wodbuster.ErrQuotaExceeded, "quota"},
		"not included":  {wodbuster.ErrNotIncluded, "not_included"},
		"busy":          {wodbuster.ErrBusy, "busy"},
		"session":       {wodbuster.ErrSessionExpired, "session_expired"},
		"wrapped":       {errors.Join(errors.New("Book"), wodbuster.ErrClassFull), "full"},
		"not published": {errors.New("race: day never published"), "not_published"},
		"login":         {errors.New("login failed: browserauth: ..."), "login"},
		"timeout":       {errors.New("context deadline exceeded"), "timeout"},
		"anything else": {errors.New("boom"), "error"},
		"no error":      {nil, "none"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, failureReason(tc.err))
		})
	}
}

func TestObserveOutcomeCountsAndBoundsLabels(t *testing.T) {
	reg := prom.NewRegistry()
	m := NewMetrics(reg)

	m.observeOutcome(booking.Outcome{Status: "booked"}, "Monday", "Wod")
	m.observeOutcome(booking.Outcome{Status: "waitlisted"}, "Friday", "Open box")
	// A class type nobody validated, and a weekday that is not one.
	m.observeOutcome(booking.Outcome{Status: "booked"}, "Funday", "Zumba")

	expected := `
# HELP wodbuster_bot_booking_outcomes_total What happened to each chased class, by outcome, weekday and class type.
# TYPE wodbuster_bot_booking_outcomes_total counter
wodbuster_bot_booking_outcomes_total{class="(others)",outcome="booked",weekday="(others)"} 1
wodbuster_bot_booking_outcomes_total{class="Open box",outcome="waitlisted",weekday="Friday"} 1
wodbuster_bot_booking_outcomes_total{class="Wod",outcome="booked",weekday="Monday"} 1
`
	require.NoError(t, testutil.CollectAndCompare(reg,
		stringsReader(expected), "wodbuster_bot_booking_outcomes_total"))
}

// A failed outcome also lands on the failure counter, under its reason.
func TestFailedOutcomeCountsAsAFailure(t *testing.T) {
	reg := prom.NewRegistry()
	m := NewMetrics(reg)

	m.observeOutcome(booking.Outcome{Status: "failed", Err: wodbuster.ErrClassFull}, "Monday", "Wod")
	m.observeOutcome(booking.Outcome{Status: "booked"}, "Monday", "Wod")

	assert.Equal(t, 1, testutil.CollectAndCount(reg, "wodbuster_bot_booking_failures_total"))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.failures.WithLabelValues("full")))
}

// The alert that matters most is "no run happened". A week with nothing
// scheduled is still a run, and must not trip it.
func TestRunLastSuccessIsSetOnCompletion(t *testing.T) {
	reg := prom.NewRegistry()
	m := NewMetrics(reg)

	assert.Equal(t, 0.0, testutil.ToFloat64(m.runLastSuccess))

	m.observeRunFinished("failed", time.Second)
	assert.Equal(t, 0.0, testutil.ToFloat64(m.runLastSuccess), "a failed run is not a success")

	m.observeRunFinished("completed", time.Second)
	assert.InDelta(t, float64(time.Now().Unix()), testutil.ToFloat64(m.runLastSuccess), 5)
}

// 2026-10-04: the cron fired 30 minutes late because the host had been
// suspended, and nothing measured it.
func TestRunLateness(t *testing.T) {
	reg := prom.NewRegistry()
	m := NewMetrics(reg)

	intended := time.Date(2026, time.October, 4, 11, 50, 0, 0, time.UTC)

	m.observeRunLateness(intended, intended.Add(-time.Second)) // early: zero
	m.observeRunLateness(intended, intended.Add(30*time.Minute))

	assert.Equal(t, uint64(2), collectHistogram(t, reg, "wodbuster_bot_run_lateness_seconds").GetSampleCount())
	assert.InDelta(t, 1800.0, collectHistogram(t, reg, "wodbuster_bot_run_lateness_seconds").GetSampleSum(), 0.001,
		"an early start contributes zero, a 30-minute miss contributes 1800")
}

func stringsReader(s string) *strings.Reader { return strings.NewReader(s) }

func collectHistogram(t *testing.T, g prom.Gatherer, name string) *dto.Histogram {
	t.Helper()
	families, err := g.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() == name {
			require.NotEmpty(t, f.GetMetric())
			return f.GetMetric()[0].GetHistogram()
		}
	}
	t.Fatalf("no metric named %q", name)
	return nil
}
