package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/utils"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster/browserauth"
)

// Where a stored session was checked, for the session_checks_total metric,
// or where a fresh one came from, for the logs.
const (
	sourceRefresh    = "refresh"
	sourceLogin      = "login"
	sourceTest       = "test"
	sourceLoginCheck = "login_check"
)

// Check results, for the same metric.
const (
	checkOK           = "ok"
	checkExpired      = "expired"
	checkChallenge    = "challenge"
	checkInconclusive = "inconclusive"
	checkUnreadable   = "unreadable"
)

// The refresh is spread out so it does not look like a bot on a timer: it
// starts up to defaultRefreshStartJitter after the cron fires, and waits
// defaultRefreshGap plus up to defaultRefreshGapJitter between athletes. Six
// athletes take two or three minutes, at a slightly different time each day.
const (
	defaultRefreshGap         = 10 * time.Second
	defaultRefreshGapJitter   = 20 * time.Second
	defaultRefreshStartJitter = 5 * time.Minute
)

// jitter is a random duration in [0, limit).
func jitter(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	return rand.N(limit)
}

// sealSession turns a session into what is stored: the whole session as JSON,
// encrypted with the password's key, plus plain metadata for /status and the
// metrics. lastUsed is the last time WodBuster accepted it.
func sealSession(key string, chatID int64, sess wodbuster.Session, lastUsed time.Time) (models.StoredSession, error) {
	raw, err := json.Marshal(sess)
	if err != nil {
		return models.StoredSession{}, fmt.Errorf("could not encode the session: %w", err)
	}
	enc, err := utils.EncryptPassword(string(raw), key)
	if err != nil {
		return models.StoredSession{}, fmt.Errorf("could not encrypt the session: %w", err)
	}
	loggedIn := sess.IssuedAt
	if loggedIn.IsZero() {
		loggedIn = lastUsed
	}
	expiry := sess.AuthExpiry()
	return models.StoredSession{
		ChatID:        chatID,
		Encrypted:     enc,
		LoggedInAt:    loggedIn,
		LastUsedAt:    lastUsed,
		AuthExpiresAt: expiry,
		// "Remember this device" is what gives .WBAuth an expiry; "just
		// once" leaves it a browser-session cookie.
		TrustedDevice: !expiry.IsZero(),
	}, nil
}

// openSession is sealSession's inverse.
func openSession(key string, st models.StoredSession) (wodbuster.Session, error) {
	raw, err := utils.DecryptPassword(st.Encrypted, key)
	if err != nil {
		return wodbuster.Session{}, fmt.Errorf("could not decrypt the stored session: %w", err)
	}
	var sess wodbuster.Session
	if err := json.Unmarshal([]byte(raw), &sess); err != nil {
		return wodbuster.Session{}, fmt.Errorf("could not decode the stored session: %w", err)
	}
	if err := sess.Valid(); err != nil {
		return wodbuster.Session{}, err
	}
	return sess, nil
}

// SetSessionRefresh schedules the stored-session refresh. spec is a cron spec in
// the gym's zone; empty turns it off. Called once, before Start.
func (bs *BookingScheduler) SetSessionRefresh(spec string) { bs.sessionRefreshSpec = spec }

