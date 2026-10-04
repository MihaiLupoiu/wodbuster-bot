package storage

import (
	"context"
	"testing"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/v2/event"
)

// The driver reports every command through these monitors, which is why there
// is nothing to wrap at the call sites.
func TestMongoMonitorsRecordCommands(t *testing.T) {
	reg := prom.NewRegistry()
	m := NewMongoMetrics(reg)
	pool, cmd := m.Monitors()

	pool.Event(&event.PoolEvent{Type: event.ConnectionReady})
	pool.Event(&event.PoolEvent{Type: event.ConnectionReady})
	pool.Event(&event.PoolEvent{Type: event.ConnectionClosed})

	cmd.Started(context.Background(), &event.CommandStartedEvent{CommandName: "find"})
	cmd.Succeeded(context.Background(), &event.CommandSucceededEvent{
		CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "find", Duration: 12 * time.Millisecond},
	})
	cmd.Started(context.Background(), &event.CommandStartedEvent{CommandName: "insert"})
	cmd.Failed(context.Background(), &event.CommandFailedEvent{
		CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "insert", Duration: 50 * time.Millisecond},
	})

	assert.Equal(t, 1.0, testutil.ToFloat64(m.connections.WithLabelValues("activeConn")),
		"two opened, one closed")
	assert.Equal(t, 1.0, testutil.ToFloat64(m.operations.WithLabelValues("find", "succeeded")))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.operations.WithLabelValues("insert", "failed")))
	assert.Equal(t, 2, testutil.CollectAndCount(reg, "wodbuster_bot_mongo_duration_ms"))
}

// A command the driver reports but we did not list must not create a series.
func TestMongoCommandLabelIsBounded(t *testing.T) {
	reg := prom.NewRegistry()
	m := NewMongoMetrics(reg)
	_, cmd := m.Monitors()

	cmd.Started(context.Background(), &event.CommandStartedEvent{CommandName: "somethingNew"})

	assert.Equal(t, 1.0, testutil.ToFloat64(m.operations.WithLabelValues("(others)", "started")))
}

// Storage without metrics passes nil monitors, which the driver accepts.
func TestNilMongoMetricsGivesNilMonitors(t *testing.T) {
	var m *MongoMetrics
	pool, cmd := m.Monitors()
	assert.Nil(t, pool)
	assert.Nil(t, cmd)
}
