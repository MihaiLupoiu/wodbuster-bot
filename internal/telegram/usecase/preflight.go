package usecase

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/utils"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

// ReuseMode is SESSION_REUSE: what the bot does with stored sessions.
type ReuseMode string

const (
	// ReuseOff is the old behaviour: no stored sessions, no refresh, no
	// preflight; every run logs in at 11:50.
	ReuseOff ReuseMode = "off"
	// ReuseShadow logs everyone in at 11:00 and books with that session. The
	// days-old stored session is only checked, never used to book: the shadow
	// week measures whether it would have worked.
	ReuseShadow ReuseMode = "shadow"
	// ReuseLive is phase 2 and not built yet: the preflight would book with the
	// stored session when it works, and log in only when it does not.
	ReuseLive ReuseMode = "live"
)

// ParseReuseMode reads a SESSION_REUSE value. live is refused until phase 2 is
// built, rather than silently behaving like shadow.
func ParseReuseMode(s string) (ReuseMode, error) {
	switch m := ReuseMode(s); m {
	case ReuseOff, ReuseShadow:
		return m, nil
	case ReuseLive:
		return "", errors.New("SESSION_REUSE=live is phase 2 of the session reuse plan and not built yet; use shadow")
	default:
		return "", fmt.Errorf("SESSION_REUSE=%q: want off or shadow", s)
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

// A shadow week's run books only with a session logged in by this week's
// preflight: one logged in since the preflight started, if it started within
// preflightMemory. Without that (a restart since, or no preflight) it falls back
// to freshLoginWindow, which fits the default 11:00 preflight and 11:50 run.
// Either way a days-old session is checked, never booked with.
const (
	freshLoginWindow = 2 * time.Hour
	preflightMemory  = 12 * time.Hour
)

// defaultShadowJitter spreads the preflight's shadow checks over a few
// seconds, so the athletes' requests do not reach WodBuster in the same
// instant.
const defaultShadowJitter = 20 * time.Second

// jobStopWait caps how long the run waits for a preflight or refresh it has
// cancelled. A job that ignores the cancel must not hold up the booking.
const jobStopWait = 15 * time.Second

// runSessionReadTimeout bounds the run's read of the stored session.
const runSessionReadTimeout = 5 * time.Second

// SetSessionPreflight schedules the Sunday preflight and sets the reuse mode.
// spec is a cron spec in the gym's zone; empty turns the preflight off. Called
// once, before Start.
func (bs *BookingScheduler) SetSessionPreflight(spec string, mode ReuseMode) {
	bs.preflightSpec = spec
	bs.reuseMode = mode
}

// SetPreflightAllOKMessage chooses whether the admins hear from a preflight
// where every athlete logged in. Off, they hear only about trouble. Called
// once, before Start.
func (bs *BookingScheduler) SetPreflightAllOKMessage(on bool) { bs.preflightQuietWhenOK = !on }

// storesSessions is whether sessions are stored at all: SESSION_REUSE=off
// keeps none, and runs nothing that keeps them.
func (bs *BookingScheduler) storesSessions() bool { return bs.reuseMode != ReuseOff }

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
	opensAt := bs.opening.Target(now)
	runStart := opensAt.Add(-bs.opening.StartBefore)
	if !now.Before(runStart) {
		bs.logger.Info("session preflight skipped: the run is due", "run_start", runStart)
		return
	}

	// The deadline is the run's start, counted from now so that it holds for
	// whatever clock now came from.
	ctx, cancel := context.WithTimeout(bs.life, runStart.Sub(now))
	defer cancel()
	// Registered before looking at the run (see runBlocker.begin).
	end, ok := bs.preflightJob.begin(cancel)
	if !ok {
		bs.logger.Warn("session preflight skipped: the previous one is still going")
		return
	}
	defer end()
	// Only the weekly run: a rehearsal stores no session and waits its turn
	// at the login gate like anyone else.
	if bs.weekRunning.Load() {
		bs.logger.Info("session preflight skipped: the booking run is already going")
		return
	}

	started := time.Now()
	bs.preflightStarted.Store(started.UnixNano())
	users, err := bs.storage.ListUsers(ctx)
	if err != nil {
		bs.logger.Error("session preflight failed: could not read the users", "error", err)
		bs.metrics.observePreflight("failed")
		return
	}
	slices.SortFunc(users, byChatID)
	admins := models.ActiveAdmins(users)

	var athletes []models.User
	for _, u := range users {
		if bookable(u) {
			athletes = append(athletes, u)
		}
	}
	if len(athletes) == 0 {
		bs.logger.Info("session preflight: nobody to book for")
		bs.metrics.observePreflight("nobody")
		return
	}
	bs.logger.Info("session preflight starting", "athletes", len(athletes),
		"run_start", runStart.Format(time.RFC1123))

	// Each athlete gets their own goroutine, as in the run; the login gate
	// still lets one browser through at a time, so this only stops one slow
	// athlete from holding up the shadow checks of the others.
	var alerts loginAlerts
	results := make([]preflightResult, len(athletes))
	var wg sync.WaitGroup
	for i, u := range athletes {
		wg.Go(func() { results[i] = bs.preflightOne(ctx, u, runStart, opensAt, &alerts) })
	}
	wg.Wait()
	// Nothing is logged in or stored past here: the run need not wait for
	// the admins' summary to be delivered.
	end()
	alerts.wait()

	bs.reportPreflight(results, admins, time.Since(started))
	bs.updateSessionGauges(bs.life)
}

func byChatID(a, b models.User) int { return cmp.Compare(a.ChatID, b.ChatID) }

func (bs *BookingScheduler) preflightOne(ctx context.Context, u models.User,
	runStart, opensAt time.Time, alerts *loginAlerts) preflightResult {

	// Cut short before the check, the athlete still counts in the summary.
	res := preflightResult{user: u, shadow: reuseInconclusive}
	log := bs.logger.With("chat_id", u.ChatID, "source", sourcePreflight)

	if !sleepCtx(ctx, jitter(bs.shadowJitter)) {
		res.err = ctx.Err()
		return res
	}
	res.shadow = bs.shadowCheck(ctx, u)

	password, err := utils.DecryptPassword(u.Password, bs.encryptionKey)
	if err != nil {
		res.err = fmt.Errorf("could not read the stored password: %w", err)
		log.Error("preflight login impossible", "error", res.err)
		return res
	}
	sess, err := bs.booker.Login(ctx, u.Email, password, func(attempt, of int, err error) {
		bs.preflightTrouble(u.ChatID, runStart, opensAt, attempt, of, err, alerts)
	})
	if err != nil {
		res.err = err
		if ctx.Err() != nil {
			// The run started and took over; it logs in itself.
			log.Warn("preflight login cut short by the run", "error", err)
			return res
		}
		log.Error("preflight login failed", "error", err)
		// After the "try 1 of 3" warning, never before it.
		alerts.waitFor(u.ChatID)
		bs.notify(u.ChatID, fmt.Sprintf("⚠️ Logging in to WodBuster failed at %s (%v). "+
			"The %s run will try again; if you hear nothing by %s, book your classes by hand.",
			time.Now().In(bs.opening.Location).Format("15:04"), err,
			runStart.In(bs.opening.Location).Format("15:04"),
			opensAt.Add(5*time.Minute).In(bs.opening.Location).Format("15:04")))
		return res
	}

	st, err := sealSession(bs.encryptionKey, u.ChatID, sess, time.Now())
	saved := false
	if err == nil {
		saved, err = bs.saveForActive(ctx, st)
	}
	if err != nil {
		// The run will log in again; nothing for the athlete to do.
		res.err = fmt.Errorf("logged in, but could not store the session: %w", err)
		log.Error("preflight could not store the session", "error", err)
		return res
	}
	if !saved {
		// Disabled since the preflight started: the run will skip them too.
		res.err = errors.New("no longer active; session not stored")
		log.Info("preflight: the athlete is no longer active; session not stored")
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
	attrs := []any{"result", result,
		"age", age.Round(time.Minute), "idle", idle.Round(time.Minute),
		"logged_in_at", st.LoggedInAt, "last_used_at", st.LastUsedAt,
		"auth_expires_at", st.AuthExpiresAt, "trusted_device", st.TrustedDevice,
		"error", err}
	log.Info("shadow check", attrs...)
	if result == reuseWouldFail {
		// The same line the refresh writes, so one search finds every
		// refusal. A dead session is no use to anyone: the login that
		// follows normally replaces it, and if that fails, it is gone anyway.
		log.Warn("session refused", append(attrs, "reason", check)...)
		if _, err := bs.storage.DeleteSessionIfUnchanged(ctx, u.ChatID, st.UpdatedAt); err != nil {
			log.Error("could not delete the refused session", "error", err)
		}
	}
	return result
}

// preflightTrouble is loginTrouble for the preflight: the athlete hears about
// the first failed attempt, the admins about the first of the whole preflight.
func (bs *BookingScheduler) preflightTrouble(chatID int64, runStart, opensAt time.Time,
	attempt, of int, err error, alerts *loginAlerts) {

	loc := bs.opening.Location
	if attempt == 1 {
		alerts.toAthlete(chatID, func() {
			bs.notify(chatID, fmt.Sprintf("⚠️ Getting ready for the %s booking: logging in to WodBuster "+
				"failed (try %d of %d). I'm still trying, and the %s run will try again too.",
				opensAt.In(loc).Format("15:04"), attempt, of, runStart.In(loc).Format("15:04")))
		})
	}
	alerts.toAdminsOnce(func() {
		ctx, cancel := context.WithTimeout(bs.life, 10*time.Second)
		defer cancel()
		bs.notifyAdmins(ctx, chatID, fmt.Sprintf("⚠️ Sunday preflight: a login failed for chat %d "+
			"(try %d of %d): %v\nOther athletes may follow. The %s run will try again for anyone left without a session.",
			chatID, attempt, of, err, runStart.In(loc).Format("15:04")))
	})
}

// loginAlerts is what the athletes of one run or preflight share while they
// log in: the admins hear about the first failed login only, and every
// message goes out in the background, so a slow Telegram never holds up the
// next login attempt.
//
// An athlete's own messages are tracked apart, so that their final word
// (results, or "book by hand") can wait for the warnings before it without
// waiting on anyone else's. The run or preflight waits for all of them once
// every athlete is done.
type loginAlerts struct {
	admins     sync.Once
	adminSends sync.WaitGroup

	mu    sync.Mutex
	chats map[int64]*sync.WaitGroup
}

func (a *loginAlerts) athlete(chatID int64) *sync.WaitGroup {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.chats == nil {
		a.chats = map[int64]*sync.WaitGroup{}
	}
	wg, ok := a.chats[chatID]
	if !ok {
		wg = &sync.WaitGroup{}
		a.chats[chatID] = wg
	}
	return wg
}

// toAthlete sends in the background. Called only from that athlete's own
// goroutine, like waitFor.
func (a *loginAlerts) toAthlete(chatID int64, f func()) { a.athlete(chatID).Go(f) }

func (a *loginAlerts) toAdminsOnce(f func()) { a.admins.Do(func() { a.adminSends.Go(f) }) }

// waitFor waits for the messages to one athlete. A nil *loginAlerts has none.
func (a *loginAlerts) waitFor(chatID int64) {
	if a != nil {
		a.athlete(chatID).Wait()
	}
}

// wait waits for every message; only once no athlete can send any more.
func (a *loginAlerts) wait() {
	a.adminSends.Wait()
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, wg := range a.chats {
		wg.Wait()
	}
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

	if ok == len(results) && bs.preflightQuietWhenOK {
		return
	}
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

// stopRunBlockers ends a preflight or refresh still running and waits for
// both together, up to jobStopWait, so the run never logs anyone in at the
// same time.
func (bs *BookingScheduler) stopRunBlockers() {
	names := []string{"session preflight", "session refresh"}
	for i, r := range stopAll(jobStopWait, &bs.preflightJob, &bs.refreshJob) {
		switch {
		case !r.running:
		case r.ended:
			bs.logger.Warn("the booking run is starting; stopped the " + names[i])
		default:
			bs.logger.Error("the booking run is starting; the "+names[i]+" did not stop in time, going ahead",
				"waited", jobStopWait)
		}
	}
}

// freshSince is the oldest login a real run may book with: one made by this
// week's preflight. See freshLoginWindow.
func (bs *BookingScheduler) freshSince(now time.Time) time.Time {
	if ns := bs.preflightStarted.Load(); ns != 0 {
		if p := time.Unix(0, ns); now.Sub(p) >= 0 && now.Sub(p) < preflightMemory {
			return p
		}
	}
	return now.Add(-freshLoginWindow)
}

// runSession is the stored session a real run may book with, or nil. In a
// shadow week that is only one logged in by this morning's preflight: a
// days-old session is checked, never booked with.
func (bs *BookingScheduler) runSession(ctx context.Context, chatID int64, now time.Time) *wodbuster.Session {
	if bs.reuseMode != ReuseShadow {
		return nil
	}
	// A stalled store must not hold the run: without a session it logs in,
	// which it had time for before the preflight existed.
	ctx, cancel := context.WithTimeout(ctx, runSessionReadTimeout)
	defer cancel()
	st, ok, err := bs.storage.GetSession(ctx, chatID)
	if err != nil {
		bs.logger.Error("could not read the stored session; the run will log in", "chat_id", chatID, "error", err)
		return nil
	}
	if !ok {
		return nil
	}
	if since := bs.freshSince(now); st.LoggedInAt.Before(since) {
		bs.logger.Info("stored session is older than this morning's preflight; the run will log in",
			"chat_id", chatID, "logged_in_at", st.LoggedInAt, "fresh_since", since)
		return nil
	}
	sess, err := openSession(bs.encryptionKey, st)
	if err != nil {
		bs.logger.Error("stored session unreadable; the run will log in", "chat_id", chatID, "error", err)
		return nil
	}
	return &sess
}

// endedSession holds what the run's OnSession hands over until the run's
// results are out. A nil *endedSession holds nothing.
type endedSession struct {
	offered bool

	mu     sync.Mutex
	sess   wodbuster.Session
	reused bool
	has    bool
}

func (e *endedSession) set(sess wodbuster.Session, reused bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sess, e.reused, e.has = sess, reused, true
}

func (e *endedSession) get() (sess wodbuster.Session, reused, ok bool) {
	if e == nil {
		return wodbuster.Session{}, false, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sess, e.reused, e.has
}

// storeRunSession keeps the session the run ended with, and counts whether
// the run used the stored one or had to log in.
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
		_, err = bs.saveForActive(ctx, st)
	}
	if err != nil {
		bs.logger.Error("could not store the run's session", "chat_id", chatID, "error", err)
	}
}