// RefreshSessions checks every active athlete's stored session with one
// schedule read, and stores the session WodBuster hands back.
//
// The read is what keeps a session alive: WodBuster renews .WBAuth on ordinary
// requests, and a session left unused for 16 hours is the longest gap that has
// been proven to survive. Running every 12 hours keeps every stored session
// inside that.
//
// What happens to each session depends on the answer:
//   - accepted: the renewed session is stored.
//   - refused (the login page): the athlete is logged in again with the stored
//     password and the new session replaces it. If that fails too, it is
//     deleted and the admins are told.
//   - a Cloudflare challenge, or a network error: nothing was learned about
//     the session, so it is kept and checked again next time.
//
// The refresh takes minutes, and an athlete can /login or be disabled in the
// middle of it. So each athlete and their session are read just before the
// check, and every write is conditional on the session not having changed
// since: a newer write, or a deletion, always wins over the refresh.
//
// Sessions of athletes who are not active are deleted: only an account that
// is booked for keeps a credential.
//
// Every check is a metric, and every refusal a log line with the session's age
// and idle time: that is how the real lifetime of a session gets measured.
func (bs *BookingScheduler) RefreshSessions() {
	ctx, cancel := context.WithTimeout(bs.life, refreshBudget)
	defer cancel()
	// Registered before looking at the run, so the run cannot start unseen in
	// between (see runBlocker.begin).
	end, ok := bs.refreshJob.begin(cancel)
	if !ok {
		bs.logger.Warn("skipping the session refresh: the previous one is still going")
		return
	}
	defer end()
	// Only the weekly run: a rehearsal logs in through the same gate and
	// writes no session, so it has nothing to fear from a refresh.
	if bs.weekRunning.Load() {
		bs.logger.Info("skipping the session refresh during a booking run")
		return
	}

	if !sleepCtx(ctx, jitter(bs.refreshStartJitter)) {
		return
	}

	users, err := bs.storage.ListUsers(ctx)
	if err != nil {
		bs.logger.Error("session refresh skipped: could not read the users", "error", err)
		return
	}
	slices.SortFunc(users, byChatID)
	r := &refreshRun{admins: models.ActiveAdmins(users)}

	started := time.Now()
	checked := 0
	complete := true
	for _, listed := range users {
		// The run stops the refresh through ctx; this catches a run that
		// started between two athletes, before it could.
		if ctx.Err() != nil || bs.weekRunning.Load() {
			bs.logger.Warn("session refresh cut short", "checked", checked, "error", ctx.Err())
			complete = false
			break
		}
		if !listed.IsActive() {
			bs.dropInactiveSession(ctx, listed.ChatID)
			continue
		}
		if _, _, ok := bs.refreshTarget(ctx, listed.ChatID); !ok {
			continue
		}
		if checked > 0 && !sleepCtx(ctx, bs.refreshGap+jitter(bs.refreshGapJitter)) {
			continue // the check at the top of the loop says why
		}
		// The list is minutes old by now: read the athlete and the session
		// again, so a /disable or a /login made meanwhile is seen.
		u, st, ok := bs.refreshTarget(ctx, listed.ChatID)
		if !ok {
			continue
		}
		checked++
		bs.refreshOne(ctx, r, u, st)
	}
	if complete && ctx.Err() == nil {
		bs.metrics.observeRefreshDone(time.Now())
	}
	// From the store, not from what this refresh saw: athletes it never got
	// to still have their sessions.
	bs.updateSessionGauges(bs.life)
	bs.logger.Info("session refresh done", "checked", checked, "complete", complete,
		"took", time.Since(started).Round(time.Second))
}

// refreshBudget caps one refresh. Checks are one request each; what can take
// long is logging athletes in again, which is why one failed login stops the
// rest (see refreshRun).
const refreshBudget = 20 * time.Minute

// refreshRun is what one refresh carries from athlete to athlete.
type refreshRun struct {
	admins []models.User
	// loginBroken is set by a failed login that was not a wrong password.
	// WodBuster's login is then most likely broken for everyone, and one
	// browser login after another, each up to a minute and a half, would
	// only burn the refresh's budget on the same failure. The daily login
	// check and the preflight report that kind of failure anyway.
	loginBroken bool
}

