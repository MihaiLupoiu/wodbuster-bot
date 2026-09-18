package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

const minimalConfig = `{
  "box": "firespain",
  "email": "someone@example.com",
  "password": "hunter2",
  "targets": [{"weekday": "monday", "time": "07:00", "class": "Wod"}]
}`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestParseWeekday(t *testing.T) {
	for in, want := range map[string]time.Weekday{
		"monday":     time.Monday,
		"lunes":      time.Monday,
		"LUN":        time.Monday,
		"  martes  ": time.Tuesday,
		"miércoles":  time.Wednesday,
		"miercoles":  time.Wednesday,
		"sunday":     time.Sunday,
		"sábado":     time.Saturday,
	} {
		got, err := parseWeekday(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	for _, in := range []string{"", "someday", "mon", "lunedi"} {
		_, err := parseWeekday(in)
		assert.Error(t, err, in)
	}
}

func TestLoadConfigFillsInDefaults(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, minimalConfig))
	require.NoError(t, err)

	assert.Equal(t, "Europe/Madrid", cfg.Timezone)
	assert.Equal(t, "sunday", cfg.Opens.Weekday)
	assert.Equal(t, "12:00", cfg.Opens.Time)
	assert.Equal(t, 250, cfg.PollEveryMs)
	assert.Equal(t, 2000, cfg.StartBeforeMs)
	assert.Equal(t, 90000, cfg.GiveUpAfterMs)
	assert.Equal(t, 3, cfg.Attempts)
	assert.True(t, cfg.headless(), "headless defaults to true when the key is absent")
	require.NotNil(t, cfg.loc, "validate must resolve the timezone")
}

func TestLoadConfigKeepsExplicitValues(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, `{
      "box": "firespain", "email": "a@b.c", "password": "p",
      "timezone": "UTC",
      "opens": {"weekday": "saturday", "time": "10:30"},
      "pollEveryMs": 50, "startBeforeMs": 100, "giveUpAfterMs": 200, "attempts": 7,
      "headless": false,
      "targets": [{"weekday": "friday", "time": "19:00", "class": "Open box"}]
    }`))
	require.NoError(t, err)

	assert.Equal(t, "UTC", cfg.Timezone)
	assert.Equal(t, "saturday", cfg.Opens.Weekday)
	assert.Equal(t, 50, cfg.PollEveryMs)
	assert.Equal(t, 7, cfg.Attempts)
	assert.False(t, cfg.headless())
}

// Credentials in a config file get committed by accident; the environment is
// the way in that does not.
func TestEnvironmentOverridesFileCredentials(t *testing.T) {
	t.Setenv("WODBUSTER_EMAIL", "env@example.com")
	t.Setenv("WODBUSTER_PASSWORD", "from-env")

	cfg, err := loadConfig(writeConfig(t, minimalConfig))
	require.NoError(t, err)
	assert.Equal(t, "env@example.com", cfg.Email)
	assert.Equal(t, "from-env", cfg.Password)
}

