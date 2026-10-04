package telegram

import (
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/platform/metrics"
)

const labelCommand = "command"

// knownCommands is every command the bot implements, and the allow-list that
// keeps the command label bounded: the value comes from a Telegram message, so
// anyone can invent one, and each distinct value is a series Prometheus keeps.
// It lives here, beside the switch in handleUpdate, so that adding a command
// and forgetting its metric is a one-file mistake rather than a two-package one.
var knownCommands = map[string]bool{
	"start":    true,
	"login":    true,
	"book":     true,
	"status":   true,
	"test":     true,
	"active":   true,
	"schedule": true,
	"rehearse": true,
	"help":     true,
}

// Metrics is this package's Prometheus surface. Build it with the registry the
// platform owns, and hand it to the bot:
//
//	bot.WithMetrics(telegram.NewMetrics(prom.Registry()))
type Metrics struct {
	commandsTotal   *prom.CounterVec
	commandDuration *prom.HistogramVec
	updatesTotal    prom.Counter
	lastUpdate      prom.Gauge
	messagesSent    *prom.CounterVec
}

// NewMetrics registers the bot's metrics against reg.
func NewMetrics(reg prom.Registerer) *Metrics {
	factory := promauto.With(reg)

	return &Metrics{
		commandsTotal: factory.NewCounterVec(
			prom.CounterOpts{
				Namespace: metrics.Namespace,
				Name:      "commands_total",
				Help: "Total number of Telegram commands received, by command. " +
					"Unknown commands are bucketed under \"(others)\", and a message " +
					"carrying no command at all under \"(none)\".",
			},
			[]string{labelCommand},
		),

		commandDuration: factory.NewHistogramVec(
			prom.HistogramOpts{
				Namespace: metrics.Namespace,
				Name:      "command_duration_seconds",
				Help: "How long a command took to handle. Most are instant; " +
					"/login and /rehearse drive a browser.",
				Buckets: []float64{0.01, 0.1, 0.5, 1, 2, 5, 10, 20, 45, 90},
			},
			[]string{labelCommand},
		),

		updatesTotal: factory.NewCounter(prom.CounterOpts{
			Namespace: metrics.Namespace,
			Name:      "updates_total",
			Help:      "Telegram updates received.",
		}),

		lastUpdate: factory.NewGauge(prom.GaugeOpts{
			Namespace: metrics.Namespace,
			Name:      "last_update_timestamp_seconds",
			Help: "Unix time of the last update received. A stalled long-poll — " +
				"\"getUpdates: unexpected EOF\" and no reconnect — leaves the bot " +
				"deaf with nothing in the logs, and this is how that looks.",
		}),

		messagesSent: factory.NewCounterVec(prom.CounterOpts{
			Namespace: metrics.Namespace,
			Name:      "messages_sent_total",
			Help: "Replies sent, by result. A booking that succeeds but cannot be " +
				"reported is, to the athlete, a booking that did not happen.",
		}, []string{"result"}),
	}
}

// ObserveUpdate counts one update arriving from Telegram.
func (m *Metrics) ObserveUpdate() {
	if m == nil {
		return
	}
	m.updatesTotal.Inc()
	m.lastUpdate.SetToCurrentTime()
}

// ObserveCommandDuration records how long a command took to handle.
func (m *Metrics) ObserveCommandDuration(command string, d time.Duration) {
	if m == nil {
		return
	}
	m.commandDuration.WithLabelValues(commandLabel(command)).Observe(d.Seconds())
}

// ObserveMessageSent counts one reply.
func (m *Metrics) ObserveMessageSent(err error) {
	if m == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	m.messagesSent.WithLabelValues(result).Inc()
}

// ObserveCommand counts one received command. A nil *Metrics counts nothing, so
// a bot built without metrics needs no branch.
func (m *Metrics) ObserveCommand(command string) {
	if m == nil {
		return
	}
	m.commandsTotal.With(prom.Labels{labelCommand: commandLabel(command)}).Inc()
}

func commandLabel(command string) string {
	return metrics.Bounded(command, knownCommands)
}
