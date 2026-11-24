package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/booking"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/utils"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

// BookingContext is one athlete's run, while it is happening.
type BookingContext struct {
	ChatID      int64
	BookingData models.BookingWindow
	Cancel      context.CancelFunc
	Status      string
}

// Notifier delivers a result back to the athlete. The bot implements it; a nil
// Notifier is fine, and means the run only reaches the logs.
type Notifier interface {
	Notify(chatID int64, text string)
}

// BookingScheduler wakes up before the opening and books for everyone.
//
// One goroutine per athlete: WodBuster serialises booking calls per athlete,
// not globally, so athletes do not slow each other down. Within an athlete,
// the client queues its own calls — see pkg/wodbuster.
type BookingScheduler struct {
	storage           Storage
	booker            Booker
	opening           Opening
	encryptionKey     string
	logger            *slog.Logger
	metrics           *Metrics
	cron              *cron.Cron
	notifier          Notifier
	activeBookings    map[int64]*BookingContext
	activeBookingsMux sync.RWMutex
	isRunning         bool

	// weeklyRun is the cron entry of the Sunday run, so that "next run" is
	// that one and not the daily login check.
	weeklyRun cron.EntryID

	// loginCheckSpec is when the daily login check runs, as a cron spec in
	// the gym's zone. Empty turns it off.
	loginCheckSpec string
	loginFailing   atomic.Bool // the last login check failed

	// sessionRefreshSpec is when stored sessions are checked and renewed, as
	// a cron spec in the gym's zone. Empty turns it off. refreshGap spaces
	// the athletes' checks apart.
	sessionRefreshSpec string
	refreshGap         time.Duration
	refreshGapJitter   time.Duration
	refreshStartJitter time.Duration

	// preflightSpec is when the Sunday session preflight runs; reuseMode is
	// SESSION_REUSE (off, shadow). See preflight.go.
	preflightSpec        string
	reuseMode            ReuseMode
	preflightQuietWhenOK bool
	shadowJitter         time.Duration
	// preflightStarted is when this week's preflight began, as Unix nanos;
	// the run books only with a session logged in since.
	preflightStarted atomic.Int64

	// weekRunning is set while the weekly run is going, so neither the
	// preflight nor the refresh starts during one. The run stops either one
	// that is still going, through preflightJob and refreshJob.
	weekRunning  atomic.Bool
	preflightJob runBlocker
	refreshJob   runBlocker

	// gaugesMu serialises updateSessionGauges.
	gaugesMu sync.Mutex

	// life ends when the scheduler stops, and with it every job that
	// derives its context from it.
	life     context.Context
	stopLife context.CancelFunc
}

func NewBookingScheduler(storage Storage, booker Booker, opening Opening,
	encryptionKey string, logger *slog.Logger) *BookingScheduler {

	life, stopLife := context.WithCancel(context.Background())
	return &BookingScheduler{
		storage:            storage,
		booker:             booker,
		opening:            opening,
		encryptionKey:      encryptionKey,
		logger:             logger,
		cron:               cron.New(cron.WithLocation(opening.Location)),
		activeBookings:     make(map[int64]*BookingContext),
		refreshGap:         defaultRefreshGap,
		refreshGapJitter:   defaultRefreshGapJitter,
		refreshStartJitter: defaultRefreshStartJitter,
		shadowJitter:       defaultShadowJitter,
		// The app sets the configured mode; off keeps a scheduler built
		// without one (tests, tools) on the old behaviour.
		reuseMode: ReuseOff,
		life:      life,
		stopLife:  stopLife,
	}
}

// SetMetrics records what the runs do. Called once, at startup, from the same
// place that builds the platform registry.
func (bs *BookingScheduler) SetMetrics(m *Metrics) { bs.metrics = m }

// SetNotifier wires up where results are delivered. Called once, at startup,
// because the bot and the scheduler need each other.
func (bs *BookingScheduler) SetNotifier(n Notifier) { bs.notifier = n }

// SetLoginCheck schedules a daily login with an admin's account, so that a
// login WodBuster has broken is found days before the opening rather than
// during it. spec is a cron spec in the gym's zone; empty turns it off. Called
// once, before Start.
func (bs *BookingScheduler) SetLoginCheck(spec string) { bs.loginCheckSpec = spec }

