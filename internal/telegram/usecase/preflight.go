package usecase

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/utils"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

// Session reuse modes (SESSION_REUSE).
const (
	// ReuseOff is the old behaviour: no preflight, every run logs in at 11:50.
	ReuseOff = "off"
	// ReuseShadow logs everyone in at 11:00 and books with that session. The
	// days-old stored session is only checked, never used to book: the shadow
	// week measures whether it would have worked.
	ReuseShadow = "shadow"
	// ReuseLive is phase 2 and not built yet: the preflight would book with the
	// stored session when it works, and log in only when it does not.
	ReuseLive = "live"
)

// ValidReuseMode checks a SESSION_REUSE value. live is refused until phase 2 is
// built, rather than silently behaving like shadow.
func ValidReuseMode(mode string) error {
	switch mode {
	case ReuseOff, ReuseShadow:
		return nil
	case ReuseLive:
		return fmt.Errorf("SESSION_REUSE=live is phase 2 of the session reuse plan and not built yet; use shadow")
	default:
		return fmt.Errorf("SESSION_REUSE=%q: want off or shadow", mode)
	}
}

// Results of the shadow check and of the run's use of a session, for
// session_reuse_total.
const (
	reuseWouldWork    = "would_work"
	reuseWouldFail    = "would_fail"
	reuseInconclusive = "inconclusive"
	reuseMissing      = "missing"
	reuseUsed         = "used"
	reuseFallback     = "fallback"
)

const sourcePreflight = "preflight"

// freshLoginWindow is how recent a stored session's login must be for a shadow
// week's run to book with it. The preflight logs in at 11:00 and the run starts
// at 11:50, so two hours takes the preflight's session and never a days-old one.
const freshLoginWindow = 2 * time.Hour

// SetSessionPreflight schedules the Sunday preflight and sets the reuse mode.
// spec is a cron spec in the gym's zone; empty turns the preflight off. Called
// once, before Start.
func (bs *BookingScheduler) SetSessionPreflight(spec, mode string) {
	bs.preflightSpec = spec
	bs.reuseMode = mode
}

// bookable is whether the Sunday run books for this user. The preflight and
// the run must agree on it.
func bookable(u models.User) bool {
	return u.IsAuthenticated && u.IsActive() && len(u.ClassBookingSchedules) > 0
}

// preflightResult is one athlete's preflight.
type preflightResult struct {
	user     models.User
	shadow   string // reuseWouldWork, reuseWouldFail, reuseInconclusive, reuseMissing
	loggedIn bool
	err      error
}

// Preflight is the Sunday session preflight, run by cron at 11:00.
func (bs *BookingScheduler) Preflight() { bs.preflight(time.Now()) }

// preflight logs every athlete in about an hour before the opening, with the
// run's own login logic, and stores the session for the run to use.
//
// It moves a broken login from 11:50 to 11:00: the first failed attempt warns
// the athlete and the admins within a minute, and even a login that takes the
// full 3 attempts for every athlete (about 30 minutes, as on 2026-11-15) ends
// before the run starts. The run then only logs in again for athletes left
// without a session.
//
// Before logging in it checks the athlete's stored session, the one the
// 12-hourly refresh has been keeping alive, and records whether it would have
// worked. In shadow mode that is all that happens to it.
//
// It never overlaps the run: its deadline is the run's start, and the run
// stops it if it is still going.
func (bs *BookingScheduler) preflight(now time.Time) {
	if bs.reuseMode != ReuseShadow {
		bs.logger.Info("session preflight skipped", "session_reuse", bs.reuseMode)
		return
	}
	if bs.weekRunning.Load() || len(bs.GetActiveBookings()) > 0 {
		bs.logger.Info("session preflight skipped: the booking run is already going")
		return
	}
	opensAt := bs.opening.Target(now)
	runStart := opensAt.Add(-bs.opening.StartBefore)
	if !now.Before(runStart) {
		bs.logger.Info("session preflight skipped: the run is due", "run_start", runStart)
		return
	}

	ctx, cancel := context.WithDeadline(context.Background(), runStart)
	done := make(chan struct{})
	bs.preflightMu.Lock()
	bs.preflightCancel, bs.preflightDone = cancel, done
	bs.preflightMu.Unlock()
	defer func() {
		cancel()
		bs.preflightMu.Lock()
		bs.preflightCancel, bs.preflightDone = nil, nil
		bs.preflightMu.Unlock()
		close(done)
	}()

	started := time.Now()
	users, err := bs.storage.ListUsers(ctx)
	if err != nil {
		bs.logger.Error("session preflight failed: could not read the users", "error", err)
		bs.metrics.observePreflight("failed")
		return
	}
	sort.Slice(users, func(i, j int) bool { return users[i].ChatID < users[j].ChatID })
	admins := models.ActiveAdmins(users)

	var athletes []models.User
	for _, u := range users {
		if bookable(u) {
			athletes = append(athletes, u)
		}
	}
	if len(athletes) == 0 {
		bs.logger.Info("session preflight: nobody to book for")
		return
	}
	bs.logger.Info("session preflight starting", "athletes", len(athletes),
		"run_start", runStart.Format(time.RFC1123))

	// Each athlete gets their own goroutine, as in the run; the login gate
	// still lets one browser through at a time, so this only stops one slow
	// athlete from holding up the shadow checks of the others.
	var alertAdmin sync.Once
	results := make([]preflightResult, len(athletes))
	var wg sync.WaitGroup
	for i, u := range athletes {
		wg.Add(1)
		go func(i int, u models.User) {
			defer wg.Done()
			results[i] = bs.preflightOne(ctx, u, runStart, opensAt, &alertAdmin)
		}(i, u)
	}
	wg.Wait()

	bs.reportPreflight(results, admins, time.Since(started))
}

