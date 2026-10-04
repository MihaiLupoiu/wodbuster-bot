package telegram

import (
	"strings"
	"testing"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const commandsHelp = `
# HELP wodbuster_bot_commands_total Total number of Telegram commands received, by command. Unknown commands are bucketed under "(others)", and a message carrying no command at all under "(none)".
# TYPE wodbuster_bot_commands_total counter
`

func TestObserveCommandCountsPerCommand(t *testing.T) {
	reg := prom.NewRegistry()
	m := NewMetrics(reg)

	m.ObserveCommand("book")
	m.ObserveCommand("book")
	m.ObserveCommand("status")

	expected := commandsHelp + `wodbuster_bot_commands_total{command="book"} 2
wodbuster_bot_commands_total{command="status"} 1
`
	require.NoError(t, testutil.CollectAndCompare(
		reg, strings.NewReader(expected), "wodbuster_bot_commands_total"))
}

// Anyone can type /asdf, and every distinct label value is a series Prometheus
// keeps. Only the commands the bot implements get one of their own.
func TestUnknownCommandsAreBucketed(t *testing.T) {
	reg := prom.NewRegistry()
	m := NewMetrics(reg)

	m.ObserveCommand("asdf")
	m.ObserveCommand("alsoNotACommand")
	m.ObserveCommand("") // plain text, no command at all

	expected := commandsHelp + `wodbuster_bot_commands_total{command="(none)"} 1
wodbuster_bot_commands_total{command="(others)"} 2
`
	require.NoError(t, testutil.CollectAndCompare(
		reg, strings.NewReader(expected), "wodbuster_bot_commands_total"))
	assert.Equal(t, 2, testutil.CollectAndCount(reg, "wodbuster_bot_commands_total"),
		"three distinct inputs, two series")
}

// The allow-list has to stay in step with the switch in handleUpdate: a command
// the bot answers but does not count is invisible in the dashboards.
func TestEveryImplementedCommandIsCounted(t *testing.T) {
	for _, cmd := range []string{
		"start", "login", "book", "status", "test", "active", "schedule", "rehearse", "help",
	} {
		assert.Equal(t, cmd, commandLabel(cmd),
			"%q is handled in handleUpdate but missing from knownCommands", cmd)
	}
}

// A bot built without metrics holds a nil *Metrics.
func TestNilMetricsCountsNothing(t *testing.T) {
	var m *Metrics
	assert.NotPanics(t, func() { m.ObserveCommand("book") })
}
