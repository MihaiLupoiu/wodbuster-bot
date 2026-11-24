package telegram

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/usecase"
)

// Who may run what.
//
// Anyone can sign in and ask where they stand: /login checks and saves a
// newcomer's WodBuster login, but the account stays pending until an admin
// approves it, and nothing else works until then. Admin commands are invisible
// to everyone else: they get the same reply as a typo.
var (
	openCommands  = map[string]bool{"start": true, "help": true, "login": true, "status": true}
	adminCommands = map[string]bool{"users": true, "approve": true, "disable": true, "announcement": true}
)

type access int

const (
	allowed access = iota
	notApproved
	disabled
	notAdmin
)

// accessFor decides whether a user may run a command. registered is false for
// a chat the bot has never seen log in; their command's own handler tells them
// to /login, so they are let through to it.
func accessFor(command string, user models.User, registered bool) access {
	if openCommands[command] {
		return allowed
	}
	if adminCommands[command] {
		if registered && user.IsAdmin() && user.IsActive() {
			return allowed
		}
		return notAdmin
	}
	if !registered {
		return allowed
	}
	switch user.Status {
	case models.StatusActive:
		return allowed
	case models.StatusDisabled:
		return disabled
	default:
		return notApproved
	}
}

const unknownCommand = "I don't know that command. Use /help to see available commands"

// gate answers a user who may not run command, and says whether they may.
func (b *Bot) gate(ctx context.Context, chatID int64, command string) bool {
	user, registered := b.manager.GetUser(ctx, chatID)
	switch accessFor(command, user, registered) {
	case allowed:
		return true
	case notApproved:
		b.Notify(chatID, pendingMessage)
	case disabled:
		b.Notify(chatID, "⛔ Your account is disabled, so the bot does nothing for you. "+
			"Ask the admin if you think this is a mistake.")
	case notAdmin:
		b.sendMessage(chatID, unknownCommand)
	}
	b.logger.Info("command refused", "chat_id", chatID, "command", command,
		"registered", registered, "status", user.Status, "role", user.Role)
	return false
}

const pendingMessage = "⏳ Your WodBuster login works and is saved. Your account is waiting for the " +
	"admin's approval; you will get a message here as soon as it is active. Until then " +
	"nothing is booked for you."

// afterLogin says what a saved login means for this user: a newcomer waits
// for approval, and the admin is asked for it.
func (b *Bot) afterLogin(ctx context.Context, chatID int64, username string) {
	user, ok := b.manager.GetUser(ctx, chatID)
	if !ok {
		return
	}
	switch user.Status {
	case models.StatusActive:
		b.Notify(chatID, "Add your weekly classes with /book. Every Sunday at 12:00 the bot books them for you.")
	case models.StatusDisabled:
		b.Notify(chatID, "⛔ Your account is disabled, so nothing will be booked. Ask the admin to enable it.")
	default:
		b.Notify(chatID, pendingMessage)
		b.notifyAdmins(ctx, chatID, signupMessage(user, username))
	}
}

func signupMessage(u models.User, username string) string {
	who := u.Email
	if username != "" {
		who += " (@" + username + ")"
	}
	return fmt.Sprintf("🆕 %s logged in and is waiting for approval.\n\nApprove: /approve %d\nRefuse: /disable %d",
		who, u.ChatID, u.ChatID)
}

// storeTimeout bounds the storage reads a command makes, so a slow store
// cannot hold a reply, or a handler goroutine, forever.
const storeTimeout = 10 * time.Second

func (b *Bot) handleUsers(ctx context.Context, chatID int64) {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	users, err := b.manager.ListUsers(ctx)
	if err != nil {
		b.logger.Error("could not list the users", "err", err)
		b.Notify(chatID, "❌ Could not read the users: "+err.Error())
		return
	}
	sessions := map[int64]bool{}
	for _, u := range users {
		if !u.IsActive() {
			continue
		}
		_, ok, err := b.manager.GetSession(ctx, u.ChatID)
		if err != nil {
			b.logger.Error("could not read a stored session", "chat_id", u.ChatID, "err", err)
			ok = true // unknown: do not raise a false alarm
		}
		sessions[u.ChatID] = ok
	}
	b.Notify(chatID, formatUsers(users, sessions))
}

// formatUsers lists pending accounts first, since they are the ones waiting
// on the admin. hasSession says, for active users, whether a WodBuster session
// is stored; an active user without one is marked, since Sunday's run will
// have to log in for them. Whether a stored session still works is not known
// here, so the mark says only that none is stored.
func formatUsers(users []models.User, hasSession map[int64]bool) string {
	if len(users) == 0 {
		return "Nobody has logged in yet."
	}
	order := map[string]int{models.StatusPending: 0, models.StatusActive: 1, models.StatusDisabled: 2}
	sorted := append([]models.User(nil), users...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if order[sorted[i].Status] != order[sorted[j].Status] {
			return order[sorted[i].Status] < order[sorted[j].Status]
		}
		return sorted[i].Email < sorted[j].Email
	})

	icon := map[string]string{models.StatusPending: "⏳", models.StatusActive: "✅", models.StatusDisabled: "⛔"}
	var lines []string
	for _, u := range sorted {
		line := fmt.Sprintf("%s %s · chat %d · %d class(es)", icon[u.Status], u.Email, u.ChatID,
			len(u.ClassBookingSchedules))
		if u.IsAdmin() {
			line += " · admin"
		}
		if ok, known := hasSession[u.ChatID]; u.IsActive() && known && !ok {
			line += " · ⚠️ no stored session"
		}
		lines = append(lines, line)
	}
	return fmt.Sprintf("👥 Users (%d)\n\n%s\n\n/approve <chat> activates an account, /disable <chat> switches it off.",
		len(users), strings.Join(lines, "\n"))
}

