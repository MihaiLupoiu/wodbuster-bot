package main

import (
	"log"
	"os"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/app"
)

func main() {
	config, err := app.NewConfig()
	if err != nil {
		log.Fatal(err)
	}

	application, err := app.New(config)
	if err != nil {
		log.Fatal(err)
	}

	if err := application.Execute(); err != nil {
		os.Exit(1)
	}
}
