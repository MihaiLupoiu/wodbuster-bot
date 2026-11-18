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
	"sync"
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
	box            string
	loc            *time.Location
	headless       bool
	chromePath     string
	diagnosticsDir string
	loginAttempts  int
	loginBackoff   time.Duration
	gate           *loginGate
	trustDevice    bool
	log            *slog.Logger
	metrics        *Metrics

	// clientOpts go last when a wodbuster.Client is built. Tests use it to
	// point clients at wodbustertest.
	clientOpts []wodbuster.Option

	// login is how a run gets a session. It is s.authenticate outside tests;
	// tests swap it so that a run can be driven against wodbustertest without
	// a browser.
	login func(ctx context.Context, email, password string) (wodbuster.Session, error)
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

// WithDiagnosticsDir makes a failed login leave an HTML dump (passwords
// stripped) and a screenshot there. Empty turns it off. It is the only record
// of what WodBuster showed when a login got stuck.
func WithDiagnosticsDir(d string) Option { return func(s *Service) { s.diagnosticsDir = d } }

// WithTrustDevice makes the login answer "remember this device" instead of
// "just once". Then .WBAuth gets an expiry 45 days out, moved forward on every
// use, which is what makes a stored session last and lets it be shown and
// alerted on. On by default; WODBUSTER_TRUST_DEVICE=false turns it off without
// a release.
func WithTrustDevice(v bool) Option { return func(s *Service) { s.trustDevice = v } }

// WithLoginAttempts sets how many times a run tries to log in before giving
// up, and how long it waits between tries. Values below one keep the default.
func WithLoginAttempts(n int, backoff time.Duration) Option {
	return func(s *Service) {
		if n > 0 {
			s.loginAttempts = n
		}
		if backoff > 0 {
			s.loginBackoff = backoff
		}
	}
}

// WithLoginGap makes logins happen one at a time, each starting at least gap
// after the previous one finished. Zero lets them run side by side.
//
// Every athlete's run starts at 11:50 on the dot, so without it every browser
// hits the login at the same second, which is the pattern most likely to
// draw a Cloudflare challenge. Five athletes at a few seconds each plus 20s
// gaps is under two minutes of the ten the run has before the opening.
func WithLoginGap(gap time.Duration) Option {
	return func(s *Service) { s.gate = newLoginGate(gap) }
}

// Three tries fit comfortably in the ten minutes a run starts ahead of the
// opening: a stuck login gives up after about 45s.
const (
	defaultLoginAttempts = 3
	defaultLoginBackoff  = 5 * time.Second
)

func New(box string, loc *time.Location, log *slog.Logger, opts ...Option) *Service {
	s := &Service{box: box, loc: loc, headless: true, log: log,
		loginAttempts: defaultLoginAttempts, loginBackoff: defaultLoginBackoff,
		trustDevice: true, gate: newLoginGate(0)}
	s.login = s.authenticate
	for _, o := range opts {
		o(s)
	}
	return s
}

// Authenticate logs in once and returns the session. It is what /login needs:
// one try, so wrong credentials are reported straight away, and a session the
// caller can store. A session used every 12 hours has been seen to last more
// than 31 hours, so a stored one can reach Sunday.
func (s *Service) Authenticate(ctx context.Context, email, password string) (wodbuster.Session, error) {
	return s.login(ctx, email, password)
}

// Login logs in the way a run does: up to the configured attempts, through the
// login gate, with onRetry hearing about each failure that will be tried
// again. It is for the Sunday preflight, which must behave exactly like the
// run it prepares.
func (s *Service) Login(ctx context.Context, email, password string,
	onRetry func(attempt, of int, err error)) (wodbuster.Session, error) {
	return s.loginWithRetry(ctx, s.log.With("athlete", email, "box", s.box), email, password, onRetry)
}

// CheckSession asks WodBuster, with one schedule read, whether it still
// accepts sess, and returns the session as the server left it: WodBuster
// renews .WBAuth on ordinary requests, so the returned one is the one to store.
//
// No browser, no login gate. The error says what to do with the session:
//   - wodbuster.ErrSessionExpired: the server refused it. Throw it away.
//   - wodbuster.ErrChallenge: Cloudflare stepped in. The session was not
//     judged; keep it and try later.
//   - anything else (network, timeouts): inconclusive, keep it.
func (s *Service) CheckSession(ctx context.Context, sess wodbuster.Session) (wodbuster.Session, error) {
	client, err := s.newClient(sess)
	if err != nil {
		return wodbuster.Session{}, err
	}
	if err := client.Ping(ctx); err != nil {
		return wodbuster.Session{}, err
	}
	return client.Session(), nil
}

func (s *Service) newClient(sess wodbuster.Session) (*wodbuster.Client, error) {
	opts := []wodbuster.Option{wodbuster.WithLogger(s.log)}
	if s.metrics != nil {
		opts = append(opts,
			wodbuster.WithHTTPClient(s.metrics.httpClientWith(&http.Client{Timeout: 20 * time.Second})))
	}
	return wodbuster.NewClient(sess, append(opts, s.clientOpts...)...)
}

func (s *Service) authenticate(ctx context.Context, email, password string) (wodbuster.Session, error) {
	waited, err := s.gate.acquire(ctx)
	if err != nil {
		return wodbuster.Session{}, fmt.Errorf("waiting for a login slot: %w", err)
	}
	defer s.gate.release()
	if waited > time.Second {
		s.log.Info("login slot acquired", "athlete", email, "waited", waited.Round(time.Second))
	}

	opts := []browserauth.Option{
		browserauth.WithHeadless(s.headless),
		browserauth.WithLogger(s.log),
		browserauth.WithDriftObserver(s.metrics.observeDrift),
		browserauth.WithTrustDevice(s.trustDevice),
	}
	if s.diagnosticsDir != "" {
		opts = append(opts, browserauth.WithDiagnosticsDir(s.diagnosticsDir))
	}
	if s.chromePath != "" {
		opts = append(opts, browserauth.WithChromePath(s.chromePath))
	}

	started := time.Now()
	sess, err := browserauth.New(opts...).
		Authenticate(ctx, s.box, browserauth.Credentials{Email: email, Password: password})
	s.metrics.observeLogin(err, time.Since(started))
	return sess, err
}