// refreshOne checks one session and stores what WodBuster hands back.
func (bs *BookingScheduler) refreshOne(ctx context.Context, r *refreshRun, u models.User, st models.StoredSession) {
	log := bs.logger.With("chat_id", u.ChatID, "source", sourceRefresh)

	sess, err := openSession(bs.encryptionKey, st)
	if err != nil {
		// A key change or a corrupt document. Either way it is useless.
		log.Error("stored session unreadable; replacing it", "error", err)
		bs.metrics.observeSessionCheck(sourceRefresh, checkUnreadable)
		bs.relogin(ctx, log, r, u, st, err)
		return
	}

	renewed, err := bs.booker.CheckSession(ctx, sess)
	now := time.Now()
	switch {
	case err == nil:
		bs.metrics.observeSessionCheck(sourceRefresh, checkOK)
		next, err := sealSession(bs.encryptionKey, u.ChatID, renewed, now)
		var replaced bool
		if err == nil {
			replaced, err = bs.replaceSession(ctx, log, st, next)
		}
		if err != nil {
			log.Error("could not store the renewed session", "error", err)
			return
		}
		if replaced {
			log.Debug("session renewed", "new_auth_expires_at", next.AuthExpiresAt)
		}

	case errors.Is(err, wodbuster.ErrSessionExpired):
		bs.metrics.observeSessionCheck(sourceRefresh, checkExpired)
		bs.metrics.observeSessionRefused(sourceRefresh, checkExpired,
			now.Sub(st.LoggedInAt), now.Sub(st.LastUsedAt))
		log.Warn("session refused",
			"reason", checkExpired,
			"age", now.Sub(st.LoggedInAt).Round(time.Minute),
			"idle", now.Sub(st.LastUsedAt).Round(time.Minute),
			"logged_in_at", st.LoggedInAt, "last_used_at", st.LastUsedAt,
			"auth_expires_at", st.AuthExpiresAt, "trusted_device", st.TrustedDevice,
			"error", err)
		bs.relogin(ctx, log, r, u, st, err)

	case errors.Is(err, wodbuster.ErrChallenge):
		bs.metrics.observeSessionCheck(sourceRefresh, checkChallenge)
		log.Warn("session check met a Cloudflare challenge; keeping the session", "error", err)

	default:
		bs.metrics.observeSessionCheck(sourceRefresh, checkInconclusive)
		log.Warn("session check inconclusive; keeping the session", "error", err)
	}
}

// relogin replaces the lost session prev with a fresh login: one attempt, not
// the run's three, since nothing is waiting on it and Sunday's preflight and
// run retry on their own. A success is quiet; a failure deletes prev and goes
// to the admins, because the Sunday run would meet the same failure.
func (bs *BookingScheduler) relogin(ctx context.Context, log *slog.Logger, r *refreshRun, u models.User,
	prev models.StoredSession, cause error) {

	next, err := bs.loginFor(ctx, r, u)
	if err != nil {
		bs.reloginFailed(ctx, log, r, u, prev, cause, fmt.Errorf("logging in again failed: %w", err))
		return
	}
	replaced, err := bs.replaceSession(ctx, log, prev, next)
	if err != nil {
		bs.reloginFailed(ctx, log, r, u, prev, cause,
			fmt.Errorf("logged in again, but could not store the session: %w", err))
		return
	}
	if replaced {
		log.Info("lost session replaced by a new login",
			"auth_expires_at", next.AuthExpiresAt, "trusted_device", next.TrustedDevice)
	}
}

// loginFor logs u in once and seals the session, unless an earlier login of
// this refresh already failed in a way that says the login is broken.
func (bs *BookingScheduler) loginFor(ctx context.Context, r *refreshRun, u models.User) (models.StoredSession, error) {
	if r.loginBroken {
		return models.StoredSession{}, errors.New("not tried: an earlier login in this refresh failed")
	}
	password, err := utils.DecryptPassword(u.Password, bs.encryptionKey)
	if err != nil {
		return models.StoredSession{}, fmt.Errorf("could not read the stored password: %w", err)
	}
	sess, err := bs.booker.Authenticate(ctx, u.Email, password)
	if err != nil {
		if !errors.Is(err, browserauth.ErrRejected) && ctx.Err() == nil {
			r.loginBroken = true
		}
		return models.StoredSession{}, err
	}
	return sealSession(bs.encryptionKey, u.ChatID, sess, time.Now())
}

