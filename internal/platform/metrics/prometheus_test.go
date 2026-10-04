package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/platform/metrics"
)

// The platform owns the registry; a package registers its own metrics against
// it and they appear on the one endpoint.
func TestRegistryTakesAPackagesOwnMetrics(t *testing.T) {
	p := metrics.NewPrometheus()

	counter := prom.NewCounter(prom.CounterOpts{
		Namespace: metrics.Namespace,
		Name:      "example_total",
		Help:      "An example.",
	})
	p.RegisterCollectors(counter)
	counter.Inc()

	rec := httptest.NewRecorder()
	p.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "wodbuster_bot_example_total 1")
	assert.Contains(t, rec.Body.String(), "go_goroutines", "runtime collectors are in by default")
	assert.Contains(t, rec.Body.String(), "process_resident_memory_bytes")
}

// Two processes' worth of registries never share state: nothing here is global.
func TestRegistriesAreIndependent(t *testing.T) {
	a, b := metrics.NewPrometheus(), metrics.NewPrometheus()

	c := prom.NewCounter(prom.CounterOpts{Name: "only_in_a_total", Help: "."})
	a.RegisterCollectors(c)

	assert.NotPanics(t, func() {
		b.RegisterCollectors(prom.NewCounter(prom.CounterOpts{Name: "only_in_a_total", Help: "."}))
	}, "the same metric name in another registry must not collide")
}

func TestBoundedKeepsLabelsFinite(t *testing.T) {
	allowed := map[string]bool{"book": true, "status": true}

	for in, want := range map[string]string{
		"book":   "book",
		"status": "status",
		"":       metrics.LabelNone,
		"asdf":   metrics.LabelOthers,
		"Book":   metrics.LabelOthers,
	} {
		assert.Equal(t, want, metrics.Bounded(in, allowed), "Bounded(%q)", in)
	}
}