// loginGate lets one login through at a time, and not until gap has passed
// since the last one finished.
type loginGate struct {
	slot chan struct{}
	gap  time.Duration

	mu   sync.Mutex
	last time.Time // when the previous login finished
}

func newLoginGate(gap time.Duration) *loginGate {
	return &loginGate{slot: make(chan struct{}, 1), gap: gap}
}

// acquire blocks until it is this caller's turn, and says how long that took.
func (g *loginGate) acquire(ctx context.Context) (time.Duration, error) {
	started := time.Now()
	select {
	case g.slot <- struct{}{}:
	case <-ctx.Done():
		return time.Since(started), ctx.Err()
	}
	g.mu.Lock()
	wait := time.Until(g.last.Add(g.gap))
	g.mu.Unlock()
	if wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			<-g.slot
			return time.Since(started), ctx.Err()
		}
	}
	return time.Since(started), nil
}

func (g *loginGate) release() {
	g.mu.Lock()
	g.last = time.Now()
	g.mu.Unlock()
	<-g.slot
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

	// OnLoginRetry, if set, hears about every failed login that is about to be
	// tried again: attempt n of `of`, and why it failed. A run only returns
	// once the last try is spent, which on 2026-11-15 was after the opening;
	// this is how the people waiting hear about trouble while there is still
	// time to book by hand.
	OnLoginRetry func(attempt, of int, err error)

	// Session, if set, is a stored session to try before logging in. The run
	// checks it with one schedule read; if WodBuster accepts it the run uses
	// it and never opens a browser, otherwise it logs in as usual.
	Session *wodbuster.Session

	// OnSession, if set, is called once the run has a working client, with
	// the session as it stands when the run ends (renewals included), so the
	// caller can store it. reused says whether that is the Session passed in
	// (true) or one from a fresh login (false). It is called even when the
	// chase itself fails, as long as the run got that far.
	OnSession func(sess wodbuster.Session, reused bool)
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

	client, reused, err := s.clientFor(ctx, log, email, password, o)
	if err != nil {
		return nil, err
	}
	if o.OnSession != nil {
		defer func() { o.OnSession(client.Session(), reused) }()
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
			Priority: weekdayPriority(t.Weekday),
		}
		log.Info("target resolved", "class", g.Target.String(), "waitlist", t.Waitlist,
			"priority", g.Priority)
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

// clientFor gives the run a client: from the stored session when WodBuster
// still accepts it, otherwise from a fresh login. reused says which.
func (s *Service) clientFor(ctx context.Context, log *slog.Logger, email, password string,
	o RunOptions) (client *wodbuster.Client, reused bool, err error) {

	if o.Session != nil {
		started := time.Now()
		client, err := s.newClient(*o.Session)
		if err == nil {
			err = client.Ping(ctx)
		}
		if err == nil {
			log.Info("using the stored session",
				"took", time.Since(started).Round(time.Millisecond),
				"logged_in_at", o.Session.IssuedAt, "auth_expires_at", o.Session.AuthExpiry())
			return client, true, nil
		}
		if ctx.Err() != nil {
			return nil, false, err
		}
		log.Warn("stored session not usable; logging in", "err", err,
			"logged_in_at", o.Session.IssuedAt)
	}

	started := time.Now()
	sess, err := s.loginWithRetry(ctx, log, email, password, o.OnLoginRetry)
	if err != nil {
		return nil, false, fmt.Errorf("login failed: %w", err)
	}
	log.Info("logged in", "took", time.Since(started).Round(time.Millisecond),
		"cookies", len(sess.Cookies))
	client, err = s.newClient(sess)
	if err != nil {
		return nil, false, err
	}
	return client, false, nil
}

// loginWithRetry logs in, trying again when the failure might be WodBuster's
// rather than the athlete's. On 2026-10-18 one athlete's login sat on the login
// page after the device prompt until it timed out; the same login had failed
// that way once before and worked on the next try a minute later. The run
// starts ten minutes early precisely so there is time for that.
func (s *Service) loginWithRetry(ctx context.Context, log *slog.Logger,
	email, password string, onRetry func(attempt, of int, err error)) (wodbuster.Session, error) {

	var err error
	for attempt := 1; attempt <= s.loginAttempts; attempt++ {
		var sess wodbuster.Session
		sess, err = s.login(ctx, email, password)
		if err == nil {
			return sess, nil
		}
		if errors.Is(err, browserauth.ErrRejected) || attempt == s.loginAttempts {
			break
		}
		log.Warn("login failed; trying again", "attempt", attempt,
			"of", s.loginAttempts, "in", s.loginBackoff, "err", err)
		if onRetry != nil {
			onRetry(attempt, s.loginAttempts, err)
		}
		select {
		case <-ctx.Done():
			return wodbuster.Session{}, errors.Join(err, ctx.Err())
		case <-time.After(s.loginBackoff):
		}
	}
	return wodbuster.Session{}, err
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

// weekdayPriority books the week in calendar order, Monday first. Monday is
// the class everyone goes for at the opening, so it gets the first booking
// slot; the rest follow in the order they come up. Sunday, if a box ever
// schedules one, goes last.
func weekdayPriority(d time.Weekday) int {
	return (int(d) + 6) % 7
}