// reloginFailed deletes the lost session prev, unless it changed meanwhile,
// and tells the admins. A refresh cut short by the run or a shutdown tells
// nobody; one that ran out of its own time does, since the session is lost
// all the same.
func (bs *BookingScheduler) reloginFailed(ctx context.Context, log *slog.Logger, r *refreshRun, u models.User,
	prev models.StoredSession, cause, err error) {

	log.Error("could not replace a lost session", "error", err)
	// Detached: the delete is still right when ctx has just run out.
	dctx, cancel := recordingContext(ctx)
	defer cancel()
	if _, derr := bs.storage.DeleteSessionIfUnchanged(dctx, u.ChatID, prev.UpdatedAt); derr != nil {
		log.Error("could not delete the lost session", "error", derr)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	bs.tellAdmins(r.admins, 0, fmt.Sprintf(
		"🚨 Session refresh: the stored WodBuster session for chat %d (%s) stopped working (%v), "+
			"and it could not be replaced: %v\n\n"+
			"Sunday's run will try to log in again. If this keeps failing, that athlete should book by hand.",
		u.ChatID, u.Email, cause, err))
}

// replaceSession stores next in place of prev, unless the stored session
// changed since prev was read; then the newer one stands and replaced is
// false.
func (bs *BookingScheduler) replaceSession(ctx context.Context, log *slog.Logger,
	prev, next models.StoredSession) (replaced bool, err error) {

	replaced, err = bs.storage.ReplaceSession(ctx, prev.UpdatedAt, next)
	if err != nil {
		return false, err
	}
	if !replaced {
		log.Info("the stored session changed during the refresh; keeping the newer one")
	}
	return replaced, nil
}

// refreshTarget reads an athlete and their stored session; ok is false when
// the athlete is gone or not active, or has no session.
func (bs *BookingScheduler) refreshTarget(ctx context.Context, chatID int64) (models.User, models.StoredSession, bool) {
	u, ok := bs.storage.GetUser(ctx, chatID)
	if !ok || !u.IsActive() {
		return models.User{}, models.StoredSession{}, false
	}
	st, ok, err := bs.storage.GetSession(ctx, chatID)
	if err != nil {
		bs.logger.Error("could not read a stored session", "chat_id", chatID, "error", err)
		return models.User{}, models.StoredSession{}, false
	}
	return u, st, ok
}

// dropInactiveSession deletes the session of an athlete who is not active.
// /disable deletes it already; this catches the rest, such as one stored
// before /login stopped storing sessions for accounts that are not active.
func (bs *BookingScheduler) dropInactiveSession(ctx context.Context, chatID int64) {
	st, ok, err := bs.storage.GetSession(ctx, chatID)
	if err != nil || !ok {
		return
	}
	deleted, err := bs.storage.DeleteSessionIfUnchanged(ctx, chatID, st.UpdatedAt)
	if err != nil {
		bs.logger.Error("could not delete an inactive athlete's session", "chat_id", chatID, "error", err)
		return
	}
	if deleted {
		bs.logger.Info("deleted the session of an athlete who is not active", "chat_id", chatID)
	}
}

// sleepCtx waits d, and says false if ctx ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// updateSessionGauges sets the session gauges from what is stored now, for
// every active athlete. It runs at startup and after anything that stores or
// deletes sessions in bulk, so the gauges never wait 12 hours for a refresh.
// With SESSION_REUSE=off nothing is stored, and the gauges are left alone.
//
// Calls are serialised: two of them interleaving could leave the older
// reading in the gauges.
func (bs *BookingScheduler) updateSessionGauges(ctx context.Context) {
	if bs.metrics == nil || !bs.storesSessions() {
		return
	}
	bs.gaugesMu.Lock()
	defer bs.gaugesMu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	users, err := bs.storage.ListUsers(ctx)
	if err != nil {
		bs.logger.Error("could not update the session gauges", "error", err)
		return
	}
	stored := map[int64]models.StoredSession{}
	for _, u := range users {
		if !u.IsActive() {
			continue
		}
		st, ok, err := bs.storage.GetSession(ctx, u.ChatID)
		if err != nil {
			// A gauge that drops an athlete would read as a lost session
			// and set off the alerts; better to leave them as they were.
			bs.logger.Error("could not update the session gauges", "chat_id", u.ChatID, "error", err)
			return
		}
		if ok {
			stored[u.ChatID] = st
		}
	}
	bs.metrics.setStoredSessions(stored)
}

// storeFreshSession keeps a session a fresh login just made, replacing the
// stored one, when sessions are kept at all and the athlete is still active.
// A newer session is worth having: it is the one the refresh keeps alive.
// Failing to store it only costs the session.
func (bs *BookingScheduler) storeFreshSession(ctx context.Context, u models.User, sess wodbuster.Session, source string) {
	if !bs.storesSessions() || !u.IsActive() {
		return
	}
	log := bs.logger.With("chat_id", u.ChatID, "source", source)
	if err := sess.Valid(); err != nil {
		log.Error("the login returned no usable session; nothing stored", "error", err)
		return
	}
	st, err := sealSession(bs.encryptionKey, u.ChatID, sess, time.Now())
	var saved bool
	if err == nil {
		saved, err = bs.saveForActive(ctx, st)
	}
	if err != nil {
		log.Error("could not store the session", "error", err)
		return
	}
	if !saved {
		log.Info("the athlete is no longer active; session not stored")
		return
	}
	log.Info("session stored", "auth_expires_at", st.AuthExpiresAt, "trusted_device", st.TrustedDevice)
	bs.updateSessionGauges(ctx)
}

// saveForActive stores st unless its athlete is gone or no longer active.
// saved says whether it was kept.
//
// The athlete is read again after the save as well as before it. /disable
// saves the user first and deletes the session second, so a save that lands
// after that delete is followed by a read that sees the disable, and undone
// here. Every write of a session the athlete did not just ask for goes
// through this, so a /disable always wins.
func (bs *BookingScheduler) saveForActive(ctx context.Context, st models.StoredSession) (saved bool, err error) {
	if u, ok := bs.storage.GetUser(ctx, st.ChatID); !ok || !u.IsActive() {
		return false, nil
	}
	if err := bs.storage.SaveSession(ctx, st); err != nil {
		return false, err
	}
	if u, ok := bs.storage.GetUser(ctx, st.ChatID); ok && u.IsActive() {
		return true, nil
	}
	if err := bs.storage.DeleteSession(ctx, st.ChatID); err != nil {
		return false, fmt.Errorf("the athlete was disabled meanwhile, and their session could not be deleted: %w", err)
	}
	return false, nil
}

// dropAllSessions deletes every stored session. SESSION_REUSE=off runs it at
// startup: nothing reads them any more, and nothing would ever delete them.
func (bs *BookingScheduler) dropAllSessions(ctx context.Context) {
	users, err := bs.storage.ListUsers(ctx)
	if err != nil {
		bs.logger.Error("could not delete the stored sessions", "error", err)
		return
	}
	deleted := 0
	for _, u := range users {
		_, ok, err := bs.storage.GetSession(ctx, u.ChatID)
		if err != nil || !ok {
			continue
		}
		if err := bs.storage.DeleteSession(ctx, u.ChatID); err != nil {
			bs.logger.Error("could not delete a stored session", "chat_id", u.ChatID, "error", err)
			continue
		}
		deleted++
	}
	if deleted > 0 {
		bs.logger.Info("SESSION_REUSE=off: deleted the stored sessions", "deleted", deleted)
	}
}