// handleSetStatus is /approve and /disable. The user hears about it: an
// approval is what they have been waiting for, and a disable explains why the
// bot went quiet.
func (b *Bot) handleSetStatus(ctx context.Context, chatID int64, args string, status string) {
	command := map[string]string{models.StatusActive: "/approve", models.StatusDisabled: "/disable"}[status]
	target, err := strconv.ParseInt(strings.TrimSpace(args), 10, 64)
	if err != nil {
		b.Notify(chatID, fmt.Sprintf("Usage: %s <chat id>. /users lists them.", command))
		return
	}
	before, _ := b.manager.GetUser(ctx, target)

	user, err := b.manager.SetUserStatus(ctx, target, status)
	switch {
	case errors.Is(err, usecase.ErrUserNotFound):
		b.Notify(chatID, fmt.Sprintf("No user with chat %d. /users lists them.", target))
		return
	case errors.Is(err, usecase.ErrAdminStatus):
		b.Notify(chatID, fmt.Sprintf("%s is an admin; an admin's account cannot be changed from the bot.", user.Email))
		return
	case err != nil:
		b.logger.Error("could not change the status", "chat_id", target, "status", status, "err", err)
		b.Notify(chatID, "❌ Could not change the status: "+err.Error())
		return
	}
	if before.Status == status {
		b.Notify(chatID, fmt.Sprintf("%s was already %s.", user.Email, status))
		return
	}

	b.logger.Info("status set by admin", "admin", chatID, "chat_id", target, "status", status)
	switch status {
	case models.StatusActive:
		b.Notify(target, "✅ Your account is approved. Add your weekly classes with /book, "+
			"and the bot books them every Sunday at 12:00.")
		b.Notify(chatID, fmt.Sprintf("✅ %s is active.", user.Email))
	case models.StatusDisabled:
		// A run in progress for them stops now, not at the opening.
		b.manager.CancelBooking(target)
		b.Notify(target, "⛔ Your account has been disabled by the admin. Nothing will be booked for you.")
		b.Notify(chatID, fmt.Sprintf("⛔ %s is disabled.", user.Email))
	}
}

// announcementGap keeps a broadcast well under Telegram's limit of about 30
// messages a second.
const announcementGap = 50 * time.Millisecond

// handleAnnouncement sends the admin's text to every active user, the admin
// included, so they see exactly what everyone got.
func (b *Bot) handleAnnouncement(ctx context.Context, chatID int64, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		b.Notify(chatID, "Usage: /announcement <message>\n\nIt goes to every active user, as plain text.")
		return
	}
	users, err := b.manager.ListUsers(ctx)
	if err != nil {
		b.logger.Error("could not list the users", "err", err)
		b.Notify(chatID, "❌ Could not read the users, so nothing was sent: "+err.Error())
		return
	}
	recipients := announcementRecipients(users)
	msg := "📢 Announcement\n\n" + text

	var failed []string
	for i, id := range recipients {
		if i > 0 {
			time.Sleep(announcementGap)
		}
		if err := b.deliver(id, msg); err != nil {
			failed = append(failed, fmt.Sprintf("chat %d: %v", id, err))
		}
	}
	b.logger.Info("announcement sent", "admin", chatID,
		"recipients", len(recipients), "failed", len(failed))

	report := fmt.Sprintf("📢 Sent to %d of %d active user(s).", len(recipients)-len(failed), len(recipients))
	if len(failed) > 0 {
		report += "\n\nNot delivered:\n" + strings.Join(failed, "\n")
	}
	b.Notify(chatID, report)
}

func announcementRecipients(users []models.User) []int64 {
	var ids []int64
	for _, u := range users {
		if u.IsActive() {
			ids = append(ids, u.ChatID)
		}
	}
	return ids
}

// sessionLine describes a stored WodBuster session for /status. It says what
// is known, not more: when WodBuster last accepted it, and when its login
// cookie runs out. A session the refresh has not touched for a day is flagged,
// because the refresh runs twice a day. The expiry date is given in loc, the
// gym's time zone, so it is the day the athlete would say; nil means UTC.
func sessionLine(st models.StoredSession, ok bool, err error, now time.Time, loc *time.Location) string {
	if err != nil {
		return "❓ could not be read right now"
	}
	if !ok {
		return "⚠️ none stored: the next run will log in"
	}
	icon, verb := "✅", "stored"
	if now.Sub(st.LastUsedAt) > 24*time.Hour {
		icon, verb = "⚠️", "stored but not checked recently"
	}
	s := fmt.Sprintf("%s %s · last accepted by WodBuster %s", icon, verb, ago(now.Sub(st.LastUsedAt)))
	if st.AuthExpiresAt.IsZero() {
		return s + " · login cookie ends with the session"
	}
	return s + " · login cookie expires " + st.AuthExpiresAt.In(orUTC(loc)).Format("2006-01-02")
}

func orUTC(loc *time.Location) *time.Location {
	if loc == nil {
		return time.UTC
	}
	return loc
}

// ago is a duration said the way a person would: "just now", "5m ago",
// "3h ago", "2d ago".
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}

func statusLine(u models.User) string {
	var s string
	switch u.Status {
	case models.StatusActive:
		s = "✅ Active"
	case models.StatusDisabled:
		s = "⛔ Disabled"
	default:
		s = "⏳ Waiting for approval"
	}
	if u.IsAdmin() {
		s += " (admin)"
	}
	return s
}
