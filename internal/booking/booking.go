// Package booking is the bot's side of pkg/wodbuster.
//
// It turns what the bot stores — an athlete's credentials and a list of
// recurring classes — into a real booking run: authenticate, trust the
// server's clock, wait for the opening, chase. The library does the work; this
// package only translates, so that the bot never has to know about sessions,
// class ids or race options.
//
// One Service is shared by every athlete. A run is per athlete and owns its own
// client, because a wodbuster.Client is bound to one session and serialises
// that athlete's booking calls.
package booking

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster/browserauth"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster/race"
)

// Target is a recurring class: "Monday 07:00 Wod", resolved to a real date at
// run time because a class id cannot be known in advance.
type Target struct {
	Weekday  time.Weekday
	Start    string // "07:00"
	Class    string // "Wod"
	Waitlist bool
}

// Outcome is what happened to one target, in terms the bot can show a user.
type Outcome struct {
	Class  string // "Monday 2026-09-28 07:00 Wod"
	Status string // booked, waitlisted, already-booked, dry-run, failed
	Err    error
}

func (o Outcome) OK() bool { return o.Err == nil && o.Status != "failed" }

// Service books for any athlete of one box.
type Service struct {
	box        string
	loc        *time.Location
	headless   bool
	chromePath string
	log        *slog.Logger
}

type Option func(*Service)

// WithHeadless(false) opens a real browser window during login. Debugging only:
// a server has no display.
func WithHeadless(v bool) Option { return func(s *Service) { s.headless = v } }

// WithChromePath points at a specific browser binary. Empty means "search the
// usual locations", which is right on a laptop and wrong in a container.
func WithChromePath(p string) Option { return func(s *Service) { s.chromePath = p } }

func New(box string, loc *time.Location, log *slog.Logger, opts ...Option) *Service {
	s := &Service{box: box, loc: loc, headless: true, log: log}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Authenticate checks that credentials work, and throws the session away. It is
// what /login needs: an answer, not a session — sessions do not survive until
// Sunday, so every run logs in fresh.
func (s *Service) Authenticate(ctx context.Context, email, password string) error {
	_, err := s.authenticate(ctx, email, password)
	return err
}

func (s *Service) authenticate(ctx context.Context, email, password string) (wodbuster.Session, error) {
	opts := []browserauth.Option{
		browserauth.WithHeadless(s.headless),
		browserauth.WithLogger(s.log),
	}
	if s.chromePath != "" {
		opts = append(opts, browserauth.WithChromePath(s.chromePath))
	}
	return browserauth.New(opts...).
		Authenticate(ctx, s.box, browserauth.Credentials{Email: email, Password: password})
}

// RunOptions says when to act and whether to touch anything.
type RunOptions struct {
	// OpensAt is the moment the week is published. Zero means "act now",
	// which is what a rehearsal wants.
	OpensAt time.Time

	// DryRun resolves every target and books nothing.
	DryRun bool
}

// Run books one athlete's targets. It is the same sequence cmd/wodbook runs,
// which is deliberate: that binary is where the sequence gets proven.
func (s *Service) Run(ctx context.Context, email, password string, targets []Target, o RunOptions) ([]Outcome, error) {
	if len(targets) == 0 {
		return nil, nil
	}

	sess, err := s.authenticate(ctx, email, password)
	if err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}
	client, err := wodbuster.NewClient(sess, wodbuster.WithLogger(s.log))
	if err != nil {
		return nil, err
	}

	clock, err := wodbuster.NewServerClock(ctx, client, 4)
	if err != nil {
		s.log.Warn("could not sync with the server clock; using the local one", "err", err)
		clock = wodbuster.SystemClock{}
	}

	goals := make([]race.Goal, 0, len(targets))
	for _, t := range targets {
		start, err := wodbuster.ParseTimeOfDay(t.Start)
		if err != nil {
			return nil, fmt.Errorf("target %s %s: %w", t.Weekday, t.Start, err)
		}
		goals = append(goals, race.Goal{
			Target: wodbuster.Target{
				Date:  wodbuster.NextWeekday(clock.Now().In(s.loc), t.Weekday),
				Start: start,
				Name:  t.Class,
			},
			Waitlist: t.Waitlist,
		})
	}

	opts := race.Options{Clock: clock, DryRun: o.DryRun, Log: s.log}

	if !o.OpensAt.IsZero() {
		opensAt := o.OpensAt
		// The server's own countdown beats a wall-clock guess when it offers one.
		if serverSays, err := race.OpensAt(ctx, client, goals[0].Date, clock); err == nil {
			if drift := serverSays.Sub(opensAt); drift > 2*time.Second || drift < -2*time.Second {
				s.log.Info("trusting the server's countdown over the configured opening",
					"server", serverSays, "configured", opensAt)
				opensAt = serverSays
			}
		}
		if err := race.WaitUntil(ctx, opensAt, opts); err != nil {
			return nil, err
		}
		// Reopens an idle connection so the first request of the race does not
		// pay for a fresh handshake.
		if err := client.Ping(ctx); err != nil {
			s.log.Warn("warm-up ping failed", "err", err)
		}
	}

	results := race.Chase(ctx, client, goals, opts)
	out := make([]Outcome, 0, len(results))
	for _, r := range results {
		out = append(out, Outcome{
			Class:  r.Goal.Target.String(),
			Status: r.Outcome.String(),
			Err:    r.Err,
		})
	}
	return out, nil
}

// NextOpening is the next moment the box publishes a week, in loc.
func NextOpening(now time.Time, wd time.Weekday, hhmm string, loc *time.Location) (time.Time, error) {
	t, err := wodbuster.ParseTimeOfDay(hhmm)
	if err != nil {
		return time.Time{}, err
	}
	now = now.In(loc)
	next := time.Date(now.Year(), now.Month(), now.Day(), t.Hour, t.Minute, 0, 0, loc)
	for next.Weekday() != wd || !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next, nil
}
