package usecase

import (
	"errors"
	"strconv"
	"strings"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/booking"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
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

	loginChecks          *prom.CounterVec
	loginCheckLastPassed prom.Gauge

	sessionChecks        *prom.CounterVec
	sessionAgeAtFailure  *prom.HistogramVec
	sessionIdleAtFailure *prom.HistogramVec
	sessionOldestLogin   prom.Gauge
	sessionAuthExpiry    *prom.GaugeVec
	sessionsStored       prom.Gauge
	refreshLastSuccess   prom.Gauge
	reuseMode            *prom.GaugeVec
	sessionReuse         *prom.CounterVec
	preflights           *prom.CounterVec

	// authExpiryChats are the chat ids sessionAuthExpiry has a series for,
	// so that stale ones can go without a Reset that empties the gauge.
	authExpiryChats map[string]bool
}

// sessionLifetimeBuckets run from an hour to the 45 days a remembered
// .WBAuth lasts: the range a session's real lifetime falls in. A function, so
// no caller can change the buckets another one gets.
func sessionLifetimeBuckets() []float64 {
	return []float64{
		3600, 6 * 3600, 12 * 3600, 86400, 2 * 86400, 3 * 86400,
		7 * 86400, 14 * 86400, 30 * 86400, 45 * 86400,
	}
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

		loginChecks: f.NewCounterVec(prom.CounterOpts{
			Namespace: ns, Name: "login_checks_total",
			Help: "Daily login checks, by result: passed, failed, skipped.",
		}, []string{"result"}),

		sessionChecks: f.NewCounterVec(prom.CounterOpts{
			Namespace: ns, Name: "session_checks_total",
			Help: "Every check of a stored WodBuster session, by where it ran (refresh, " +
				"preflight) and result (ok, expired, challenge, inconclusive, unreadable).",
		}, []string{"source", "result"}),

		sessionAgeAtFailure: f.NewHistogramVec(prom.HistogramOpts{
			Namespace: ns, Name: "session_age_at_failure_seconds",
			Help: "How long after its login a stored session was refused. This is the " +
				"measured lifetime of a session.",
			Buckets: sessionLifetimeBuckets(),
		}, []string{"source", "reason"}),

		sessionIdleAtFailure: f.NewHistogramVec(prom.HistogramOpts{
			Namespace: ns, Name: "session_idle_at_failure_seconds",
			Help:    "How long a refused session had gone unused: the idle gap it did not survive.",
			Buckets: sessionLifetimeBuckets(),
		}, []string{"source", "reason"}),

		// Timestamps rather than ages: an age is computed when the gauge is
		// set and freezes when nothing sets it again, which is exactly when
		// the refresh has stopped. time() minus a timestamp keeps moving.
		sessionOldestLogin: f.NewGauge(prom.GaugeOpts{
			Namespace: ns, Name: "session_oldest_login_timestamp_seconds",
			Help: "Unix time of the login of the oldest stored session of an active athlete. " +
				"time() minus this is how long sessions really live. 0 with no session stored.",
		}),

		sessionAuthExpiry: f.NewGaugeVec(prom.GaugeOpts{
			Namespace: ns, Name: "session_auth_expiry_timestamp_seconds",
			Help: "Unix time each stored session's .WBAuth cookie expires. Absent for a session " +
				"cookie. Under a week away means the refresh has stopped renewing it.",
		}, []string{"chat_id"}),

		sessionsStored: f.NewGauge(prom.GaugeOpts{
			Namespace: ns, Name: "sessions_stored",
			Help: "Active athletes with a stored session.",
		}),

		refreshLastSuccess: f.NewGauge(prom.GaugeOpts{
			Namespace: ns, Name: "session_refresh_last_success_timestamp_seconds",
			Help: "Unix time of the last session refresh that went through every athlete. " +
				"time() minus this past a day means stored sessions are no longer kept alive.",
		}),

		reuseMode: f.NewGaugeVec(prom.GaugeOpts{
			Namespace: ns, Name: "session_reuse_mode",
			Help: "1 for the configured SESSION_REUSE (off, shadow). The session alerts " +
				"join on it, so that off, which keeps no sessions, sets none of them off.",
		}, []string{"mode"}),

		sessionReuse: f.NewCounterVec(prom.CounterOpts{
			Namespace: ns, Name: "session_reuse_total",
			Help: "Sunday results for stored sessions, per athlete, by phase (shadow, live) " +
				"and result: would_work, would_fail, inconclusive, missing (the preflight's " +
				"shadow check), used, fallback (the run).",
		}, []string{"phase", "result"}),

		preflights: f.NewCounterVec(prom.CounterOpts{
			Namespace: ns, Name: "preflight_total",
			Help: "Sunday session preflights, by result: all_ok, partial, failed, nobody (no athlete had classes).",
		}, []string{"result"}),

		loginCheckLastPassed: f.NewGauge(prom.GaugeOpts{
			Namespace: ns, Name: "login_check_last_success_timestamp_seconds",
			Help: "Unix time of the last daily login check that logged in. time() minus " +
				"this past a day and a bit means either the login is broken or the " +
				"check is not running; both need a look before Sunday.",
		}),
	}
}

