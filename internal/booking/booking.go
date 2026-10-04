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
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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
	metrics    *Metrics
}

type Option func(*Service)

// WithHeadless(false) opens a real browser window during login. Debugging only:
// a server has no display.
func WithHeadless(v bool) Option { return func(s *Service) { s.headless = v } }

// WithChromePath points at a specific browser binary. Empty means "search the
// usual locations", which is right on a laptop and wrong in a container.
func WithChromePath(p string) Option { return func(s *Service) { s.chromePath = p } }

// WithMetrics records what the runs do. Build it from the platform registry:
// booking.WithMetrics(booking.NewMetrics(prom.Registry())).
func WithMetrics(m *Metrics) Option { return func(s *Service) { s.metrics = m } }

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
		browserauth.WithDriftObserver(s.metrics.observeDrift),
	}
	if s.chromePath != "" {
		opts = append(opts, browserauth.WithChromePath(s.chromePath))
	}

	started := time.Now()
	sess, err := browserauth.New(opts...).
		Authenticate(ctx, s.box, browserauth.Credentials{Email: email, Password: password})
	s.metrics.observeLogin(loginResult(err), time.Since(started))
	return sess, err
}

// RunOptions says when to act and whether to touch anything.
type RunOptions struct {
	// OpensAt is the moment the week is published. Zero, or any time already
	// past, means "act now" — which is what a rehearsal wants, and what a run
	// that woke up late needs.
	OpensAt time.Time

	// DryRun resolves every target and books nothing.
	DryRun bool

	// GiveUpAfter bounds the chase once it starts. Zero takes the race
	// package's default of 90s, which is right for a real opening and far too
	// patient for a rehearsal.
	GiveUpAfter time.Duration

	// MaxWait refuses to wait longer than this for an opening. Zero takes the
	// default below. It exists because the alternative to a loud refusal is a
	// run that sits waiting for a date days away and dies on somebody else's
	// timeout, which is exactly what happened on 2026-10-04.
	MaxWait time.Duration
}

// defaultMaxWait is comfortably longer than any sane head start and far
// shorter than a week.
const defaultMaxWait = time.Hour

// Run books one athlete's targets. It is the same sequence cmd/wodbook runs,
// which is deliberate: that binary is where the sequence gets proven.
func (s *Service) Run(ctx context.Context, email, password string, targets []Target, o RunOptions) ([]Outcome, error) {
	if len(targets) == 0 {
		return nil, nil
	}

	// Before the browser, not after: an opening days away is a programming
	// error, and finding that out should not cost a login first.
	maxWait := o.MaxWait
	if maxWait <= 0 {
		maxWait = defaultMaxWait
	}
	if wait := time.Until(o.OpensAt); wait > maxWait {
		return nil, fmt.Errorf("refusing to wait %s for the opening at %s: "+
			"that is further off than this run should ever wait, so something "+
			"computed the wrong opening",
			wait.Round(time.Minute), o.OpensAt.Format(time.RFC1123))
	}

	log := s.log.With("athlete", email, "box", s.box)
	log.Info("run starting", "targets", len(targets), "dry_run", o.DryRun,
		"opens_at", o.OpensAt.Format(time.RFC1123))

	started := time.Now()
	sess, err := s.authenticate(ctx, email, password)
	if err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}
	log.Info("logged in", "took", time.Since(started).Round(time.Millisecond),
		"cookies", len(sess.Cookies))
	clientOpts := []wodbuster.Option{wodbuster.WithLogger(s.log)}
	if s.metrics != nil {
		clientOpts = append(clientOpts,
			wodbuster.WithHTTPClient(s.metrics.httpClientWith(&http.Client{Timeout: 20 * time.Second})))
	}
	client, err := wodbuster.NewClient(sess, clientOpts...)
	if err != nil {
		return nil, err
	}

	clock, err := wodbuster.NewServerClock(ctx, client, 4)
	if err != nil {
		log.Warn("could not sync with the server clock; using the local one", "err", err)
		clock = wodbuster.SystemClock{}
	} else if oc, ok := clock.(wodbuster.OffsetClock); ok {
		log.Info("clock synced", "offset", oc.Offset.Round(time.Millisecond))
		s.metrics.observeClockOffset(oc.Offset)
	}

	goals := make([]race.Goal, 0, len(targets))
	for _, t := range targets {
		start, err := wodbuster.ParseTimeOfDay(t.Start)
		if err != nil {
			return nil, fmt.Errorf("target %s %s: %w", t.Weekday, t.Start, err)
		}
		g := race.Goal{
			Target: wodbuster.Target{
				Date:  wodbuster.NextWeekday(clock.Now().In(s.loc), t.Weekday),
				Start: start,
				Name:  t.Class,
			},
			Waitlist: t.Waitlist,
		}
		log.Info("target resolved", "class", g.Target.String(), "waitlist", t.Waitlist)
		goals = append(goals, g)
	}

	opts := race.Options{Clock: clock, DryRun: o.DryRun, GiveUpAfter: o.GiveUpAfter, Log: s.log}

	// The server's clock decides whether there is still a wait: the opening is
	// its noon, not ours.
	if o.OpensAt.After(clock.Now()) {
		opensAt := o.OpensAt
		// The server's own countdown beats a wall-clock guess when it offers one.
		if serverSays, err := race.OpensAt(ctx, client, goals[0].Date, clock); err == nil {
			if drift := serverSays.Sub(opensAt); drift > 2*time.Second || drift < -2*time.Second {
				s.log.Info("trusting the server's countdown over the configured opening",
					"server", serverSays, "configured", opensAt)
				opensAt = serverSays
			}
		}
		s.log.Info("waiting for the opening",
			"at", opensAt.Format(time.RFC1123), "in", time.Until(opensAt).Round(time.Second))
		if err := race.WaitUntil(ctx, opensAt, opts); err != nil {
			return nil, err
		}
		// Reopens an idle connection so the first request of the race does not
		// pay for a fresh handshake.
		if err := client.Ping(ctx); err != nil {
			s.log.Warn("warm-up ping failed", "err", err)
		}
	}

	log.Info("chasing", "targets", len(goals))
	chaseStarted := time.Now()
	results := race.Chase(ctx, client, goals, opts)
	log.Info("chase finished", "took", time.Since(chaseStarted).Round(time.Millisecond))

	out := make([]Outcome, 0, len(results))
	for _, r := range results {
		s.observeResult(r, o.OpensAt)
		out = append(out, Outcome{
			Class:  r.Goal.Target.String(),
			Status: r.Outcome.String(),
			Err:    r.Err,
		})
	}
	return out, nil
}

// observeResult records what one chased goal did. Latency is measured from the
// opening, because that is the moment everyone else starts too; a run with no
// opening (a rehearsal) has nothing to measure against.
func (s *Service) observeResult(r race.Result, opensAt time.Time) {
	if s.metrics == nil {
		return
	}
	s.metrics.observeAttempts(r.Attempts)

	switch {
	case errors.Is(r.Err, race.ErrNeverPublished):
		s.metrics.observeUnpublished(r.Goal.Date.Weekday())
	case r.Outcome == race.OutcomeBooked && !opensAt.IsZero() && !r.At.IsZero():
		if d := r.At.Sub(opensAt); d >= 0 {
			s.metrics.observeBookingLatency(d)
		}
	}
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
