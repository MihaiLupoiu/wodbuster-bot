package booking

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The path carries a class id and a ticks value, so the raw path would be a new
// series on every request.
func TestEndpointLabel(t *testing.T) {
	for path, want := range map[string]string{
		"/athlete/handlers/LoadClass.ashx":            "LoadClass",
		"/athlete/handlers/Calendario_Inscribir.ashx": "Calendario_Inscribir",
		"/athlete/handlers/Calendario_Avisar.ashx":    "Calendario_Avisar",
		"/athlete/reservas.aspx":                      "reservas",
		"/athlete/handlers/SomethingNew.ashx":         "(others)",
		"/":                                           "(none)",
	} {
		assert.Equal(t, want, endpointLabel(path), "endpointLabel(%q)", path)
	}
}

func TestLoginResult(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"success":    {nil, "ok"},
		"bad creds":  {errors.New("browserauth: WodBuster rejected the credentials"), "rejected"},
		"slow":       {errors.New("browserauth: login did not finish in time (wrong credentials?)"), "timeout"},
		"cancelled":  {context.DeadlineExceeded, "timeout"},
		"no browser": {errors.New("exec: \"chromium\": executable file not found"), "error"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, loginResult(tc.err))
		})
	}
}

// The transport counts what the client actually sends, without pkg/wodbuster
// knowing Prometheus exists.
func TestTransportCountsRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/athlete/handlers/Calendario_Inscribir.ashx" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := prom.NewRegistry()
	m := NewMetrics(reg)
	client := m.httpClientWith(&http.Client{Timeout: 5 * time.Second})

	for _, p := range []string{
		"/athlete/handlers/LoadClass.ashx?ticks=1&idu=x",
		"/athlete/handlers/LoadClass.ashx?ticks=2&idu=x",
		"/athlete/handlers/Calendario_Inscribir.ashx?id=7",
	} {
		resp, err := client.Get(srv.URL + p)
		require.NoError(t, err)
		resp.Body.Close()
	}

	assert.Equal(t, 2.0, testutil.ToFloat64(m.apiRequests.WithLabelValues("LoadClass", "2xx")),
		"the ticks value must not split the series")
	assert.Equal(t, 1.0, testutil.ToFloat64(m.apiRequests.WithLabelValues("Calendario_Inscribir", "5xx")))
	assert.Equal(t, 2, testutil.CollectAndCount(reg, "wodbuster_bot_api_duration_seconds"))
}

// A transport error has no status code and must still be counted.
func TestTransportCountsFailures(t *testing.T) {
	reg := prom.NewRegistry()
	m := NewMetrics(reg)
	client := m.httpClientWith(&http.Client{Timeout: 100 * time.Millisecond})

	_, err := client.Get("http://127.0.0.1:1/athlete/handlers/LoadClass.ashx")
	require.Error(t, err)
	assert.Equal(t, 1.0, testutil.ToFloat64(m.apiRequests.WithLabelValues("LoadClass", "error")))
}

// Nil metrics is a service built without them.
func TestNilMetricsAreInert(t *testing.T) {
	var m *Metrics
	assert.NotPanics(t, func() {
		m.observeLogin("ok", time.Second)
		m.observeDrift("device", "label")
		m.observeClockOffset(time.Second)
		m.observeBookingLatency(time.Second)
		m.observeAttempts(3)
		m.observeUnpublished(time.Monday)
		assert.NotNil(t, m.httpClientWith(http.DefaultClient))
	})
}