func (bs *BookingScheduler) preflightOne(ctx context.Context, u models.User,
	runStart, opensAt time.Time, alertAdmin *sync.Once) preflightResult {

	res := preflightResult{user: u}
	log := bs.logger.With("chat_id", u.ChatID, "source", sourcePreflight)

	res.shadow = bs.shadowCheck(ctx, u)

	password, err := utils.DecryptPassword(u.Password, bs.encryptionKey)
	if err != nil {
		res.err = fmt.Errorf("could not read the stored password: %w", err)
		log.Error("preflight login impossible", "error", res.err)
		return res
	}
	sess, err := bs.booker.Login(ctx, u.Email, password, func(attempt, of int, err error) {
		bs.preflightTrouble(u.ChatID, runStart, opensAt, attempt, of, err, alertAdmin)
	})
	if err != nil {
		res.err = err
		if ctx.Err() != nil {
			// The run started and took over; it logs in itself.
			log.Warn("preflight login cut short by the run", "error", err)
			return res
		}
		log.Error("preflight login failed", "error", err)
		bs.notify(u.ChatID, fmt.Sprintf("⚠️ Logging in to WodBuster failed at %s (%v). "+
			"The %s run will try again; if you hear nothing by %s, book your classes by hand.",
			time.Now().In(bs.opening.Location).Format("15:04"), err,
			runStart.In(bs.opening.Location).Format("15:04"),
			opensAt.Add(5*time.Minute).In(bs.opening.Location).Format("15:04")))
		return res
	}

	st, err := sealSession(bs.encryptionKey, u.ChatID, sess, time.Now())
	if err == nil {
		err = bs.storage.SaveSession(ctx, st)
	}
	if err != nil {
		// The run will log in again; nothing for the athlete to do.
		res.err = fmt.Errorf("logged in, but could not store the session: %w", err)
		log.Error("preflight could not store the session", "error", err)
		return res
	}
	res.loggedIn = true
	log.Info("preflight logged in", "auth_expires_at", st.AuthExpiresAt, "trusted_device", st.TrustedDevice)
	return res
}

// shadowCheck asks whether the athlete's stored session would have worked, and
// records the answer. It never books with it.
func (bs *BookingScheduler) shadowCheck(ctx context.Context, u models.User) string {
	log := bs.logger.With("chat_id", u.ChatID, "source", sourcePreflight)
	st, ok, err := bs.storage.GetSession(ctx, u.ChatID)
	if err != nil {
		log.Error("shadow check: could not read the stored session", "error", err)
		bs.metrics.observeSessionReuse(ReuseShadow, reuseInconclusive)
		return reuseInconclusive
	}
	if !ok {
		log.Info("shadow check: no stored session")
		bs.metrics.observeSessionReuse(ReuseShadow, reuseMissing)
		return reuseMissing
	}

	now := time.Now()
	age, idle := now.Sub(st.LoggedInAt), now.Sub(st.LastUsedAt)
	sess, err := openSession(bs.encryptionKey, st)
	unreadable := err != nil
	if !unreadable {
		_, err = bs.booker.CheckSession(ctx, sess)
	}

	var result, check string
	switch {
	case unreadable:
		result, check = reuseWouldFail, checkUnreadable
	case err == nil:
		result, check = reuseWouldWork, checkOK
	case errors.Is(err, wodbuster.ErrSessionExpired):
		result, check = reuseWouldFail, checkExpired
		bs.metrics.observeSessionRefused(sourcePreflight, checkExpired, age, idle)
	case errors.Is(err, wodbuster.ErrChallenge):
		result, check = reuseInconclusive, checkChallenge
	default:
		result, check = reuseInconclusive, checkInconclusive
	}
	bs.metrics.observeSessionCheck(sourcePreflight, check)
	bs.metrics.observeSessionReuse(ReuseShadow, result)
	log.Info("shadow check", "result", result,
		"age", age.Round(time.Minute), "idle", idle.Round(time.Minute),
		"logged_in_at", st.LoggedInAt, "last_used_at", st.LastUsedAt,
		"auth_expires_at", st.AuthExpiresAt, "trusted_device", st.TrustedDevice,
		"error", err)
	return result
}