// Start schedules the weekly run, StartBefore ahead of the opening.
func (bs *BookingScheduler) Start() error {
	if bs.isRunning {
		return fmt.Errorf("booking scheduler is already running")
	}

	spec := bs.opening.cronSpec()
	id, err := bs.cron.AddFunc(spec, bs.processAllBookings)
	if err != nil {
		return fmt.Errorf("failed to schedule cronjob %q: %w", spec, err)
	}
	bs.weeklyRun = id

	if bs.loginCheckSpec == "" {
		bs.logger.Info("daily login check is off")
	} else {
		if _, err := bs.cron.AddFunc(bs.loginCheckSpec, bs.CheckLogin); err != nil {
			return fmt.Errorf("failed to schedule the login check %q: %w", bs.loginCheckSpec, err)
		}
		bs.logger.Info("daily login check scheduled", "cron", bs.loginCheckSpec)
	}

	if bs.sessionRefreshSpec == "" || !bs.storesSessions() {
		bs.logger.Info("session refresh is off", "session_reuse", bs.reuseMode)
	} else {
		if _, err := bs.cron.AddFunc(bs.sessionRefreshSpec, bs.RefreshSessions); err != nil {
			return fmt.Errorf("failed to schedule the session refresh %q: %w", bs.sessionRefreshSpec, err)
		}
		bs.logger.Info("session refresh scheduled", "cron", bs.sessionRefreshSpec)
	}

	if bs.preflightSpec == "" || bs.reuseMode == ReuseOff {
		bs.logger.Info("session preflight is off", "session_reuse", bs.reuseMode)
	} else {
		if _, err := bs.cron.AddFunc(bs.preflightSpec, bs.Preflight); err != nil {
			return fmt.Errorf("failed to schedule the session preflight %q: %w", bs.preflightSpec, err)
		}
		bs.logger.Info("session preflight scheduled", "cron", bs.preflightSpec, "session_reuse", bs.reuseMode)
	}

	bs.cron.Start()
	bs.isRunning = true
	bs.metrics.setReuseMode(bs.reuseMode)
	bs.logger.Info("booking scheduler started",
		"cron", spec, "timezone", bs.opening.Location.String(),
		"opening", bs.opening.Next(time.Now()).Format(time.RFC1123))

	// Starting up inside the window means the cron for it has already been and
	// gone — the container was down, restarted, or its host was suspended past
	// the hour. The week is open and the places are going; chase them now
	// rather than next Sunday. Anything already booked comes back as
	// already-booked, so a restart during the window is harmless. First, so
	// nothing below delays it.
	if bs.opening.InWindow(time.Now()) {
		bs.logger.Info("starting inside the booking window; running now",
			"opened_at", bs.opening.Target(time.Now()).Format(time.RFC1123))
		go bs.processAllBookings()
	}

	if bs.storesSessions() {
		// The session gauges are otherwise set by the first refresh, up to
		// 12 hours away; a restart would read as "no sessions" until then.
		// In the background: a slow store must not hold up the bot.
		go bs.updateSessionGauges(bs.life)
	} else {
		// Sessions left from shadow weeks: nothing reads them now, and
		// nothing else would ever delete them.
		go func() {
			ctx, cancel := context.WithTimeout(bs.life, time.Minute)
			defer cancel()
			bs.dropAllSessions(ctx)
		}()
	}

	return nil
}

func (bs *BookingScheduler) Stop() {
	if !bs.isRunning {
		return
	}

	bs.cron.Stop()
	bs.isRunning = false
	// Ends a preflight, refresh or login check in flight; their contexts
	// derive from life.
	bs.stopLife()

	bs.activeBookingsMux.Lock()
	for chatID, b := range bs.activeBookings {
		b.Cancel()
		bs.logger.Info("cancelled active booking", "chat_id", chatID)
	}
	bs.activeBookings = make(map[int64]*BookingContext)
	bs.activeBookingsMux.Unlock()

	bs.logger.Info("booking scheduler stopped")
}

