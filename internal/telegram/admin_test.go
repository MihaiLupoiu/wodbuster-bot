package telegram

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
)

func user(role, status string) models.User {
	return models.User{ChatID: 7, Email: "a@example.com", Role: role, Status: status}
}

func TestAccessFor(t *testing.T) {
	admin := user(models.RoleAdmin, models.StatusActive)
	active := user(models.RoleUser, models.StatusActive)
	pending := user(models.RoleUser, models.StatusPending)
	off := user(models.RoleUser, models.StatusDisabled)

	for name, tc := range map[string]struct {
		command    string
		user       models.User
		registered bool
		want       access
	}{
		"a stranger can log in":                {"login", models.User{}, false, allowed},
		"a stranger reaches /book's own reply": {"book", models.User{}, false, allowed},
		"pending can log in again":             {"login", pending, true, allowed},
		"pending can see their status":         {"status", pending, true, allowed},
		"pending cannot book":                  {"book", pending, true, notApproved},
		"pending cannot rehearse":              {"rehearse", pending, true, notApproved},
		"disabled cannot book":                 {"book", off, true, disabled},
		"disabled can still see their status":  {"status", off, true, allowed},
		"active can book":                      {"book", active, true, allowed},
		"active is not an admin":               {"users", active, true, notAdmin},
		"a stranger is not an admin":           {"announcement", models.User{}, false, notAdmin},
		"admin can list users":                 {"users", admin, true, allowed},
		"admin can announce":                   {"announcement", admin, true, allowed},
		"an admin who is not active is not":    {"approve", user(models.RoleAdmin, models.StatusPending), true, notAdmin},
		"a missing status is not active":       {"book", user(models.RoleUser, ""), true, notApproved},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, accessFor(tc.command, tc.user, tc.registered))
		})
	}
}

func TestFormatUsersPutsPendingFirst(t *testing.T) {
	users := []models.User{
		{ChatID: 1, Email: "admin@example.com", Role: models.RoleAdmin, Status: models.StatusActive},
		{ChatID: 2, Email: "off@example.com", Role: models.RoleUser, Status: models.StatusDisabled},
		{ChatID: 3, Email: "new@example.com", Role: models.RoleUser, Status: models.StatusPending},
	}
	got := formatUsers(users, map[int64]bool{1: true})
	lines := strings.Split(got, "\n")
	assert.Equal(t, "👥 Users (3)", lines[0])
	assert.Equal(t, "⏳ new@example.com · chat 3 · 0 class(es)", lines[2])
	assert.Equal(t, "✅ admin@example.com · chat 1 · 0 class(es) · admin", lines[3])
	assert.Equal(t, "⛔ off@example.com · chat 2 · 0 class(es)", lines[4])
}

func TestAnnouncementsGoOnlyToActiveUsers(t *testing.T) {
	users := []models.User{
		{ChatID: 1, Status: models.StatusActive, Role: models.RoleAdmin},
		{ChatID: 2, Status: models.StatusActive},
		{ChatID: 3, Status: models.StatusPending},
		{ChatID: 4, Status: models.StatusDisabled},
	}
	assert.Equal(t, []int64{1, 2}, announcementRecipients(users))
}

func TestSignupMessageGivesTheAdminTheCommands(t *testing.T) {
	got := signupMessage(models.User{ChatID: 42, Email: "new@example.com"}, "newbie")
	assert.Contains(t, got, "new@example.com (@newbie)")
	assert.Contains(t, got, "/approve 42")
	assert.Contains(t, got, "/disable 42")
}
