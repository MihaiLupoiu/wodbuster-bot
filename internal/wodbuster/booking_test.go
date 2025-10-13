package wodbuster

import (
	"context"
	"testing"
)

func TestBookClass(t *testing.T) {
	t.Skip("Skipping live booking test - uncomment to test manually")

	user, pass := getTestCredentials(t)
	client := setupTestClient(t)
	defer client.Close()

	// Book a Wod class on Wednesday at 19:30
	// Can use constants (converted to strings) or plain strings
	err := client.BookClass(context.Background(), user, pass, string(DayWednesday), string(ClassTypeWod), "19:30")
	if err != nil {
		t.Logf("Error booking class: %v", err)
	} else {
		t.Log("Successfully booked class!")
	}

	// Alternative: using plain strings directly
	// err := client.BookClass(context.Background(), user, pass, "X", "Wod", "19:30")
}

// func TestRemoveBooking(t *testing.T) {
// 	client := setupTestClient(t)
// 	defer client.Close()
//
// 	err := client.RemoveBooking("Monday", "10:00")
// 	assert.Error(t, err)
// 	assert.Contains(t, err.Error(), "failed to navigate")
// }