// processAllBookings is the weekly run: every athlete with classes on their
// schedule, each in their own goroutine, all of them waiting for the same
// opening.
//
// The schedule is the source of truth, not the attempts. Attempts used to be
// the trigger — a "pending" row per /book — and nothing ever set them back to
// pending, so every class was booked once and then never again. On 2026-10-18
// that left two of five athletes out of the run without a word.
func (bs *BookingScheduler) processAllBookings() { bs.runWeek(time.Now()) }

// runWeek is the weekly run as of now; separate so a test can run two weeks.
func (bs *BookingScheduler) runWeek(now time.Time) {
	bs.weekRunning.Store(true)
	defer bs.weekRunning.Store(false)
	// The preflight's deadline is this moment, so it is normally done, and the
	// refresh is normally hours away; whichever is still going stops here
	// rather than logging in alongside the run.
	bs.stopRunBlockers()
	// Whatever the run stored, the gauges show it afterwards.
	defer bs.updateSessionGauges(bs.life)

	opensAt := bs.opening.Target(now)
	bs.logger.Info("booking run starting",
		"opens_at", opensAt.Format(time.RFC1123),
		"already_open", opensAt.Before(now))

	// Lateness is measured against when this run was meant to begin — the
	// opening minus the head start — not against the opening itself.
	bs.metrics.observeRunLateness(opensAt.Add(-bs.opening.StartBefore), now)

	// Stop cancels the runs in flight as well; their recording detaches.
	ctx := bs.life
	users, err := bs.storage.ListUsers(ctx)
	if err != nil {
		bs.logger.Error("could not read the athletes", "error", err)
		bs.metrics.observeRunFinished("failed", time.Since(now))
		return
	}
	byAthlete := bs.weekAttempts(ctx, users, opensAt)
	bs.metrics.observeRunStart(countAttempts(byAthlete))
	if len(byAthlete) == 0 {
		bs.logger.Info("nothing to book")
		// A run with nothing to do still ran: the alert that matters is "no run
		// happened", and an empty week must not trip it.
		bs.metrics.observeRunFinished("completed", time.Since(now))
		return
	}
	bs.logger.Info("booking for athletes", "athletes", len(byAthlete))

	// The admins hear about the first failed login of the run straight away:
	// one is enough to go and look, and on 2026-11-15 every one of eighteen
	// failed the same way.
	var alerts loginAlerts

	var wg sync.WaitGroup
	for chatID, attempts := range byAthlete {
		wg.Go(func() {
			bs.runForAthlete(ctx, chatID, attempts, RunSettings{
				OpensAt: opensAt,
				OnLoginRetry: func(attempt, of int, err error) {
					bs.loginTrouble(chatID, opensAt, attempt, of, err, &alerts)
				},
				alerts: &alerts,
			})
		})
	}
	wg.Wait()
	alerts.wait()
	bs.metrics.observeRunFinished("completed", time.Since(now))
	bs.logger.Info("booking run finished", "took", time.Since(now).Round(time.Millisecond))
}

