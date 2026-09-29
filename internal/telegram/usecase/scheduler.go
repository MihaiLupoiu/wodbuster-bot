package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
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
	cron              *cron.Cron
	notifier          Notifier
	activeBookings    map[int64]*BookingContext
	activeBookingsMux sync.RWMutex
	isRunning         bool
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

// SetNotifier wires up where results are delivered. Called once, at startup,
// because the bot and the scheduler need each other.
func (bs *BookingScheduler) SetNotifier(n Notifier) { bs.notifier = n }

// Start schedules the weekly run, StartBefore ahead of the opening.
func (bs *BookingScheduler) Start() error {
	if bs.isRunning {
		return fmt.Errorf("booking scheduler is already running")
	}

	spec := bs.opening.cronSpec()
	if _, err := bs.cron.AddFunc(spec, bs.processAllBookings); err != nil {
		return fmt.Errorf("failed to schedule cronjob %q: %w", spec, err)
	}

	bs.cron.Start()
	bs.isRunning = true
	bs.logger.Info("booking scheduler started",
		"cron", spec, "timezone", bs.opening.Location.String(),
		"opening", bs.opening.Next(time.Now()).Format(time.RFC1123))

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

// processAllBookings is the weekly run: every athlete with pending classes,
// each in their own goroutine, all of them waiting for the same opening.
func (bs *BookingScheduler) processAllBookings() {
	opensAt := bs.opening.Next(time.Now())
	bs.logger.Info("booking run starting", "opens_at", opensAt.Format(time.RFC1123))

	ctx := context.Background()
	pending, err := bs.storage.GetAllPendingBookings(ctx)
	if err != nil {
		bs.logger.Error("could not read pending bookings", "error", err)
		return
	}
	byAthlete := groupByChat(pending)
	if len(byAthlete) == 0 {
		bs.logger.Info("nothing to book")
		return
	}
	bs.logger.Info("booking for athletes", "athletes", len(byAthlete))

	var wg sync.WaitGroup
	for chatID, attempts := range byAthlete {
		wg.Add(1)
		go func(chatID int64, attempts []models.BookingAttempt) {
			defer wg.Done()
			bs.runForAthlete(ctx, chatID, attempts, RunSettings{OpensAt: opensAt})
		}(chatID, attempts)
	}
	wg.Wait()
	bs.logger.Info("booking run finished")
}

// RunSettings is what distinguishes the Sunday run from a rehearsal.
type RunSettings struct {
	OpensAt time.Time // zero means "now"
	DryRun  bool
}

// runForAthlete books every pending class of one athlete in a single run, so
// they share one login and one clock sync.
func (bs *BookingScheduler) runForAthlete(ctx context.Context, chatID int64,
	attempts []models.BookingAttempt, rs RunSettings) []booking.Outcome {

	runCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
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

	targets := make([]booking.Target, 0, len(attempts))
	for _, a := range attempts {
		wd, err := utils.ParseWeekday(a.Day)
		if err != nil {
			bs.fail(runCtx, chatID, []models.BookingAttempt{a}, err)
			continue
		}
		targets = append(targets, booking.Target{
			Weekday: wd, Start: a.Hour, Class: a.ClassType, Waitlist: true,
		})
	}
	if len(targets) == 0 {
		return nil
	}

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

	outcomes, err := bs.booker.Run(runCtx, user.Email, password, targets,
		booking.RunOptions{OpensAt: rs.OpensAt, DryRun: rs.DryRun})
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
	out := bs.runForAthlete(ctx, chatID, attempts, RunSettings{DryRun: true})
	if out == nil {
		return nil, fmt.Errorf("the rehearsal did not get as far as the classes; check the logs")
	}
	return out, nil
}

func (bs *BookingScheduler) record(ctx context.Context, chatID int64,
	attempts []models.BookingAttempt, outcomes []booking.Outcome, notify bool) {

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
		bs.logger.Info("booking outcome", "chat_id", chatID,
			"class", o.Class, "status", o.Status, "err", o.Err)
	}
	if notify {
		bs.notify(chatID, "🏋️ Booking results\n\n"+strings.Join(lines, "\n"))
	}
}

func (bs *BookingScheduler) fail(ctx context.Context, chatID int64,
	attempts []models.BookingAttempt, err error) {

	bs.logger.Error("booking run failed", "chat_id", chatID, "error", err)
	for _, a := range attempts {
		if a.ID == "" {
			continue
		}
		if uerr := bs.storage.UpdateBookingStatus(ctx, a.ID, "failed", err.Error()); uerr != nil {
			bs.logger.Error("could not record the failure", "booking_id", a.ID, "error", uerr)
		}
	}
	bs.notify(chatID, "❌ Booking run failed: "+err.Error())
}

func (bs *BookingScheduler) notify(chatID int64, text string) {
	if bs.notifier == nil {
		return
	}
	bs.notifier.Notify(chatID, text)
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

func groupByChat(attempts []models.BookingAttempt) map[int64][]models.BookingAttempt {
	out := map[int64][]models.BookingAttempt{}
	for _, a := range attempts {
		out[a.ChatID] = append(out[a.ChatID], a)
	}
	return out
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
	entries := bs.cron.Entries()
	if len(entries) == 0 {
		return time.Time{}
	}
	return entries[0].Next
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
