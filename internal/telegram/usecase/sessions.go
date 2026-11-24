package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/utils"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

// Where a stored session was checked, for the session_checks_total metric.
const (
	sourceRefresh = "refresh"
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

// jitter is a random duration in [0, max).
func jitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return rand.N(max)
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
	if len(bs.GetActiveBookings()) > 0 {
		bs.logger.Info("skipping the session refresh during a booking run")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	if !sleepCtx(ctx, jitter(bs.refreshStartJitter)) {
		return
	}
	if bs.weekRunning.Load() || len(bs.GetActiveBookings()) > 0 {
		bs.logger.Info("skipping the session refresh during a booking run")
		return
	}

	users, err := bs.storage.ListUsers(ctx)
	if err != nil {
		bs.logger.Error("session refresh skipped: could not read the users", "error", err)
		return
	}
	sort.Slice(users, func(i, j int) bool { return users[i].ChatID < users[j].ChatID })
	admins := models.ActiveAdmins(users)

	started := time.Now()
	kept := map[int64]models.StoredSession{}
	checked := 0
	for _, listed := range users {
		if !listed.IsActive() {
			bs.dropInactiveSession(ctx, listed.ChatID)
			continue
		}
		if _, _, ok := bs.refreshTarget(ctx, listed.ChatID); !ok {
			continue
		}
		if checked > 0 && !sleepCtx(ctx, bs.refreshGap+jitter(bs.refreshGapJitter)) {
			break
		}
		// The list is minutes old by now: read the athlete and the session
		// again, so a /disable or a /login made meanwhile is seen.
		u, st, ok := bs.refreshTarget(ctx, listed.ChatID)
		if !ok {
			continue
		}
		checked++
		if cur, ok := bs.refreshOne(ctx, u, st, admins); ok {
			kept[u.ChatID] = cur
		}
	}
	bs.metrics.setStoredSessions(kept, time.Now())
	bs.logger.Info("session refresh done", "checked", checked, "stored", len(kept),
		"took", time.Since(started).Round(time.Second))
}

// refreshOne checks one session and returns what is stored for the athlete
// afterwards; ok is false when nothing is.
func (bs *BookingScheduler) refreshOne(ctx context.Context, u models.User, st models.StoredSession,
	admins []models.User) (models.StoredSession, bool) {

	log := bs.logger.With("chat_id", u.ChatID, "source", sourceRefresh)

	sess, err := openSession(bs.encryptionKey, st)
	if err != nil {
		// A key change or a corrupt document. Either way it is useless.
		log.Error("stored session unreadable; replacing it", "error", err)
		bs.metrics.observeSessionCheck(sourceRefresh, checkUnreadable)
		return bs.relogin(ctx, log, u, st, admins, err)
	}

	renewed, err := bs.booker.CheckSession(ctx, sess)
	now := time.Now()
	switch {
	case err == nil:
		bs.metrics.observeSessionCheck(sourceRefresh, checkOK)
		next, err := sealSession(bs.encryptionKey, u.ChatID, renewed, now)
		if err != nil {
			log.Error("could not seal the renewed session", "error", err)
			return st, true
		}
		cur, ok, err := bs.replaceSession(ctx, log, st, next)
		if err != nil {
			log.Error("could not store the renewed session", "error", err)
			return st, true
		}
		log.Debug("session renewed", "new_auth_expires_at", next.AuthExpiresAt)
		return cur, ok

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
		return bs.relogin(ctx, log, u, st, admins, err)

	case errors.Is(err, wodbuster.ErrChallenge):
		bs.metrics.observeSessionCheck(sourceRefresh, checkChallenge)
		log.Warn("session check met a Cloudflare challenge; keeping the session", "error", err)
		return st, true

	default:
		bs.metrics.observeSessionCheck(sourceRefresh, checkInconclusive)
		log.Warn("session check inconclusive; keeping the session", "error", err)
		return st, true
	}
}

// relogin replaces the lost session prev with a fresh login, the same way a
// run logs in. A success is quiet; a failure deletes prev and goes to the
// admins, because the Sunday run would meet the same failure.
func (bs *BookingScheduler) relogin(ctx context.Context, log *slog.Logger, u models.User,
	prev models.StoredSession, admins []models.User, cause error) (models.StoredSession, bool) {

	password, err := utils.DecryptPassword(u.Password, bs.encryptionKey)
	var sess wodbuster.Session
	if err == nil {
		sess, err = bs.booker.Login(ctx, u.Email, password, nil)
	}
	if err == nil {
		var next models.StoredSession
		if next, err = sealSession(bs.encryptionKey, u.ChatID, sess, time.Now()); err == nil {
			var cur models.StoredSession
			var ok bool
			if cur, ok, err = bs.replaceSession(ctx, log, prev, next); err == nil {
				log.Info("lost session replaced by a new login",
					"auth_expires_at", next.AuthExpiresAt, "trusted_device", next.TrustedDevice)
				return cur, ok
			}
		}
	}
	log.Error("could not replace a lost session", "error", err)
	if _, derr := bs.storage.DeleteSessionIfUnchanged(ctx, u.ChatID, prev.UpdatedAt); derr != nil {
		log.Error("could not delete the lost session", "error", derr)
	}
	bs.tellAdmins(admins, 0, fmt.Sprintf(
		"🚨 Session refresh: the stored WodBuster session for chat %d (%s) stopped working (%v), "+
			"and logging in again failed: %v\n\n"+
			"Sunday's run will try to log in again. If this keeps failing, that athlete should book by hand.",
		u.ChatID, u.Email, cause, err))
	return models.StoredSession{}, false
}

// replaceSession stores next in place of prev, unless the stored session
// changed since prev was read. Then the newer one stands, and it is what comes
// back; ok is false when nothing is stored any more.
func (bs *BookingScheduler) replaceSession(ctx context.Context, log *slog.Logger,
	prev, next models.StoredSession) (models.StoredSession, bool, error) {

	replaced, err := bs.storage.ReplaceSession(ctx, prev.UpdatedAt, next)
	if err != nil {
		return models.StoredSession{}, false, err
	}
	if replaced {
		return next, true, nil
	}
	log.Info("the stored session changed during the refresh; keeping the newer one")
	cur, ok, err := bs.storage.GetSession(ctx, prev.ChatID)
	if err != nil {
		log.Error("could not read the newer session", "error", err)
		return models.StoredSession{}, false, nil
	}
	return cur, ok, nil
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
