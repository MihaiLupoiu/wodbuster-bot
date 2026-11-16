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

	// admin hears about failures that the athlete alone used to hear about, or
	// nobody did. Zero means no admin.
	admin int64

	// loginCheckSpec is when the daily login check runs, as a cron spec in
	// the gym's zone. Empty, or no admin, turns it off.
	loginCheckSpec string
	loginFailing   atomic.Bool // the last login check failed
}

func NewBookingScheduler(storage Storage, booker Booker, opening Opening,
	encryptionKey string, logger *slog.Logger) *BookingScheduler {

	return &BookingScheduler{
		storage:        storage,
		booker:         booker,
		opening:        opening,
		encryptionKey:  encryptionKey,
		logger:         logger,
		cron:           cron.New(cron.WithLocation(opening.Location)),
		activeBookings: make(map[int64]*BookingContext),
	}
}

// SetMetrics records what the runs do. Called once, at startup, from the same
// place that builds the platform registry.
func (bs *BookingScheduler) SetMetrics(m *Metrics) { bs.metrics = m }

// SetNotifier wires up where results are delivered. Called once, at startup,
// because the bot and the scheduler need each other.
func (bs *BookingScheduler) SetNotifier(n Notifier) { bs.notifier = n }

// SetAdmin names the chat that hears about failures: the daily login check,
// and every athlete's run that goes wrong. Called once, at startup.
func (bs *BookingScheduler) SetAdmin(chatID int64) { bs.admin = chatID }

// SetLoginCheck schedules a daily login with the admin's account, so that a
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

	switch {
	case bs.loginCheckSpec == "":
		bs.logger.Info("daily login check is off")
	case bs.admin == 0:
		bs.logger.Warn("daily login check is off: it logs in as the admin, and there is no admin",
			"cron", bs.loginCheckSpec)
	default:
		if _, err := bs.cron.AddFunc(bs.loginCheckSpec, bs.CheckLogin); err != nil {
			return fmt.Errorf("failed to schedule the login check %q: %w", bs.loginCheckSpec, err)
		}
		bs.logger.Info("daily login check scheduled", "cron", bs.loginCheckSpec, "chat_id", bs.admin)
	}

	bs.cron.Start()
	bs.isRunning = true
	bs.logger.Info("booking scheduler started",
		"cron", spec, "timezone", bs.opening.Location.String(),
		"opening", bs.opening.Next(time.Now()).Format(time.RFC1123))

	// Starting up inside the window means the cron for it has already been and
	// gone — the container was down, restarted, or its host was suspended past
	// the hour. The week is open and the places are going; chase them now
	// rather than next Sunday. Anything already booked comes back as
	// already-booked, so a restart during the window is harmless.
	if bs.opening.InWindow(time.Now()) {
		bs.logger.Info("starting inside the booking window; running now",
			"opened_at", bs.opening.Target(time.Now()).Format(time.RFC1123))
		go bs.processAllBookings()
	}

	return nil
}

