package usecase

import (
	"errors"
	"strings"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/booking"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/platform/metrics"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

// knownClasses is the class-type allow-list from utils.ValidateClassType. The
// value reaches here from a /book command, so it is bounded here rather than
// trusted.
var knownClasses = map[string]bool{
	"Wod": true, "Open": true, "Strength": true, "Cardio": true, "Yoga": true,
	"Open box": true, "Hybrid": true,
}

// Metrics covers the weekly run: whether it happened, when, how late, and what
// it achieved.
type Metrics struct {
	runsTotal       *prom.CounterVec
	runLastSuccess  prom.Gauge
	runLateness     prom.Histogram
	runDuration     prom.Histogram
	pendingBookings prom.Gauge
	outcomes        *prom.CounterVec
	failures        *prom.CounterVec
}

func NewMetrics(reg prom.Registerer) *Metrics {
	f := promauto.With(reg)
	ns := metrics.Namespace

	return &Metrics{
		runsTotal: f.NewCounterVec(prom.CounterOpts{
			Namespace: ns, Name: "runs_total",
			Help: "Weekly booking runs, by result.",
		}, []string{"result"}),

		runLastSuccess: f.NewGauge(prom.GaugeOpts{
			Namespace: ns, Name: "run_last_success_timestamp_seconds",
			Help: "Unix time of the last run that finished. The single number worth " +
				"alerting on: time() minus this exceeding a week means the bot has " +
				"stopped working, whatever the cause.",
		}),

		runLateness: f.NewHistogram(prom.HistogramOpts{
			Namespace: ns, Name: "run_lateness_seconds",
			Help: "How far past its intended start a run actually began. A suspended " +
				"host fires its cron on resume, which is how a run once started 30 " +
				"minutes after the opening.",
			// Seconds either side of nothing, then the sizes of a real miss.
			Buckets: []float64{1, 5, 15, 60, 300, 900, 1800, 3600},
		}),

		runDuration: f.NewHistogram(prom.HistogramOpts{
			Namespace: ns, Name: "run_duration_seconds",
			Help:    "How long a run took from start to last outcome.",
			Buckets: []float64{5, 15, 30, 60, 120, 300, 600, 1200},
		}),

		pendingBookings: f.NewGauge(prom.GaugeOpts{
			Namespace: ns, Name: "pending_bookings",
			Help: "Bookings waiting for the next run, as counted when it starts. " +
				"Zero means a run that books nothing is doing its job.",
		}),

		outcomes: f.NewCounterVec(prom.CounterOpts{
			Namespace: ns, Name: "booking_outcomes_total",
			Help: "What happened to each chased class, by outcome, weekday and class type.",
		}, []string{"outcome", "weekday", "class"}),

		failures: f.NewCounterVec(prom.CounterOpts{
			Namespace: ns, Name: "booking_failures_total",
			Help: "Bookings that did not happen, by reason. \"full\" is the gym being " +
				"busy; everything else is something to fix.",
		}, []string{"reason"}),
	}
}

func (m *Metrics) observeRunStart(pending int) {
	if m == nil {
		return
	}
	m.pendingBookings.Set(float64(pending))
}

// observeRunLateness takes the opening the run was for and the moment it began.
// A run that starts before the opening — the normal case — is zero late.
func (m *Metrics) observeRunLateness(intendedStart, actualStart time.Time) {
	if m == nil {
		return
	}
	late := actualStart.Sub(intendedStart)
	if late < 0 {
		late = 0
	}
	m.runLateness.Observe(late.Seconds())
}

func (m *Metrics) observeRunFinished(result string, d time.Duration) {
	if m == nil {
		return
	}
	m.runsTotal.WithLabelValues(result).Inc()
	m.runDuration.Observe(d.Seconds())
	if result == "completed" {
		m.runLastSuccess.SetToCurrentTime()
	}
}

func (m *Metrics) observeOutcome(o booking.Outcome, weekday, class string) {
	if m == nil {
		return
	}
	m.outcomes.WithLabelValues(
		o.Status,
		metrics.Bounded(weekday, knownWeekdays),
		metrics.Bounded(class, knownClasses),
	).Inc()

	if !o.OK() {
		m.failures.WithLabelValues(failureReason(o.Err)).Inc()
	}
}

// observeRunFailure records a run that never reached its classes — a login that
// failed, a password that would not decrypt — against every booking it was
// supposed to make, so the failure counter does not undercount.
func (m *Metrics) observeRunFailure(err error, bookings int) {
	if m == nil {
		return
	}
	reason := failureReason(err)
	for range bookings {
		m.failures.WithLabelValues(reason).Inc()
	}
}

var knownWeekdays = map[string]bool{
	"Monday": true, "Tuesday": true, "Wednesday": true, "Thursday": true,
	"Friday": true, "Saturday": true, "Sunday": true,
}

// failureReason maps an error onto the handful of labels worth alerting on
// separately. "full" is the gym being busy and needs nobody woken up; the rest
// mean something is wrong with the bot, the account or the site.
func failureReason(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, wodbuster.ErrClassFull):
		return "full"
	case errors.Is(err, wodbuster.ErrQuotaExceeded):
		return "quota"
	case errors.Is(err, wodbuster.ErrNotIncluded):
		return "not_included"
	case errors.Is(err, wodbuster.ErrBusy):
		return "busy"
	case errors.Is(err, wodbuster.ErrSessionExpired):
		return "session_expired"
	case errors.Is(err, wodbuster.ErrClassNotFound):
		return "class_not_found"
	default:
		return failureReasonByText(err)
	}
}

func failureReasonByText(err error) string {
	switch msg := err.Error(); {
	case contains(msg, "never published"):
		return "not_published"
	case contains(msg, "login failed"), contains(msg, "browserauth"):
		return "login"
	case contains(msg, "context deadline exceeded"), contains(msg, "context canceled"):
		return "timeout"
	default:
		return "error"
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
