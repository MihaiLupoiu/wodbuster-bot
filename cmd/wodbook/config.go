package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

// Target is a recurring class: the same weekday and time every week. The CLI
// turns it into a concrete date at run time, because the persistent identity of
// a class is a date plus a time plus a name — never an id (see the wodbuster
// package docs).
type Target struct {
	Weekday  string `json:"weekday"` // monday, martes, lun... see parseWeekday
	Time     string `json:"time"`    // "07:00"
	Class    string `json:"class"`   // "Wod"
	Waitlist *bool  `json:"waitlist,omitempty"`
}

type Opening struct {
	Weekday string `json:"weekday"`
	Time    string `json:"time"`
}

type Config struct {
	Box      string `json:"box"`
	Email    string `json:"email"`
	Password string `json:"password"`

	Timezone string   `json:"timezone"`
	Opens    Opening  `json:"opens"`
	Targets  []Target `json:"targets"`

	Waitlist    bool `json:"waitlist"`
	TrustDevice bool `json:"trustDevice"`

	ChromePath     string `json:"chromePath"`
	Headless       *bool  `json:"headless"`
	DiagnosticsDir string `json:"diagnosticsDir"`

	// Tuning. Defaults are fine.
	PollEveryMs   int `json:"pollEveryMs"`
	StartBeforeMs int `json:"startBeforeMs"`
	GiveUpAfterMs int `json:"giveUpAfterMs"`
	RetryEveryMs  int `json:"retryEveryMs"`
	StatusEveryMs int `json:"statusEveryMs"`

	// Attempts caps the booking calls per class. Leave it out, or set it to 0,
	// to keep trying until the class fills up or giveUpAfterMs runs out.
	Attempts int `json:"attempts"`

	loc *time.Location
}

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "domingo": time.Sunday, "dom": time.Sunday,
	"monday": time.Monday, "lunes": time.Monday, "lun": time.Monday,
	"tuesday": time.Tuesday, "martes": time.Tuesday, "mar": time.Tuesday,
	"wednesday": time.Wednesday, "miercoles": time.Wednesday, "miércoles": time.Wednesday, "mie": time.Wednesday,
	"thursday": time.Thursday, "jueves": time.Thursday, "jue": time.Thursday,
	"friday": time.Friday, "viernes": time.Friday, "vie": time.Friday,
	"saturday": time.Saturday, "sabado": time.Saturday, "sábado": time.Saturday, "sab": time.Saturday,
}

func parseWeekday(s string) (time.Weekday, error) {
	wd, ok := weekdays[strings.ToLower(strings.TrimSpace(s))]
	if !ok {
		return 0, fmt.Errorf("unknown weekday %q", s)
	}
	return wd, nil
}

// defaultEnvFile is read when -env is not given, and only if it happens to be
// there: the other binaries in this repo keep credentials in a .env, and making
// the CLI ignore it would be a trap.
const defaultEnvFile = ".env"

// loadDotEnv copies an env file into the process environment.
//
// An exported variable wins — the file is a convenience for a laptop, and a
// real environment variable is what a deployment sets. An exported *empty*
// variable does not win, which is why this does not use godotenv.Load: that
// treats "set to nothing" as set, so a stray `export WODBUSTER_EMAIL=` would
// silently shadow the file and look exactly like a rejected password. Empty
// means absent here, the same as everywhere else in this config.
//
// A path the caller asked for by name must exist; the default one need not.
func loadDotEnv(path string, explicit bool) error {
	if path == "" {
		return nil
	}
	vars, err := godotenv.Read(path)
	if err != nil {
		if !explicit && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("%s is not a readable env file: %w", path, err)
	}
	for k, v := range vars {
		if os.Getenv(k) != "" {
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			return fmt.Errorf("setting %s from %s: %w", k, path, err)
		}
	}
	return nil
}

func loadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}

	// The environment wins: credentials do not belong in a file that gets
	// committed by accident.
	if v := os.Getenv("WODBUSTER_EMAIL"); v != "" {
		c.Email = v
	}
	if v := os.Getenv("WODBUSTER_PASSWORD"); v != "" {
		c.Password = v
	}

	if c.Timezone == "" {
		c.Timezone = "Europe/Madrid"
	}
	if c.Opens.Weekday == "" {
		c.Opens.Weekday = "sunday"
	}
	if c.Opens.Time == "" {
		c.Opens.Time = "12:00"
	}
	if c.PollEveryMs <= 0 {
		c.PollEveryMs = 250
	}
	if c.StartBeforeMs <= 0 {
		c.StartBeforeMs = 2000
	}
	if c.GiveUpAfterMs <= 0 {
		c.GiveUpAfterMs = 90000
	}
	if c.RetryEveryMs <= 0 {
		c.RetryEveryMs = 150
	}
	if c.StatusEveryMs <= 0 {
		c.StatusEveryMs = 1000
	}
	if c.Attempts < 0 {
		c.Attempts = 0
	}

	return c, c.validate()
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.Box) == "" {
		return fmt.Errorf(`"box" is required (your centre's subdomain, e.g. "firespain")`)
	}
	if c.Email == "" || c.Password == "" {
		return fmt.Errorf("missing credentials: set WODBUSTER_EMAIL and WODBUSTER_PASSWORD " +
			"in the environment, in the file given to -env, or in the config file")
	}
	if len(c.Targets) == 0 {
		return fmt.Errorf(`"targets" is empty: nothing to book`)
	}
	for i, t := range c.Targets {
		if _, err := parseWeekday(t.Weekday); err != nil {
			return fmt.Errorf("target %d: %w", i+1, err)
		}
		if _, err := wodbuster.ParseTimeOfDay(t.Time); err != nil {
			return fmt.Errorf("target %d: %w", i+1, err)
		}
		if strings.TrimSpace(t.Class) == "" {
			return fmt.Errorf("target %d: missing class name", i+1)
		}
	}
	if _, err := parseWeekday(c.Opens.Weekday); err != nil {
		return fmt.Errorf("opens.weekday: %w", err)
	}
	if _, err := wodbuster.ParseTimeOfDay(c.Opens.Time); err != nil {
		return fmt.Errorf("opens.time: %w", err)
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return fmt.Errorf("unknown timezone %q: %w", c.Timezone, err)
	}
	c.loc = loc
	return nil
}

func (c *Config) headless() bool {
	if c.Headless == nil {
		return true
	}
	return *c.Headless
}

// nextOpening is the next moment the box publishes a week. If today is the
// opening day and the hour has not passed, it is today.
func (c *Config) nextOpening(now time.Time) time.Time {
	wd, _ := parseWeekday(c.Opens.Weekday)
	at, _ := wodbuster.ParseTimeOfDay(c.Opens.Time)
	n := now.In(c.loc)
	today := time.Date(n.Year(), n.Month(), n.Day(), at.Hour, at.Minute, 0, 0, c.loc)
	if n.Weekday() == wd && n.Before(today) {
		return today
	}
	d := wodbuster.NextWeekday(n, wd)
	return d.At(at, c.loc)
}