func (bs *BookingScheduler) Stop() {
	if !bs.isRunning {
		return
	}

	bs.cron.Stop()
	bs.isRunning = false

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
	opensAt := bs.opening.Target(now)
	bs.logger.Info("booking run starting",
		"opens_at", opensAt.Format(time.RFC1123),
		"already_open", opensAt.Before(now))

	// Lateness is measured against when this run was meant to begin — the
	// opening minus the head start — not against the opening itself.
	bs.metrics.observeRunLateness(opensAt.Add(-bs.opening.StartBefore), now)

	ctx := context.Background()
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

	// The admin hears about the first failed login of the run straight away:
	// one is enough to go and look, and on 2026-11-15 every one of eighteen
	// failed the same way.
	var alertAdmin sync.Once

	var wg sync.WaitGroup
	for chatID, attempts := range byAthlete {
		wg.Add(1)
		go func(chatID int64, attempts []models.BookingAttempt) {
			defer wg.Done()
			bs.runForAthlete(ctx, chatID, attempts, RunSettings{
				OpensAt: opensAt,
				OnLoginRetry: func(attempt, of int, err error) {
					bs.loginTrouble(chatID, opensAt, attempt, of, err, &alertAdmin)
				},
			})
		}(chatID, attempts)
	}
	wg.Wait()
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
		if !u.IsAuthenticated || len(u.ClassBookingSchedules) == 0 {
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
	OnLoginRetry func(attempt, of int, err error)
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
	if !rs.DryRun {
		bs.notify(chatID, bs.startingMessage(rs.OpensAt, len(targets)))
	}

	outcomes, err := bs.booker.Run(runCtx, user.Email, password, targets,
		booking.RunOptions{
			OpensAt:      rs.OpensAt,
			DryRun:       rs.DryRun,
			GiveUpAfter:  rs.GiveUpAfter,
			OnLoginRetry: rs.OnLoginRetry,
		})
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
	if chatID != bs.admin {
		bs.notifyAdmin(fmt.Sprintf("❌ Booking run failed for chat %d (%d class(es)): %v",
			chatID, len(attempts), err))
	}
}

// loginTrouble is a Sunday run's login failing with tries left. The athlete
// hears about it on the first failure, while there is still time before the
// opening to book by hand; the admin hears about the first failure of the
// whole run.
func (bs *BookingScheduler) loginTrouble(chatID int64, opensAt time.Time,
	attempt, of int, err error, alertAdmin *sync.Once) {

	if attempt == 1 {
		bs.notify(chatID, fmt.Sprintf("⚠️ Logging in to WodBuster failed (try %d of %d). "+
			"I'm still trying, but if you don't get your results by %s, book your classes by hand.",
			attempt, of, opensAt.In(bs.opening.Location).Format("15:04")))
	}
	if chatID == bs.admin {
		return
	}
	alertAdmin.Do(func() {
		bs.notifyAdmin(fmt.Sprintf("⚠️ Sunday run: a login failed for chat %d (try %d of %d): %v\n"+
			"Other athletes may follow; each one is told to book by hand if it keeps failing.",
			chatID, attempt, of, err))
	})
}

// CheckLogin logs in with the admin's stored credentials and books nothing.
// It runs daily so that WodBuster changing its login under us — which it did
// between 2026-11-08 and 2026-11-13, found only when the Sunday run failed —
// is found with days to spare. A failure goes to the admin every day it lasts;
// the day it works again says so once.
func (bs *BookingScheduler) CheckLogin() {
	if len(bs.GetActiveBookings()) > 0 {
		bs.logger.Info("skipping the login check during a booking run")
		bs.metrics.observeLoginCheck("skipped")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	user, ok := bs.storage.GetUser(ctx, bs.admin)
	if !ok || user.Email == "" {
		bs.logger.Warn("login check skipped: the admin has no WodBuster login stored",
			"chat_id", bs.admin)
		bs.metrics.observeLoginCheck("skipped")
		bs.notifyAdmin("⚠️ The daily login check needs your WodBuster login. Use /login once.")
		return
	}
	password, err := utils.DecryptPassword(user.Password, bs.encryptionKey)
	if err == nil {
		err = bs.booker.Authenticate(ctx, user.Email, password)
	}
	if err != nil {
		bs.logger.Error("login check failed", "chat_id", bs.admin, "error", err)
		bs.metrics.observeLoginCheck("failed")
		bs.loginFailing.Store(true)
		bs.notifyAdmin("🚨 Daily login check failed: " + err.Error() + "\n\n" +
			"The Sunday run will fail the same way until this is fixed. " +
			"The diagnostics volume has a screenshot of where it stopped.")
		return
	}
	bs.logger.Info("login check passed", "chat_id", bs.admin)
	bs.metrics.observeLoginCheck("passed")
	if bs.loginFailing.Swap(false) {
		bs.notifyAdmin("✅ Daily login check works again.")
	}
}

func (bs *BookingScheduler) notifyAdmin(text string) {
	if bs.admin == 0 {
		return
	}
	bs.notify(bs.admin, text)
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
