package app

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/usecase"
)

// unsetenv clears a variable for the test and restores it afterwards.
func unsetenv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "") // registers the restore
		require.NoError(t, os.Unsetenv(k))
	}
}

// The session defaults are what a deployment gets without touching .env:
// shadow mode, a trusted device, the refresh away from Sunday's run.
func TestSessionDefaults(t *testing.T) {
	unsetenv(t, "SESSION_REUSE", "WODBUSTER_TRUST_DEVICE", "SESSION_REFRESH_CRON",
		"SESSION_PREFLIGHT_CRON", "SESSION_PREFLIGHT_ALL_OK_MESSAGE", "ENCRYPTION_KEY")
	var c Config
	require.NoError(t, c.ParseEnv())

	assert.Equal(t, "shadow", c.SessionReuse)
	assert.True(t, c.TrustDevice)
	assert.Equal(t, "0 3,15 * * *", c.SessionRefreshCron)
	assert.Equal(t, "0 11 * * 0", c.SessionPreflightCron)
	assert.True(t, c.PreflightAllOKMessage)
	assert.Equal(t, defaultEncryptionKey, c.EncryptionKey, "the constant matches the tag")

	_, err := usecase.ParseReuseMode(c.SessionReuse)
	assert.NoError(t, err, "the default mode must be one New accepts")
}

func TestCheckEncryptionKey(t *testing.T) {
	for _, tc := range []struct {
		storage, key string
		want         string // "" means accepted
	}{
		{"mongodb", defaultEncryptionKey, "public default"},
		{"mongodb", "a-real-secret-key-of-32-bytes!!!", ""},
		{"mongodb", "sixteen-byte-key", ""},
		{"memory", defaultEncryptionKey, ""},
		{"memory", "your-32-character-secret-key123", "31 bytes"}, // the old default
		{"memory", "", "0 bytes"},
		{"mongodb", "too-short", "9 bytes"},
	} {
		c := Config{StorageType: tc.storage, EncryptionKey: tc.key}
		err := c.checkEncryptionKey()
		if tc.want == "" {
			assert.NoError(t, err, "%s with %q", tc.storage, tc.key)
		} else if assert.Error(t, err, "%s with %q", tc.storage, tc.key) {
			assert.Contains(t, err.Error(), tc.want)
		}
	}
}

// Each case must fail for its own reason, before anything is connected: the
// Mongo URI points nowhere, and a later failure would pass a bare Error check.
func TestNewRefusesBadSessionSettings(t *testing.T) {
	for _, tc := range []struct{ name, reuse, key, storage, want string }{
		{"live is not built", "live", defaultEncryptionKey, "mongodb", "phase 2"},
		{"unknown mode", "on", defaultEncryptionKey, "mongodb", `SESSION_REUSE="on"`},
		{"default key with mongo", "shadow", defaultEncryptionKey, "mongodb", "public default"},
		{"key of the wrong length", "shadow", "short", "memory", "5 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := tc.key
			if tc.want != "public default" && tc.storage == "mongodb" {
				key = "a-real-secret-key-of-32-bytes!!!"
			}
			c := &Config{SessionReuse: tc.reuse, EncryptionKey: key, StorageType: tc.storage,
				Timezone: "Europe/Madrid", MongoURI: "mongodb://127.0.0.1:1"}
			_, err := New(c)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.NotContains(t, err.Error(), "MongoDB storage", "refused before connecting")
		})
	}
}