// weekAttempts turns every authenticated athlete's schedule into this week's
// attempts, and writes them down before anything is tried.
//
// An attempt's id names the athlete, the class and the opening, so it is the
// same however many times the run starts in one week: a restart inside the
// window overwrites this week's row instead of adding a second one, and next
// week gets a fresh row of its own. The rows are history; nothing reads them to
// decide what to book.
func (bs *BookingScheduler) weekAttempts(ctx context.Context, users []models.User,
	opensAt time.Time) map[int64][]models.BookingAttempt {

	week := opensAt.Format("2006-01-02")
	out := map[int64][]models.BookingAttempt{}
	for _, u := range users {
		if !bookable(u) {
			continue
		}
		for _, c := range u.ClassBookingSchedules {
			now := time.Now()
			a := models.BookingAttempt{
				ID:          fmt.Sprintf("%d-%s-%s", u.ChatID, c.ID, week),
				ChatID:      u.ChatID,
				Day:         c.Day,
				Hour:        c.Hour,
				ClassType:   c.ClassType,
				Status:      "pending",
				AttemptTime: opensAt,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			if err := bs.storage.SaveBookingAttempt(ctx, a); err != nil {
				// Not fatal: the booking matters more than its paper trail.
				bs.logger.Error("could not record this week's attempt",
					"booking_id", a.ID, "error", err)
				a.ID = ""
			}
			out[u.ChatID] = append(out[u.ChatID], a)
		}
	}
	return out
}

func countAttempts(byAthlete map[int64][]models.BookingAttempt) int {
	n := 0
	for _, as := range byAthlete {
		n += len(as)
	}
	return n
}

// RunSettings is what distinguishes the Sunday run from a rehearsal.
type RunSettings struct {
	OpensAt time.Time // zero, or past, means "now"
	DryRun  bool

	// GiveUpAfter bounds the chase. Zero leaves the library's default.
	GiveUpAfter time.Duration

	// OnLoginRetry hears about each failed login that will be tried again.
	OnLoginRetry booking.LoginRetryFunc

	// alerts holds the warnings OnLoginRetry sent, which the athlete must
	// get before their results.
	alerts *loginAlerts
}

// runForAthlete books every pending class of one athlete in a single run, so
// they share one login and one clock sync.
func (bs *BookingScheduler) runForAthlete(ctx context.Context, chatID int64,
	attempts []models.BookingAttempt, rs RunSettings) []booking.Outcome {

	// Long enough to log in, wait out whatever head start is left, and chase;
	// not so long that a wrong opening sits here for twenty minutes.
	budget := 10*time.Minute + time.Until(rs.OpensAt)
	if budget < 10*time.Minute {
		budget = 10 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	user, exists := bs.storage.GetUser(runCtx, chatID)
	if !exists {
		bs.logger.Error("athlete not found", "chat_id", chatID)
		return nil
	}
	password, err := utils.DecryptPassword(user.Password, bs.encryptionKey)
	if err != nil {
		bs.fail(runCtx, chatID, attempts, fmt.Errorf("could not read the stored password: %w", err))
		return nil
	}

	// planned keeps attempts and targets in step. They used to be two slices
	// zipped by index, which quietly misaligned as soon as one day name failed
	// to parse and recorded an outcome against the wrong booking.
	var planned []models.BookingAttempt
	var targets []booking.Target
	for _, a := range attempts {
		wd, err := utils.ParseWeekday(a.Day)
		if err != nil {
			bs.fail(runCtx, chatID, []models.BookingAttempt{a}, err)
			continue
		}
		planned = append(planned, a)
		targets = append(targets, booking.Target{
			Weekday: wd, Start: a.Hour, Class: a.ClassType, Waitlist: true,
		})
	}
	if len(targets) == 0 {
		return nil
	}
	attempts = planned

	bs.track(chatID, attempts[0], cancel)
	defer bs.untrack(chatID)
	for _, a := range attempts {
		// A rehearsal's attempts are synthetic and have no id; there is
		// nothing in storage to mark, and nothing happened to record.
		if a.ID == "" {
			continue
		}
		if err := bs.storage.UpdateBookingStatus(runCtx, a.ID, "active", ""); err != nil {
			bs.logger.Error("could not mark the booking active", "booking_id", a.ID, "error", err)
		}
	}

	// Said before the login, so that silence at noon means the run never
	// started, not that it is still going.
	var starting sync.WaitGroup
	if !rs.DryRun {
		msg := bs.startingMessage(rs.OpensAt, len(targets))
		if time.Now().Before(rs.OpensAt) {
			bs.notify(chatID, msg)
		} else {
			// Already open (a restart inside the window): every second
			// counts, so the message must not hold up the first request.
			// It is waited for before the results, which must come after it.
			starting.Go(func() { bs.notify(chatID, msg) })
		}
	}

	opts := booking.RunOptions{
		OpensAt:      rs.OpensAt,
		DryRun:       rs.DryRun,
		GiveUpAfter:  rs.GiveUpAfter,
		OnLoginRetry: rs.OnLoginRetry,
	}
	// A real run books with this morning's preflight session when there is
	// one, and keeps whatever session it ends with. Rehearsals log in as
	// before until phase 2.
	var ended *endedSession
	if !rs.DryRun && bs.storesSessions() {
		opts.Session = bs.runSession(runCtx, chatID, time.Now())
		ended = &endedSession{offered: opts.Session != nil}
		opts.OnSession = ended.set
	}
	outcomes, err := bs.booker.Run(runCtx, user.Email, password, targets, opts)
	starting.Wait()
	rs.alerts.waitFor(chatID)
	// Stored after the results are recorded and sent: the athlete's answer
	// must not wait on the database.
	defer func() {
		if sess, reused, ok := ended.get(); ok {
			bs.storeRunSession(runCtx, chatID, ended.offered, sess, reused)
		}
	}()
	if err != nil {
		if rs.DryRun {
			// The caller is standing in Telegram waiting for an answer; it
			// reports this one itself rather than having it arrive twice.
			bs.logger.Error("rehearsal failed", "chat_id", chatID, "error", err)
			return nil
		}
		bs.fail(runCtx, chatID, attempts, err)
		return nil
	}

	bs.record(runCtx, chatID, attempts, outcomes, !rs.DryRun)
	return outcomes
}

// Rehearse runs an athlete's classes now, booking nothing. It is how you find
// out on a Tuesday whether Sunday will work.
func (bs *BookingScheduler) Rehearse(ctx context.Context, chatID int64) ([]booking.Outcome, error) {
	schedules, ok := bs.storage.GetClassBookingSchedules(ctx, chatID)
	if !ok || len(schedules) == 0 {
		return nil, fmt.Errorf("no classes scheduled")
	}
	attempts := make([]models.BookingAttempt, 0, len(schedules))
	for _, c := range schedules {
		attempts = append(attempts, models.BookingAttempt{
			ChatID: chatID, Day: c.Day, Hour: c.Hour, ClassType: c.ClassType,
		})
	}
	out := bs.runForAthlete(ctx, chatID, attempts,
		RunSettings{DryRun: true, GiveUpAfter: 5 * time.Second})
	if out == nil {
		return nil, fmt.Errorf("the rehearsal did not get as far as the classes; check the logs")
	}
	return out, nil
}

func (bs *BookingScheduler) record(ctx context.Context, chatID int64,
	attempts []models.BookingAttempt, outcomes []booking.Outcome, notify bool) {

	ctx, cancel := recordingContext(ctx)
	defer cancel()

	var lines []string
	for i, o := range outcomes {
		status, msg := "success", ""
		if !o.OK() {
			status = "failed"
			if o.Err != nil {
				msg = o.Err.Error()
			}
		}
		if i < len(attempts) && attempts[i].ID != "" {
			if err := bs.storage.UpdateBookingStatus(ctx, attempts[i].ID, status, msg); err != nil {
				bs.logger.Error("could not record the outcome", "booking_id", attempts[i].ID, "error", err)
			}
		}
		lines = append(lines, formatOutcome(o))
		if i < len(attempts) {
			bs.metrics.observeOutcome(o, attempts[i].Day, attempts[i].ClassType)
		}
		bs.logger.Info("booking outcome", "chat_id", chatID,
			"class", o.Class, "status", o.Status, "err", o.Err)
	}
	if notify {
		bs.notify(chatID, "🏋️ Booking results\n\n"+strings.Join(lines, "\n"))
	}
}

func (bs *BookingScheduler) fail(ctx context.Context, chatID int64,
	attempts []models.BookingAttempt, err error) {

	// Detached on purpose: the usual reason a run fails is that its context
	// expired, and reusing it here means the failure cannot be written down.
	ctx, cancel := recordingContext(ctx)
	defer cancel()

	bs.logger.Error("booking run failed", "chat_id", chatID, "error", err)
	bs.metrics.observeRunFailure(err, len(attempts))
	for _, a := range attempts {
		if a.ID == "" {
			continue
		}
		if uerr := bs.storage.UpdateBookingStatus(ctx, a.ID, "failed", err.Error()); uerr != nil {
			bs.logger.Error("could not record the failure", "booking_id", a.ID, "error", uerr)
		}
	}
	bs.notify(chatID, "❌ Booking run failed: "+err.Error()+
		"\n\nNothing was booked for you this time. Book your classes by hand on WodBuster.")
	bs.notifyAdmins(ctx, chatID, fmt.Sprintf("❌ Booking run failed for chat %d (%d class(es)): %v",
		chatID, len(attempts), err))
}

// loginTrouble is a Sunday run's login failing with tries left. The athlete
// hears about it on the first failure, while there is still time before the
// opening to book by hand; the admins hear about the first failure of the
// whole run.
func (bs *BookingScheduler) loginTrouble(chatID int64, opensAt time.Time,
	attempt, of int, err error, alerts *loginAlerts) {

	if attempt == 1 {
		alerts.toAthlete(chatID, func() {
			bs.notify(chatID, fmt.Sprintf("⚠️ Logging in to WodBuster failed (try %d of %d). "+
				"I'm still trying, but if you don't get your results by %s, book your classes by hand.",
				attempt, of, opensAt.In(bs.opening.Location).Format("15:04")))
		})
	}
	alerts.toAdminsOnce(func() {
		ctx, cancel := context.WithTimeout(bs.life, 10*time.Second)
		defer cancel()
		bs.notifyAdmins(ctx, chatID, fmt.Sprintf("⚠️ Sunday run: a login failed for chat %d (try %d of %d): %v\n"+
			"Other athletes may follow; each one is told to book by hand if it keeps failing.",
			chatID, attempt, of, err))
	})
}

// CheckLogin logs in with the first active admin's stored credentials and
// books nothing. It runs daily so that WodBuster changing its login under us —
// which it did between 2026-11-08 and 2026-11-13, found only when the Sunday
// run failed — is found with days to spare. A failure goes to the admins every
// day it lasts; the day it works again says so once. With no admin there is
// no account to check with and nobody to tell, so it does nothing.
func (bs *BookingScheduler) CheckLogin() {
	if len(bs.GetActiveBookings()) > 0 {
		bs.logger.Info("skipping the login check during a booking run")
		bs.metrics.observeLoginCheck("skipped")
		return
	}
	ctx, cancel := context.WithTimeout(bs.life, 5*time.Minute)
	defer cancel()

	users, err := bs.storage.ListUsers(ctx)
	if err != nil {
		bs.logger.Error("login check skipped: could not read the users", "error", err)
		bs.metrics.observeLoginCheck("skipped")
		return
	}
	admins := models.ActiveAdmins(users)
	if len(admins) == 0 {
		bs.logger.Warn("login check skipped: there is no active admin")
		bs.metrics.observeLoginCheck("skipped")
		return
	}
	user := admins[0]
	password, err := utils.DecryptPassword(user.Password, bs.encryptionKey)
	var sess wodbuster.Session
	if err == nil {
		sess, err = bs.booker.Authenticate(ctx, user.Email, password)
	}
	if err != nil {
		bs.logger.Error("login check failed", "chat_id", user.ChatID, "error", err)
		bs.metrics.observeLoginCheck("failed")
		bs.loginFailing.Store(true)
		bs.tellAdmins(admins, 0, "🚨 Daily login check failed for "+user.Email+": "+err.Error()+"\n\n"+
			"The Sunday run will fail the same way until this is fixed. "+
			"The diagnostics volume has a screenshot of where it stopped.")
		return
	}
	bs.logger.Info("login check passed", "chat_id", user.ChatID)
	// Kept, since it is the newest session the admin has. It does not save
	// a remembered device: every login with "remember this device" adds one
	// on WodBuster's side, kept or not.
	bs.storeFreshSession(ctx, user, sess, sourceLoginCheck)
	bs.metrics.observeLoginCheck("passed")
	if bs.loginFailing.Swap(false) {
		bs.tellAdmins(admins, 0, "✅ Daily login check works again.")
	}
}

// notifyAdmins tells every active admin, except the one whose own run it is:
// that admin has already heard about it as an athlete. No admin, no message.
func (bs *BookingScheduler) notifyAdmins(ctx context.Context, except int64, text string) {
	users, err := bs.storage.ListUsers(ctx)
	if err != nil {
		bs.logger.Error("could not find the admins to tell", "error", err)
		return
	}
	bs.tellAdmins(models.ActiveAdmins(users), except, text)
}

func (bs *BookingScheduler) tellAdmins(admins []models.User, except int64, text string) {
	for _, a := range admins {
		if a.ChatID != except {
			bs.notify(a.ChatID, text)
		}
	}
}

// recordingContext detaches from a context that may already be done, so that
// results and failures still reach storage.
func recordingContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
}

func (bs *BookingScheduler) notify(chatID int64, text string) {
	if bs.notifier == nil {
		return
	}
	bs.notifier.Notify(chatID, text)
}

func (bs *BookingScheduler) startingMessage(opensAt time.Time, classes int) string {
	if opensAt.After(time.Now()) {
		return fmt.Sprintf("🔐 Logging in to book %d class(es) when the week opens at %s. Results will follow.",
			classes, opensAt.In(bs.opening.Location).Format("15:04 MST"))
	}
	return fmt.Sprintf("🔐 Logging in to book %d class(es); the week is already open. Results will follow.",
		classes)
}

func formatOutcome(o booking.Outcome) string {
	switch o.Status {
	case "booked":
		return "✅ " + o.Class
	case "already-booked":
		return "✅ " + o.Class + " (already booked)"
	case "waitlisted":
		return "⏳ " + o.Class + " (waiting list)"
	case "dry-run":
		return "🧪 " + o.Class + " (rehearsal: found and bookable)"
	default:
		if o.Err != nil {
			return "❌ " + o.Class + ": " + o.Err.Error()
		}
		return "❌ " + o.Class
	}
}

func (bs *BookingScheduler) track(chatID int64, first models.BookingAttempt, cancel context.CancelFunc) {
	bs.activeBookingsMux.Lock()
	defer bs.activeBookingsMux.Unlock()
	bs.activeBookings[chatID] = &BookingContext{
		ChatID: chatID,
		BookingData: models.BookingWindow{
			Day: first.Day, Hour: first.Hour, ClassType: first.ClassType,
			OpensAt: bs.opening.Next(time.Now()),
		},
		Cancel: cancel,
		Status: "active",
	}
}

func (bs *BookingScheduler) untrack(chatID int64) {
	bs.activeBookingsMux.Lock()
	defer bs.activeBookingsMux.Unlock()
	delete(bs.activeBookings, chatID)
}

// GetActiveBookings returns a copy of the runs in flight.
func (bs *BookingScheduler) GetActiveBookings() map[int64]*BookingContext {
	bs.activeBookingsMux.RLock()
	defer bs.activeBookingsMux.RUnlock()

	result := make(map[int64]*BookingContext, len(bs.activeBookings))
	for k, v := range bs.activeBookings {
		result[k] = v
	}
	return result
}

// CancelBooking stops an athlete's run in flight.
func (bs *BookingScheduler) CancelBooking(chatID int64) bool {
	bs.activeBookingsMux.Lock()
	defer bs.activeBookingsMux.Unlock()

	if b, exists := bs.activeBookings[chatID]; exists {
		b.Cancel()
		b.Status = "cancelled"
		delete(bs.activeBookings, chatID)
		bs.logger.Info("cancelled booking", "chat_id", chatID)
		return true
	}
	return false
}

func (bs *BookingScheduler) IsRunning() bool { return bs.isRunning }

// GetNextRunTime is when the scheduler next wakes up.
func (bs *BookingScheduler) GetNextRunTime() time.Time {
	if !bs.isRunning {
		return time.Time{}
	}
	return bs.cron.Entry(bs.weeklyRun).Next
}

// GetScheduleInfo is the human-readable version, for /schedule.
func (bs *BookingScheduler) GetScheduleInfo() string {
	if !bs.isRunning {
		return "Scheduler is not running"
	}
	opensAt := bs.opening.Next(time.Now())
	nextRun := bs.GetNextRunTime()
	if nextRun.IsZero() {
		return "No scheduled runs found"
	}
	return fmt.Sprintf("Classes open %s (in %s).\nThe bot wakes up at %s to be ready.",
		opensAt.Format("Monday, January 2 at 15:04 MST"),
		time.Until(opensAt).Round(time.Minute),
		nextRun.Format("Monday, January 2 at 15:04 MST"))
}
