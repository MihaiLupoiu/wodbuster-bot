package telegram

import (
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
	commandsTotal *prom.CounterVec
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
	}
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