func (m *Metrics) observeLoginCheck(result string) {
	if m == nil {
		return
	}
	m.loginChecks.WithLabelValues(result).Inc()
	if result == "passed" {
		m.loginCheckLastPassed.SetToCurrentTime()
	}
}

func (m *Metrics) observeSessionCheck(source, result string) {
	if m == nil {
		return
	}
	m.sessionChecks.WithLabelValues(source, result).Inc()
}

func (m *Metrics) observeSessionRefused(source, reason string, age, idle time.Duration) {
	if m == nil {
		return
	}
	m.sessionAgeAtFailure.WithLabelValues(source, reason).Observe(age.Seconds())
	m.sessionIdleAtFailure.WithLabelValues(source, reason).Observe(idle.Seconds())
}

func (m *Metrics) observeSessionReuse(phase ReuseMode, result string) {
	if m == nil {
		return
	}
	m.sessionReuse.WithLabelValues(string(phase), result).Inc()
}

func (m *Metrics) observePreflight(result string) {
	if m == nil {
		return
	}
	m.preflights.WithLabelValues(result).Inc()
}

// setStoredSessions replaces the session gauges with what is stored now.
// Callers serialise it.
func (m *Metrics) setStoredSessions(stored map[int64]models.StoredSession) {
	if m == nil {
		return
	}
	m.sessionsStored.Set(float64(len(stored)))
	var oldest time.Time
	chats := make(map[string]bool, len(stored))
	for chatID, st := range stored {
		if oldest.IsZero() || st.LoggedInAt.Before(oldest) {
			oldest = st.LoggedInAt
		}
		if !st.AuthExpiresAt.IsZero() {
			label := strconv.FormatInt(chatID, 10)
			chats[label] = true
			m.sessionAuthExpiry.WithLabelValues(label).Set(float64(st.AuthExpiresAt.Unix()))
		}
	}
	for label := range m.authExpiryChats {
		if !chats[label] {
			m.sessionAuthExpiry.DeleteLabelValues(label)
		}
	}
	m.authExpiryChats = chats
	if oldest.IsZero() {
		m.sessionOldestLogin.Set(0)
	} else {
		m.sessionOldestLogin.Set(float64(oldest.Unix()))
	}
}

// observeRefreshDone records a refresh that went through every athlete.
func (m *Metrics) observeRefreshDone(at time.Time) {
	if m == nil {
		return
	}
	m.refreshLastSuccess.Set(float64(at.Unix()))
}

// setReuseMode records the configured SESSION_REUSE.
func (m *Metrics) setReuseMode(mode ReuseMode) {
	if m == nil {
		return
	}
	m.reuseMode.Reset()
	m.reuseMode.WithLabelValues(string(mode)).Set(1)
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
	case errors.Is(err, wodbuster.ErrChallenge):
		return "challenge"
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
