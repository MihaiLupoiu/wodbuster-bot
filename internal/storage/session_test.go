package storage

import (
	"context"
	"testing"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage/functionaltest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sessionStore interface {
	SaveUser(ctx context.Context, user models.User) error
	GetUser(ctx context.Context, chatID int64) (models.User, bool)
	SaveSession(ctx context.Context, s models.StoredSession) error
	GetSession(ctx context.Context, chatID int64) (models.StoredSession, bool, error)
	DeleteSession(ctx context.Context, chatID int64) error
}

// testSessions is the contract both stores keep. Memory backs the use-case
// tests, so if it drifts from Mongo those tests prove the wrong thing.
func testSessions(t *testing.T, st sessionStore) {
	ctx := context.Background()
	// Mongo keeps milliseconds and UTC.
	at := func(d time.Duration) time.Time { return time.Now().Add(d).UTC().Truncate(time.Millisecond) }

	t.Run("missing is not an error", func(t *testing.T) {
		_, ok, err := st.GetSession(ctx, 404)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("save and get", func(t *testing.T) {
		want := models.StoredSession{
			ChatID:        1,
			Encrypted:     "sealed",
			LoggedInAt:    at(-48 * time.Hour),
			LastUsedAt:    at(-time.Hour),
			AuthExpiresAt: at(45 * 24 * time.Hour),
			TrustedDevice: true,
		}
		require.NoError(t, st.SaveSession(ctx, want))

		got, ok, err := st.GetSession(ctx, 1)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, want.Encrypted, got.Encrypted)
		assert.True(t, want.LoggedInAt.Equal(got.LoggedInAt), "logged_in_at")
		assert.True(t, want.LastUsedAt.Equal(got.LastUsedAt), "last_used_at")
		assert.True(t, want.AuthExpiresAt.Equal(got.AuthExpiresAt), "auth_expires_at")
		assert.True(t, got.TrustedDevice)
		assert.WithinDuration(t, time.Now(), got.UpdatedAt, time.Minute, "updated_at is set on save")
	})

	t.Run("session cookie keeps a zero expiry", func(t *testing.T) {
		require.NoError(t, st.SaveSession(ctx, models.StoredSession{ChatID: 2, Encrypted: "x", LoggedInAt: at(0)}))
		got, ok, err := st.GetSession(ctx, 2)
		require.NoError(t, err)
		require.True(t, ok)
		assert.True(t, got.AuthExpiresAt.IsZero())
	})

	t.Run("save replaces, one per athlete", func(t *testing.T) {
		require.NoError(t, st.SaveSession(ctx, models.StoredSession{ChatID: 3, Encrypted: "first"}))
		require.NoError(t, st.SaveSession(ctx, models.StoredSession{ChatID: 3, Encrypted: "second"}))
		got, ok, err := st.GetSession(ctx, 3)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "second", got.Encrypted)
	})

	t.Run("delete, and delete again", func(t *testing.T) {
		require.NoError(t, st.SaveSession(ctx, models.StoredSession{ChatID: 4, Encrypted: "x"}))
		require.NoError(t, st.DeleteSession(ctx, 4))
		_, ok, err := st.GetSession(ctx, 4)
		require.NoError(t, err)
		assert.False(t, ok)
		require.NoError(t, st.DeleteSession(ctx, 4), "deleting a missing session is fine")
	})

	// The reason sessions have their own collection: saving one must leave
	// the user alone, and saving the user must leave the session alone.
	t.Run("sessions and users do not touch each other", func(t *testing.T) {
		user := models.User{ChatID: 5, Email: "a@b.c", Password: "p", Status: models.StatusActive}
		require.NoError(t, st.SaveUser(ctx, user))
		require.NoError(t, st.SaveSession(ctx, models.StoredSession{ChatID: 5, Encrypted: "s"}))

		got, ok := st.GetUser(ctx, 5)
		require.True(t, ok)
		assert.Equal(t, "a@b.c", got.Email)

		user.Email = "new@b.c"
		require.NoError(t, st.SaveUser(ctx, user))
		s, ok, err := st.GetSession(ctx, 5)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "s", s.Encrypted)

		require.NoError(t, st.DeleteSession(ctx, 5))
		_, ok = st.GetUser(ctx, 5)
		assert.True(t, ok, "deleting the session keeps the user")
	})
}

func TestMemorySessions(t *testing.T) {
	testSessions(t, NewMemoryStorage())
}

func TestMongoSessions(t *testing.T) {
	ctx := context.Background()
	dbName := "test_sessions"
	client, uri, err := functionaltest.CreateMongoContainer(ctx, t, dbName)
	require.NoError(t, err)

	st, err := NewMongoStorage(uri, dbName)
	require.NoError(t, err)
	defer st.Close()
	testSessions(t, st)

	t.Run("unique index on chat_id", func(t *testing.T) {
		// Opening a second time must not fail on the existing index.
		again, err := NewMongoStorage(uri, dbName)
		require.NoError(t, err)
		defer again.Close()

		coll := client.Database(dbName).Collection("sessions")
		_, err = coll.InsertOne(ctx, models.StoredSession{ChatID: 1, Encrypted: "dup"})
		assert.Error(t, err, "a second document for the same chat id must be refused")
	})
}
