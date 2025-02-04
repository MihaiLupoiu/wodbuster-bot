package main

import (
	"flag"
	"log"
	"os"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/app"
)

func main() {
	envFile := flag.String("e", "", "Path to environment file")
	flag.Parse()

	app, err := app.Initialize(*envFile)
	if err != nil {
		log.Fatal(err)
	}

	if err := app.Execute(); err != nil {
		os.Exit(1)
	}
}
