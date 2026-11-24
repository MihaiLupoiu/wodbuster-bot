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

func TestTheDefaultKeyIsRefusedWithMongo(t *testing.T) {
	for _, tc := range []struct {
		storage, key string
		ok           bool
	}{
		{"mongodb", defaultEncryptionKey, false},
		{"mongodb", "a-real-secret-key-of-32-bytes!!!", true},
		{"memory", defaultEncryptionKey, true},
	} {
		c := Config{StorageType: tc.storage, EncryptionKey: tc.key}
		err := c.checkEncryptionKey()
		assert.Equal(t, tc.ok, err == nil, "%s with %q: %v", tc.storage, tc.key, err)
	}
}

func TestNewRefusesBadSessionSettings(t *testing.T) {
	for _, tc := range []struct{ name, reuse, key, storage string }{
		{"live is not built", "live", "local-dev-key-do-not-use-in-prod", "memory"},
		{"unknown mode", "on", "local-dev-key-do-not-use-in-prod", "memory"},
		{"default key with mongo", "shadow", defaultEncryptionKey, "mongodb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{SessionReuse: tc.reuse, EncryptionKey: tc.key, StorageType: tc.storage,
				Timezone: "Europe/Madrid", MongoURI: "mongodb://127.0.0.1:1"}
			_, err := New(c)
			assert.Error(t, err)
		})
	}
}
