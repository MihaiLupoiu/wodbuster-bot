package telegram

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
)

func TestSessionLine(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	exp := time.Date(2027, 1, 2, 3, 0, 0, 0, time.UTC)

	for name, tc := range map[string]struct {
		st   models.StoredSession
		ok   bool
		err  error
		want string
	}{
		"none": {ok: false,
			want: "⚠️ none stored: the next run will log in"},
		"unreadable store": {err: errors.New("mongo down"),
			want: "❓ could not be read right now"},
		"fresh, remembered device": {ok: true,
			st:   models.StoredSession{LastUsedAt: now.Add(-3 * time.Hour), AuthExpiresAt: exp},
			want: "✅ stored · last accepted by WodBuster 3h ago · login cookie expires 2027-01-02"},
		"minutes": {ok: true,
			st:   models.StoredSession{LastUsedAt: now.Add(-5 * time.Minute)},
			want: "✅ stored · last accepted by WodBuster 5m ago · login cookie ends with the session"},
		"session cookie": {ok: true,
			st:   models.StoredSession{LastUsedAt: now.Add(-30 * time.Second)},
			want: "✅ stored · last accepted by WodBuster just now · login cookie ends with the session"},
		"refresh has stopped": {ok: true,
			st:   models.StoredSession{LastUsedAt: now.Add(-50 * time.Hour), AuthExpiresAt: exp},
			want: "⚠️ stored but not checked recently · last accepted by WodBuster 2d ago · login cookie expires 2027-01-02"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, sessionLine(tc.st, tc.ok, tc.err, now, nil))
		})
	}
}

// The cookie's expiry is a date the athlete reads, so it is the gym's date:
// 23:30 UTC on 1 January is already 2 January in Madrid.
func TestSessionLineGivesTheExpiryInTheGymsTimeZone(t *testing.T) {
	madrid, err := time.LoadLocation("Europe/Madrid")
	require.NoError(t, err)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	st := models.StoredSession{LastUsedAt: now, AuthExpiresAt: time.Date(2027, 1, 1, 23, 30, 0, 0, time.UTC)}

	assert.Contains(t, sessionLine(st, true, nil, now, madrid), "login cookie expires 2027-01-02")
	assert.Contains(t, sessionLine(st, true, nil, now, nil), "login cookie expires 2027-01-01", "nil is UTC")
}

func TestFormatUsersMarksActiveUsersWithoutASession(t *testing.T) {
	users := []models.User{
		{ChatID: 1, Email: "with@example.com", Status: models.StatusActive},
		{ChatID: 2, Email: "without@example.com", Status: models.StatusActive},
		{ChatID: 3, Email: "new@example.com", Status: models.StatusPending},
	}
	got := formatUsers(users, map[int64]bool{1: true, 2: false})
	lines := strings.Split(got, "\n")
	assert.Equal(t, "✅ with@example.com · chat 1 · 0 class(es)", lines[3])
	assert.Equal(t, "✅ without@example.com · chat 2 · 0 class(es) · ⚠️ no stored session", lines[4])
	assert.Equal(t, "⏳ new@example.com · chat 3 · 0 class(es)", lines[2], "pending users are not marked")
}