// preflightTrouble is loginTrouble for the preflight: the athlete hears about
// the first failed attempt, the admins about the first of the whole preflight.
func (bs *BookingScheduler) preflightTrouble(chatID int64, runStart, opensAt time.Time,
	attempt, of int, err error, alertAdmin *sync.Once) {

	loc := bs.opening.Location
	if attempt == 1 {
		bs.notify(chatID, fmt.Sprintf("⚠️ Getting ready for the %s booking: logging in to WodBuster "+
			"failed (try %d of %d). I'm still trying, and the %s run will try again too.",
			opensAt.In(loc).Format("15:04"), attempt, of, runStart.In(loc).Format("15:04")))
	}
	alertAdmin.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		bs.notifyAdmins(ctx, chatID, fmt.Sprintf("⚠️ Sunday preflight: a login failed for chat %d "+
			"(try %d of %d): %v\nOther athletes may follow. The %s run will try again for anyone left without a session.",
			chatID, attempt, of, err, runStart.In(loc).Format("15:04")))
	})
}

// reportPreflight sends the admins one summary and records the outcome.
func (bs *BookingScheduler) reportPreflight(results []preflightResult, admins []models.User, took time.Duration) {
	ok := 0
	shadow := map[string]int{}
	var failures []string
	for _, r := range results {
		shadow[r.shadow]++
		if r.loggedIn {
			ok++
			continue
		}
		failures = append(failures, fmt.Sprintf("• %s (chat %d): %v", r.user.Email, r.user.ChatID, r.err))
	}

	outcome := "all_ok"
	switch {
	case ok == 0:
		outcome = "failed"
	case ok < len(results):
		outcome = "partial"
	}
	bs.metrics.observePreflight(outcome)
	bs.logger.Info("preflight done", "ok", ok, "failed", len(results)-ok,
		"took", took.Round(time.Second), "failures", failures, "shadow", shadow)

	var b strings.Builder
	if ok == len(results) {
		fmt.Fprintf(&b, "✅ Sunday preflight: all %d athletes are logged in for the booking.", ok)
	} else {
		fmt.Fprintf(&b, "⚠️ Sunday preflight: %d of %d athletes logged in. The run will log in again for:\n%s",
			ok, len(results), strings.Join(failures, "\n"))
	}
	fmt.Fprintf(&b, "\n\nStored sessions (shadow check): %d would have worked, %d refused, %d inconclusive, %d missing.",
		shadow[reuseWouldWork], shadow[reuseWouldFail], shadow[reuseInconclusive], shadow[reuseMissing])
	bs.tellAdmins(admins, 0, b.String())
}

// stopPreflight ends a preflight still running and waits for it, so the run
// never logs anyone in at the same time as the preflight.
func (bs *BookingScheduler) stopPreflight() {
	bs.preflightMu.Lock()
	cancel, done := bs.preflightCancel, bs.preflightDone
	bs.preflightMu.Unlock()
	if cancel == nil {
		return
	}
	bs.logger.Warn("the booking run is starting; stopping the session preflight")
	cancel()
	<-done
}

// runSession is the stored session a real run may book with, or nil. In a
// shadow week that is only one logged in within freshLoginWindow, which means
// this morning's preflight: a days-old session is checked, never booked with.
func (bs *BookingScheduler) runSession(ctx context.Context, chatID int64, now time.Time) *wodbuster.Session {
	if bs.reuseMode != ReuseShadow {
		return nil
	}
	st, ok, err := bs.storage.GetSession(ctx, chatID)
	if err != nil {
		bs.logger.Error("could not read the stored session; the run will log in", "chat_id", chatID, "error", err)
		return nil
	}
	if !ok {
		return nil
	}
	if now.Sub(st.LoggedInAt) > freshLoginWindow {
		bs.logger.Info("stored session is older than this morning's preflight; the run will log in",
			"chat_id", chatID, "logged_in_at", st.LoggedInAt)
		return nil
	}
	sess, err := openSession(bs.encryptionKey, st)
	if err != nil {
		bs.logger.Error("stored session unreadable; the run will log in", "chat_id", chatID, "error", err)
		return nil
	}
	return &sess
}

// storeRunSession is the run's OnSession: it keeps the session the run ended
// with, and counts whether the run used the stored one or had to log in.
func (bs *BookingScheduler) storeRunSession(ctx context.Context, chatID int64, offered bool,
	sess wodbuster.Session, reused bool) {

	if offered {
		result := reuseFallback
		if reused {
			result = reuseUsed
		}
		bs.metrics.observeSessionReuse(bs.reuseMode, result)
	}
	ctx, cancel := recordingContext(ctx)
	defer cancel()
	st, err := sealSession(bs.encryptionKey, chatID, sess, time.Now())
	if err == nil {
		err = bs.storage.SaveSession(ctx, st)
	}
	if err != nil {
		bs.logger.Error("could not store the run's session", "chat_id", chatID, "error", err)
	}
}
