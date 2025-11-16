package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/wodbuster"
	"github.com/joho/godotenv"
)

func main() {

	// Try to load from current directory first
	if err := godotenv.Load(); err != nil {
		// If failed, try to load from project root
		projectRoot := filepath.Join("..", "..")
		if err := godotenv.Load(filepath.Join(projectRoot, ".env")); err != nil {
			// Log the error but don't fail - env vars might be set in the environment
			slog.Info("Error loading .env file", "error", err)
		}
	}

	const testBaseURL = "https://firespain.wodbuster.com"
	user := os.Getenv("TEST_EMAIL")
	pass := os.Getenv("TEST_PASSWORD")

	client, err := wodbuster.NewClient(testBaseURL, wodbuster.WithHeadlessMode(false))
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	defer client.Close()

	err = client.LoginOnly(user, pass)
	if err != nil {
		log.Fatalf("Failed to login: %v", err)
	}

	err = client.NotRememberBrowser()
	if err != nil {
		log.Fatalf("Failed to not remember browser: %v", err)
	}

	classes, err := client.GetAvailableClassesOnly("L")
	if err != nil {
		log.Fatalf("Failed to get available classes: %v", err)
	}
	fmt.Println(classes)

	err = client.BookClassOnly("L", "Wod", "07:00")
	if err != nil {
		log.Fatalf("Failed to book class: %v", err)
	}

	// ===============================
	classes, err = client.GetAvailableClassesOnly("X")
	if err != nil {
		log.Fatalf("Failed to get available classes: %v", err)
	}
	fmt.Println(classes)

	err = client.BookClassOnly("X", "Wod", "07:00")
	if err != nil {
		log.Fatalf("Failed to book class: %v", err)
	}

	// ===============================

	classes, err = client.GetAvailableClassesOnly("V")
	if err != nil {
		log.Fatalf("Failed to get available classes: %v", err)
	}
	fmt.Println(classes)

	err = client.BookClassOnly("V", "Wod", "07:00")
	if err != nil {
		log.Fatalf("Failed to book class: %v", err)
	}

	// ===============================

	time.Sleep(60 * time.Minute)

	os.Exit(0)

	// ===============================

	// cookie, err := client.LogIn(context.Background(), user, pass)
	// if err != nil {
	// 	log.Fatalf("Failed to login: %v", err)
	// }
	// fmt.Println(cookie)

	// TEST 1: Book a class on Wednesday at 20:30
	// go func() {
	// 	client, err := wodbuster.NewClient(testBaseURL, wodbuster.WithHeadlessMode(false))
	// 	if err != nil {
	// 		log.Fatalf("Failed to create client: %v", err)
	// 	}

	// 	defer client.Close()

	// 	err = client.BookClass(context.Background(), user, pass, "X", "Wod", "20:30")
	// 	if err != nil {
	// 		log.Fatalf("Failed to book class: %v", err)
	// 	}
	// }()

	// TEST 2: Book a class on Monday at 07:00
	go func() {
		client, err := wodbuster.NewClient(testBaseURL, wodbuster.WithHeadlessMode(false))
		if err != nil {
			log.Fatalf("Failed to create client: %v", err)
		}

		defer client.Close()

		err = client.BookClass(context.Background(), user, pass, "L", "Wod", "07:00")
		if err != nil {
			log.Fatalf("Failed to book class: %v", err)
		}
	}()

	// TEST 3: Book a class on Wednesday at 07:00
	go func() {
		client, err := wodbuster.NewClient(testBaseURL, wodbuster.WithHeadlessMode(false))
		if err != nil {
			log.Fatalf("Failed to create client: %v", err)
		}

		defer client.Close()

		err = client.BookClass(context.Background(), user, pass, "X", "Wod", "07:00")
		if err != nil {
			log.Fatalf("Failed to book class: %v", err)
		}
	}()

	// TEST 4: Book a class on Friday at 07:00
	go func() {
		client, err := wodbuster.NewClient(testBaseURL, wodbuster.WithHeadlessMode(false))
		if err != nil {
			log.Fatalf("Failed to create client: %v", err)
		}

		defer client.Close()

		err = client.BookClass(context.Background(), user, pass, "V", "Wod", "07:00")
		if err != nil {
			log.Fatalf("Failed to book class: %v", err)
		}
	}()

	time.Sleep(30 * time.Minute)
}
