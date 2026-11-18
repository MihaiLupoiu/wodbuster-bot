package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// defaultRefreshGap spaces out the refresh's requests, so that six athletes'
// sessions do not reach WodBuster in the same second.
const defaultRefreshGap = 10 * time.Second

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
//   - refused (the login page): it is deleted, and the athlete is logged in
//     again with the stored password. If that fails too, the admins are told.
//   - a Cloudflare challenge, or a network error: nothing was learned about
//     the session, so it is kept and checked again next time.
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
	for _, u := range users {
		if !u.IsActive() {
			continue
		}
		st, ok, err := bs.storage.GetSession(ctx, u.ChatID)
		if err != nil {
			bs.logger.Error("could not read a stored session", "chat_id", u.ChatID, "error", err)
			continue
		}
		if !ok {
			continue
		}
		if checked > 0 && !sleepCtx(ctx, bs.refreshGap) {
			break
		}
		checked++
		if st, ok := bs.refreshOne(ctx, u, st, admins); ok {
			kept[u.ChatID] = st
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
		log.Error("stored session unreadable; deleting it", "error", err)
		bs.metrics.observeSessionCheck(sourceRefresh, checkUnreadable)
		bs.deleteSession(ctx, log, u.ChatID)
		return bs.relogin(ctx, log, u, admins, err)
	}

	renewed, err := bs.booker.CheckSession(ctx, sess)
	now := time.Now()
	switch {
	case err == nil:
		next, err := sealSession(bs.encryptionKey, u.ChatID, renewed, now)
		if err != nil {
			log.Error("could not seal the renewed session", "error", err)
			return st, true
		}
		if err := bs.storage.SaveSession(ctx, next); err != nil {
			log.Error("could not store the renewed session", "error", err)
			return st, true
		}
		bs.metrics.observeSessionCheck(sourceRefresh, checkOK)
		log.Debug("session renewed", "new_auth_expires_at", next.AuthExpiresAt)
		return next, true

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
		bs.deleteSession(ctx, log, u.ChatID)
		return bs.relogin(ctx, log, u, admins, err)

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

// relogin replaces a lost session with a fresh login, the same way a run logs
// in. A success is quiet; a failure goes to the admins, because the Sunday run
// would meet the same failure.
func (bs *BookingScheduler) relogin(ctx context.Context, log *slog.Logger, u models.User, admins []models.User, cause error) (models.StoredSession, bool) {

	password, err := utils.DecryptPassword(u.Password, bs.encryptionKey)
	var sess wodbuster.Session
	if err == nil {
		sess, err = bs.booker.Login(ctx, u.Email, password, nil)
	}
	if err == nil {
		var st models.StoredSession
		if st, err = sealSession(bs.encryptionKey, u.ChatID, sess, time.Now()); err == nil {
			err = bs.storage.SaveSession(ctx, st)
		}
		if err == nil {
			log.Info("lost session replaced by a new login",
				"auth_expires_at", st.AuthExpiresAt, "trusted_device", st.TrustedDevice)
			return st, true
		}
	}
	log.Error("could not replace a lost session", "error", err)
	bs.tellAdmins(admins, 0, fmt.Sprintf(
		"🚨 Session refresh: the stored WodBuster session for chat %d (%s) stopped working (%v), "+
			"and logging in again failed: %v\n\n"+
			"Sunday's run will try to log in again. If this keeps failing, that athlete should book by hand.",
		u.ChatID, u.Email, cause, err))
	return models.StoredSession{}, false
}

func (bs *BookingScheduler) deleteSession(ctx context.Context, log *slog.Logger, chatID int64) {
	if err := bs.storage.DeleteSession(ctx, chatID); err != nil {
		log.Error("could not delete the stored session", "error", err)
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
