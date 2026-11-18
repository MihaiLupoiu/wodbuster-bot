package app

import (
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/platform/logging"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	TelegramToken string `envconfig:"TELEGRAM_BOT_TOKEN" default:"your_bot_token_here"`
	Env           string `envconfig:"APP_ENV" default:"prod"`
	Logger        *slog.Logger
	LoggerLevel   slog.Level `envconfig:"LOGGING_LEVEL" default:"DEBUG"`
	WODBusterURL  string     `envconfig:"WODBUSTER_URL" default:"https://wodbuster.com"`

	// Box is the centre's subdomain: firespain.wodbuster.com.
	Box string `envconfig:"WODBUSTER_BOX" default:"firespain"`

	// Timezone the opening is defined in. The opening is a wall-clock time at
	// the gym, so it must not follow the server's zone.
	Timezone string `envconfig:"WODBUSTER_TIMEZONE" default:"Europe/Madrid"`

	// ChromePath points at the browser the login drives. Empty lets chromedp
	// search the usual locations, which works on a developer's machine; the
	// container sets it explicitly.
	ChromePath string `envconfig:"WODBUSTER_CHROME_PATH"`

	// DiagnosticsDir is where a failed login leaves an HTML dump (passwords
	// stripped) and a screenshot. Empty turns it off. In a container, mount a
	// volume here to keep the files past a restart.
	DiagnosticsDir string `envconfig:"WODBUSTER_DIAGNOSTICS_DIR" default:"/tmp/wodbuster-diagnostics"`

	// LoginGap spaces out the athletes' logins: one at a time, each starting
	// this long after the previous one finished. Zero lets them run together.
	LoginGap time.Duration `envconfig:"WODBUSTER_LOGIN_GAP" default:"20s"`

	// TrustDevice makes logins answer "remember this device", so the session
	// cookie gets a 45-day sliding expiry and a stored session lasts. False
	// turns it off without a release.
	TrustDevice bool `envconfig:"WODBUSTER_TRUST_DEVICE" default:"true"`

	// LoginCheckCron is when the daily login check runs, in the gym's zone. It
	// logs in with the first active admin's account. Empty turns it off; with
	// no admin it does nothing.
	LoginCheckCron string `envconfig:"LOGIN_CHECK_CRON" default:"0 10 * * *"`

	// SessionRefreshCron is when stored WodBuster sessions are checked and
	// renewed, in the gym's zone. Every 12 hours keeps each one inside the
	// 16 idle hours a session has been seen to survive; 03:00 and 15:00 stay
	// clear of Sunday's run. Empty turns it off.
	SessionRefreshCron string `envconfig:"SESSION_REFRESH_CRON" default:"0 3,15 * * *"`

	// SessionPreflightCron is when the Sunday preflight logs everyone in, an
	// hour before the opening, so a broken login is found with time to react.
	// Empty turns it off.
	SessionPreflightCron string `envconfig:"SESSION_PREFLIGHT_CRON" default:"0 11 * * 0"`

	// SessionReuse is off (log in at 11:50 as before, no preflight) or shadow
	// (log in at 11:00, book with that session, and only check the days-old
	// stored one). live is phase 2 and refused until it is built.
	SessionReuse string `envconfig:"SESSION_REUSE" default:"shadow"`

	// MongoDB configuration
	MongoURI    string `envconfig:"MONGO_URI" default:"mongodb://localhost:27017"`
	MongoDB     string `envconfig:"MONGO_DB" default:"wodbuster"`
	StorageType string `envconfig:"STORAGE_TYPE" default:"memory"` // "memory" or "mongodb"

	// Security configuration
	EncryptionKey string `envconfig:"ENCRYPTION_KEY" default:"your-32-character-secret-key123"`

	// Health check configuration
	HealthCheckPort string `envconfig:"HEALTH_CHECK_PORT" default:"8080"`
	Version         string `envconfig:"APP_VERSION" default:"1.0.0"`
}

func NewConfig(envFile string) (*Config, error) {
	cfg := &Config{}
	if err := cfg.LoadDotEnv(envFile); err != nil {
		log.Printf("Warning: Failed to load env file: %v", err)
	}
	if err := cfg.ParseEnv(); err != nil {
		log.Fatalf("app start failed while parsing env: %v", err)
	}

	cfg.Logger = cfg.NewLogger()
	slog.SetDefault(cfg.Logger)

	return cfg, nil
}

// NewLogger is the JSON logger at the configured level, with the bot token and
// the database password redacted from everything it writes. slog.SetDefault
// on it also routes the standard log package through the same redaction.
func (c *Config) NewLogger() *slog.Logger {
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: c.LoggerLevel})
	redactor := logging.NewRedactor(c.TelegramToken, logging.URIPassword(c.MongoURI))
	return slog.New(logging.NewHandler(jsonHandler, redactor))
}

// LoadDotEnv read .env file and load vars into env
func (c *Config) LoadDotEnv(configFile string) error {
	// do not load .env if we're in production
	// can not read it from *Config yet
	if configFile != "" {
		path, err := filepath.Abs(configFile)
		if err != nil {
			return err
		}
		slog.Info("Loading .env file", slog.String("file", path))
		return godotenv.Load(configFile)
	}
	slog.Info("Not loading .env file, because it has not been provided")
	return nil
}

// ParseEnv takes the global env as source and match the fields on Config
// from envconfig json tag
func (c *Config) ParseEnv() error {
	return envconfig.Process("", c)
}
