package storage

import (
	"context"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.mongodb.org/mongo-driver/v2/event"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/platform/metrics"
)

// mongoBuckets are in milliseconds and sized for a hosted database: Atlas is a
// network hop away, not a container on the same host, so the interesting range
// starts where a local Mongo would already have finished.
var mongoBuckets = []float64{1, 2, 5, 10, 25, 50, 100, 200, 400, 800, 1600}

// MongoMetrics instruments the driver through its own event monitors, the way
// ssp-service does: no call site changes, and every command is covered because
// the driver reports them all.
type MongoMetrics struct {
	connections *prom.GaugeVec
	operations  *prom.CounterVec
	duration    *prom.HistogramVec
	readBytes   *prom.CounterVec
	writeBytes  *prom.CounterVec
}

func NewMongoMetrics(reg prom.Registerer) *MongoMetrics {
	f := promauto.With(reg)
	ns := metrics.Namespace

	return &MongoMetrics{
		connections: f.NewGaugeVec(prom.GaugeOpts{
			Namespace: ns, Name: "mongo_connections",
			Help: "Open Mongo connections. The free Atlas tier caps these, and a leak shows here first.",
		}, []string{"key"}),

		operations: f.NewCounterVec(prom.CounterOpts{
			Namespace: ns, Name: "mongo_operations_total",
			Help: "Mongo commands, by command and result. A paused cluster, an expired " +
				"password and an IP dropped from the access list all look the same " +
				"from here: commands that stop succeeding.",
		}, []string{"cmd", "stat"}),

		duration: f.NewHistogramVec(prom.HistogramOpts{
			Namespace: ns, Name: "mongo_duration_ms",
			Help:    "Mongo command duration in milliseconds.",
			Buckets: mongoBuckets,
		}, []string{"cmd", "stat"}),

		readBytes: f.NewCounterVec(prom.CounterOpts{
			Namespace: ns, Name: "mongo_read_bytes",
			Help: "Bytes received in Mongo replies.",
		}, []string{"cmd"}),

		writeBytes: f.NewCounterVec(prom.CounterOpts{
			Namespace: ns, Name: "mongo_written_bytes",
			Help: "Bytes sent in Mongo commands.",
		}, []string{"cmd"}),
	}
}

// knownCommands bounds the cmd label. The driver reports the command name, so
// the set is finite in practice, but an unexpected one should not add a series.
var knownMongoCommands = map[string]bool{
	"find": true, "insert": true, "update": true, "delete": true,
	"findAndModify": true, "aggregate": true, "count": true, "distinct": true,
	"getMore": true, "ping": true, "hello": true, "ismaster": true,
	"endSessions": true, "killCursors": true, "listIndexes": true,
}

func (m *MongoMetrics) cmd(name string) string {
	return metrics.Bounded(name, knownMongoCommands)
}

// Monitors are what the client is built with. A nil *MongoMetrics returns nils,
// which the driver accepts, so storage without metrics needs no branch.
func (m *MongoMetrics) Monitors() (*event.PoolMonitor, *event.CommandMonitor) {
	if m == nil {
		return nil, nil
	}

	pool := &event.PoolMonitor{
		Event: func(e *event.PoolEvent) {
			switch e.Type {
			case event.ConnectionReady:
				m.connections.WithLabelValues("activeConn").Inc()
			case event.ConnectionClosed:
				m.connections.WithLabelValues("activeConn").Dec()
			}
		},
	}

	cmd := &event.CommandMonitor{
		Started: func(_ context.Context, e *event.CommandStartedEvent) {
			name := m.cmd(e.CommandName)
			m.operations.WithLabelValues(name, "started").Inc()
			m.writeBytes.WithLabelValues(name).Add(float64(len(e.Command)))
		},
		Succeeded: func(_ context.Context, e *event.CommandSucceededEvent) {
			name := m.cmd(e.CommandName)
			m.operations.WithLabelValues(name, "succeeded").Inc()
			m.readBytes.WithLabelValues(name).Add(float64(len(e.Reply)))
			m.observeDuration(name, "succeeded", e.Duration)
		},
		Failed: func(_ context.Context, e *event.CommandFailedEvent) {
			name := m.cmd(e.CommandName)
			m.operations.WithLabelValues(name, "failed").Inc()
			m.observeDuration(name, "failed", e.Duration)
		},
	}

	return pool, cmd
}

func (m *MongoMetrics) observeDuration(cmd, stat string, d time.Duration) {
	m.duration.WithLabelValues(cmd, stat).Observe(float64(d.Milliseconds()))
}
