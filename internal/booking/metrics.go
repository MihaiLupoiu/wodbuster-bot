package booking

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/platform/metrics"
)

// Buckets chosen from what the real runs do, not from habit.
var (
	// A login drives a browser: 3-15s is normal, and the interesting question
	// is whether it has started creeping towards the 90s timeout.
	loginBuckets = []float64{1, 2, 3, 5, 7.5, 10, 15, 20, 30, 45, 60, 90}

	// The race is decided inside the first second or two. Observed bookings
	// have landed at 497ms, 520ms and 833ms past the opening, so the buckets
	// have to be fine down there and are useless above a few seconds.
	bookingLatencyBuckets = []float64{0.25, 0.5, 0.75, 1, 1.5, 2, 3, 5, 10, 30, 90}

	// WodBuster's own handlers answer in tens of milliseconds when idle and
	// hundreds at an opening.
	apiBuckets = []float64{0.01, 0.025, 0.05, 0.1, 0.2, 0.3, 0.5, 1, 2, 5, 10}
)

// knownEndpoints keeps the endpoint label finite. The path carries ids and
// ticks, so the raw value would be unbounded.
var knownEndpoints = map[string]bool{
	"LoadClass":            true,
	"Calendario_Inscribir": true,
	"Calendario_Avisar":    true,
	"Calendario_Borrar":    true,
	"reservas":             true,
}

// Metrics covers everything this package does against WodBuster: logging in,
// the requests that follow, and how the chase ends.
type Metrics struct {
	loginTotal      *prom.CounterVec
	loginDuration   prom.Histogram
	controlDrift    *prom.CounterVec
	apiRequests     *prom.CounterVec
	apiDuration     *prom.HistogramVec
	clockOffset     prom.Gauge
	bookingLatency  prom.Histogram
	bookingAttempts prom.Histogram
	dayUnpublished  *prom.CounterVec
}

func NewMetrics(reg prom.Registerer) *Metrics {
	f := promauto.With(reg)
	ns := metrics.Namespace

	return &Metrics{
		loginTotal: f.NewCounterVec(prom.CounterOpts{
			Namespace: ns, Name: "login_total",
			Help: "Logins attempted, by result.",
		}, []string{"result"}),

		loginDuration: f.NewHistogram(prom.HistogramOpts{
			Namespace: ns, Name: "login_duration_seconds",
			Help:    "How long a login takes, browser included.",
			Buckets: loginBuckets,
		}),

		controlDrift: f.NewCounterVec(prom.CounterOpts{
			Namespace: ns, Name: "login_control_drift_total",
			Help: "Times a login control was found by something other than its known id, " +
				"by control and by the route that found it. Any increase means WodBuster " +
				"moved something and the ids need updating.",
		}, []string{"control", "route"}),

		apiRequests: f.NewCounterVec(prom.CounterOpts{
			Namespace: ns, Name: "api_requests_total",
			Help: "Requests to WodBuster, by endpoint and HTTP status.",
		}, []string{"endpoint", "status"}),

		apiDuration: f.NewHistogramVec(prom.HistogramOpts{
			Namespace: ns, Name: "api_duration_seconds",
			Help:    "WodBuster response time, by endpoint.",
			Buckets: apiBuckets,
		}, []string{"endpoint"}),

		clockOffset: f.NewGauge(prom.GaugeOpts{
			Namespace: ns, Name: "server_clock_offset_seconds",
			Help: "Difference between WodBuster's clock and ours, as last measured. " +
				"We book against theirs, so a drifting offset means our timing is wrong.",
		}),

		bookingLatency: f.NewHistogram(prom.HistogramOpts{
			Namespace: ns, Name: "booking_latency_seconds",
			Help:    "Time from the opening to the place being taken.",
			Buckets: bookingLatencyBuckets,
		}),

		bookingAttempts: f.NewHistogram(prom.HistogramOpts{
			Namespace: ns, Name: "booking_attempts",
			Help:    "Booking calls made per class before it resolved.",
			Buckets: []float64{1, 2, 3, 5, 10, 25, 50, 100},
		}),

		dayUnpublished: f.NewCounterVec(prom.CounterOpts{
			Namespace: ns, Name: "day_unpublished_total",
			Help: "Chases that ended because the day never appeared, by weekday. " +
				"A box that is closed that day looks the same as one that has not " +
				"published yet, so this is read together with the weekday.",
		}, []string{"weekday"}),
	}
}

func (m *Metrics) observeLogin(result string, d time.Duration) {
	if m == nil {
		return
	}
	m.loginTotal.WithLabelValues(result).Inc()
	m.loginDuration.Observe(d.Seconds())
}

func (m *Metrics) observeDrift(control, via string) {
	if m == nil {
		return
	}
	m.controlDrift.WithLabelValues(control, via).Inc()
}

func (m *Metrics) observeClockOffset(d time.Duration) {
	if m == nil {
		return
	}
	m.clockOffset.Set(d.Seconds())
}

func (m *Metrics) observeBookingLatency(d time.Duration) {
	if m == nil {
		return
	}
	m.bookingLatency.Observe(d.Seconds())
}

func (m *Metrics) observeAttempts(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.bookingAttempts.Observe(float64(n))
}

func (m *Metrics) observeUnpublished(weekday time.Weekday) {
	if m == nil {
		return
	}
	m.dayUnpublished.WithLabelValues(weekday.String()).Inc()
}

// loginResult turns an authentication error into one of a handful of labels.
func loginResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case strings.Contains(err.Error(), "rejected the credentials"):
		return "rejected"
	case errors.Is(err, context.DeadlineExceeded),
		strings.Contains(err.Error(), "did not finish in time"),
		strings.Contains(err.Error(), "deadline exceeded"):
		return "timeout"
	default:
		return "error"
	}
}

// transport records every request the client makes. It wraps the client's
// RoundTripper rather than reaching into pkg/wodbuster, which has no business
// knowing about Prometheus.
type transport struct {
	next http.RoundTripper
	m    *Metrics
}

func (t *transport) RoundTrip(r *http.Request) (*http.Response, error) {
	started := time.Now()
	resp, err := t.next.RoundTrip(r)
	elapsed := time.Since(started)

	endpoint := endpointLabel(r.URL.Path)
	status := "error"
	if err == nil {
		status = statusLabel(resp.StatusCode)
	}
	t.m.apiRequests.WithLabelValues(endpoint, status).Inc()
	t.m.apiDuration.WithLabelValues(endpoint).Observe(elapsed.Seconds())
	return resp, err
}

// endpointLabel reduces a path to the handler that served it: the path carries
// a class id and a ticks value, which would be a new series every time.
func endpointLabel(path string) string {
	base := path
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".ashx")
	base = strings.TrimSuffix(base, ".aspx")
	return metrics.Bounded(base, knownEndpoints)
}

func statusLabel(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	default:
		return "5xx"
	}
}

// httpClientWith returns a client whose requests are counted. Nil metrics means
// the library's own default client, untouched.
func (m *Metrics) httpClientWith(base *http.Client) *http.Client {
	if m == nil {
		return base
	}
	c := *base
	next := c.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	c.Transport = &transport{next: next, m: m}
	return &c
}