func TestLoadConfigRejectsBadInput(t *testing.T) {
	for name, body := range map[string]string{
		"not json":       `{`,
		"no box":         `{"email":"a@b.c","password":"p","targets":[{"weekday":"monday","time":"07:00","class":"Wod"}]}`,
		"no credentials": `{"box":"x","targets":[{"weekday":"monday","time":"07:00","class":"Wod"}]}`,
		"no targets":     `{"box":"x","email":"a@b.c","password":"p","targets":[]}`,
		"bad weekday":    `{"box":"x","email":"a@b.c","password":"p","targets":[{"weekday":"funday","time":"07:00","class":"Wod"}]}`,
		"bad time":       `{"box":"x","email":"a@b.c","password":"p","targets":[{"weekday":"monday","time":"25:00","class":"Wod"}]}`,
		"no class name":  `{"box":"x","email":"a@b.c","password":"p","targets":[{"weekday":"monday","time":"07:00","class":"  "}]}`,
		"bad opens":      `{"box":"x","email":"a@b.c","password":"p","opens":{"weekday":"funday","time":"12:00"},"targets":[{"weekday":"monday","time":"07:00","class":"Wod"}]}`,
		"bad timezone":   `{"box":"x","email":"a@b.c","password":"p","timezone":"Mars/Olympus","targets":[{"weekday":"monday","time":"07:00","class":"Wod"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("WODBUSTER_EMAIL", "")
			t.Setenv("WODBUSTER_PASSWORD", "")
			_, err := loadConfig(writeConfig(t, body))
			assert.Error(t, err)
		})
	}

	_, err := loadConfig(filepath.Join(t.TempDir(), "absent.json"))
	assert.Error(t, err, "a missing file is an error, not an empty config")
}

// The opening is the one thing that must not be off by a week: getting it wrong
// costs seven days, which is exactly the cost this binary exists to avoid.
func TestNextOpening(t *testing.T) {
	madrid, err := time.LoadLocation("Europe/Madrid")
	require.NoError(t, err)

	cfg, err := loadConfig(writeConfig(t, minimalConfig)) // opens sunday 12:00
	require.NoError(t, err)

	// 2026-09-20 is a Sunday.
	sunday := func(h, m int) time.Time { return time.Date(2026, time.September, 20, h, m, 0, 0, madrid) }

	for name, tc := range map[string]struct {
		now  time.Time
		want time.Time
	}{
		"hours before the opening, same day": {sunday(11, 50), sunday(12, 0)},
		"a minute before":                    {sunday(11, 59), sunday(12, 0)},
		"a minute after, so next week":       {sunday(12, 1), sunday(12, 0).AddDate(0, 0, 7)},
		"exactly at the opening":             {sunday(12, 0), sunday(12, 0).AddDate(0, 0, 7)},
		"the day after":                      {sunday(12, 0).AddDate(0, 0, 1), sunday(12, 0).AddDate(0, 0, 7)},
		"the day before":                     {sunday(23, 0).AddDate(0, 0, -1), sunday(12, 0)},
	} {
		t.Run(name, func(t *testing.T) {
			assert.True(t, cfg.nextOpening(tc.now).Equal(tc.want),
				"now=%s: got %s, want %s", tc.now, cfg.nextOpening(tc.now), tc.want)
		})
	}

	// A clock in another zone must not move the opening: it is 12:00 in Madrid.
	utcNow := sunday(11, 50).UTC()
	assert.True(t, cfg.nextOpening(utcNow).Equal(sunday(12, 0)),
		"the opening is defined in the configured timezone, not the caller's")
}

// Targets are written as a recurring weekday and resolved to a real date at run
// time, because a ClassID cannot be known in advance and must never be stored.
func TestTargetsResolveToTheComingWeek(t *testing.T) {
	madrid, err := time.LoadLocation("Europe/Madrid")
	require.NoError(t, err)
	sundayNoon := time.Date(2026, time.September, 20, 11, 50, 0, 0, madrid)

	wd, err := parseWeekday("monday")
	require.NoError(t, err)
	got := wodbuster.NextWeekday(sundayNoon, wd)
	assert.Equal(t, wodbuster.NewDate(2026, time.September, 21), got,
		"booking on Sunday morning targets the Monday of the week about to publish")
}

// The other binaries in this repo keep credentials in a .env; the CLI reads one
// too, so that "it is right there in the file" is not a trap.
func TestLoadDotEnv(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), ".env")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		return path
	}

	t.Run("fills the environment", func(t *testing.T) {
		t.Setenv("WODBUSTER_EMAIL", "")
		path := write(t, "WODBUSTER_EMAIL=from-file@example.com\nWODBUSTER_PASSWORD=file-secret\n")
		require.NoError(t, loadDotEnv(path, true))
		assert.Equal(t, "from-file@example.com", os.Getenv("WODBUSTER_EMAIL"))
		assert.Equal(t, "file-secret", os.Getenv("WODBUSTER_PASSWORD"))
	})

	// A deployment sets real variables; a file on disk must not quietly win.
	t.Run("an exported variable beats the file", func(t *testing.T) {
		t.Setenv("WODBUSTER_PASSWORD", "exported-secret")
		path := write(t, "WODBUSTER_PASSWORD=file-secret\n")
		require.NoError(t, loadDotEnv(path, true))
		assert.Equal(t, "exported-secret", os.Getenv("WODBUSTER_PASSWORD"))
	})

	t.Run("the default path may be absent", func(t *testing.T) {
		assert.NoError(t, loadDotEnv(filepath.Join(t.TempDir(), ".env"), false))
	})

	// Asking for a file by name and being ignored is the failure mode worth
	// avoiding: it looks exactly like credentials that did not work.
	t.Run("a named path must exist", func(t *testing.T) {
		assert.Error(t, loadDotEnv(filepath.Join(t.TempDir(), "nope.env"), true))
	})

	t.Run("no path at all is fine", func(t *testing.T) {
		assert.NoError(t, loadDotEnv("", false))
		assert.NoError(t, loadDotEnv("", true))
	})
}

// -env is what makes the file reach the config, so check the whole path.
func TestCredentialsReachTheConfigFromAnEnvFile(t *testing.T) {
	t.Setenv("WODBUSTER_EMAIL", "")
	t.Setenv("WODBUSTER_PASSWORD", "")

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envPath,
		[]byte("WODBUSTER_EMAIL=env-file@example.com\nWODBUSTER_PASSWORD=env-file-secret\n"), 0o600))

	cfgPath := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{
      "box": "firespain",
      "targets": [{"weekday": "monday", "time": "07:00", "class": "Wod"}]
    }`), 0o600))

	_, err := loadConfig(cfgPath)
	require.Error(t, err, "without the env file there are no credentials")

	require.NoError(t, loadDotEnv(envPath, true))
	cfg, err := loadConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "env-file@example.com", cfg.Email)
	assert.Equal(t, "env-file-secret", cfg.Password)
}

// An exported-but-empty variable is how a shell leaves a name behind. It must
// not shadow the file: that failure looks identical to a wrong password.
func TestEmptyExportedVariableDoesNotShadowTheFile(t *testing.T) {
	t.Setenv("WODBUSTER_EMAIL", "")
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("WODBUSTER_EMAIL=from-file@example.com\n"), 0o600))

	require.NoError(t, loadDotEnv(path, true))
	assert.Equal(t, "from-file@example.com", os.Getenv("WODBUSTER_EMAIL"))
}
