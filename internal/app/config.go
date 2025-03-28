package app

import (
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	TelegramToken string `envconfig:"TELEGRAM_BOT_TOKEN" default:"your_bot_token_here"`
	Env           string `envconfig:"APP_ENV" default:"prod"`
	Logger        *slog.Logger
	LoggerLevel   slog.Level `envconfig:"LOGGING_LEVEL" default:"DEBUG"`
	WodbusterURL  string     `envconfig:"WODBUSTER_URL" default:"https://wodbuster.com"`

	// MongoDB configuration
	MongoURI    string `envconfig:"MONGO_URI" default:"mongodb://localhost:27017"`
	MongoDB     string `envconfig:"MONGO_DB" default:"wodbuster"`
	StorageType string `envconfig:"STORAGE_TYPE" default:"memory"` // "memory" or "mongodb"
}

func NewConfig(envFile string) (*Config, error) {
	cfg := &Config{}
	if err := cfg.LoadDotEnv(envFile); err != nil {
		log.Printf("Warning: Failed to load env file: %v", err)
	}
	if err := cfg.ParseEnv(); err != nil {
		log.Fatalf("app start failed while parsing env: %v", err)
	}

	cfg.Logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LoggerLevel,
	}))
	slog.SetDefault(cfg.Logger)

	return cfg, nil
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
