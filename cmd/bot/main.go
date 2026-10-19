package main

import (
	"flag"
	"log/slog"
	"os"

	// Zones baked into the binary: the opening is a wall-clock time in Madrid,
	// and the alpine image the bot ships in has no tzdata.
	_ "time/tzdata"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/app"
)

func main() {
	var envFile string
	flag.StringVar(&envFile, "env", "", "Path to environment file")
	flag.Parse()

	app, err := app.Initialize(envFile)
	if err != nil {
		// Through slog, not log.Fatal: by now the default logger redacts the
		// bot token, and a failed Telegram handshake quotes it in its error.
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}

	if err := app.Execute(); err != nil {
		os.Exit(1)
	}
}
