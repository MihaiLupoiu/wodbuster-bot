// Package metrics configures Prometheus for the process.
//
// It owns one registry and the endpoint that serves it, and nothing else: the
// metrics themselves are defined by the packages that record them, which take
// the registerer and build their own collectors with promauto.With. That is the
// shape buying-engine-service and ssp-service use, and the reason is cohesion —
// a counter about Telegram commands belongs next to the code handling Telegram
// commands, not in a catalogue the whole service has to edit.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Namespace prefixes every metric in this process.
const Namespace = "wodbuster_bot"

// PrometheusMetrics is the process's registry plus the handler that exposes it.
type PrometheusMetrics struct {
	Handler http.Handler
	reg     *prometheus.Registry
}

// PrometheusOpt configures PrometheusMetrics.
type PrometheusOpt func(*PrometheusMetrics)

// WithoutRuntimeCollectors leaves out the Go and process collectors. Only a
// test that wants to assert on an empty registry should need it.
func WithoutRuntimeCollectors() PrometheusOpt {
	return func(p *PrometheusMetrics) {
		p.reg.Unregister(collectors.NewGoCollector())
		p.reg.Unregister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	}
}

// NewPrometheus builds the registry, with the Go runtime and process collectors
// already in it.
func NewPrometheus(opts ...PrometheusOpt) *PrometheusMetrics {
	reg := prometheus.NewRegistry()

	reg.MustRegister(collectors.NewGoCollector(
		collectors.WithGoCollectorRuntimeMetrics(
			collectors.MetricsGC,
			collectors.MetricsScheduler,
		),
	))
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	p := &PrometheusMetrics{
		Handler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}),
		reg:     reg,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Registry is what a package registers its own metrics against:
//
//	botMetrics := telegram.NewMetrics(prom.Registry())
//
// It is a *prometheus.Registry rather than the Registerer interface because
// callers also need it to collect, and tests to assert.
func (p *PrometheusMetrics) Registry() *prometheus.Registry { return p.reg }

// SetBuildInfo publishes which build is running, as the conventional always-1
// gauge. It is the first question of any incident and costs one series.
func (p *PrometheusMetrics) SetBuildInfo(version, goVersion string) {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: Namespace,
		Name:      "build_info",
		Help:      "Always 1. The labels carry the build.",
	}, []string{"version", "go_version"})
	p.reg.MustRegister(g)
	g.WithLabelValues(version, goVersion).Set(1)
}

// RegisterCollectors adds collectors a package built itself — a driver's own
// exporter, say — to the same registry.
func (p *PrometheusMetrics) RegisterCollectors(cs ...prometheus.Collector) {
	p.reg.MustRegister(cs...)
}
