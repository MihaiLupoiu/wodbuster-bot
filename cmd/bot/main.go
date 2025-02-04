package main

import (
	"log"
	"os"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/app"
)

func main() {
	app, err := app.Initialize()
	if err != nil {
		log.Fatal(err)
	}

	if err := app.Execute(); err != nil {
		os.Exit(1)
	}
}
