package wodbuster

import (
	"testing"
)

func TestGetAvailableClasses(t *testing.T) {
	user, pass := getTestCredentials(t)
	client := setupTestClient(t)
	defer client.Close()

	// Test getting classes for a specific day (Monday)
	// Can use either string or constant
	classes, err := client.GetAvailableClasses(user, pass, string(DayMonday))
	if err != nil {
		t.Logf("Error getting available classes: %v", err)
	}

	// Log the classes found
	if len(classes) > 0 {
		t.Logf("Found %d classes:", len(classes))
		for _, class := range classes {
			t.Logf("  - %s at %s (Available: %v)", class.ClassType, class.Hour, class.Available)
		}
	}
}
