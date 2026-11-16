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
		if b.admin != 0 && b.admin != chatID {
			b.Notify(b.admin, signupMessage(user, username))
		}
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

func (b *Bot) handleUsers(ctx context.Context, chatID int64) {
	users, err := b.manager.ListUsers(ctx)
	if err != nil {
		b.logger.Error("could not list the users", "err", err)
		b.Notify(chatID, "❌ Could not read the users: "+err.Error())
		return
	}
	b.Notify(chatID, formatUsers(users))
}

// formatUsers lists pending accounts first, since they are the ones waiting
// on the admin.
func formatUsers(users []models.User) string {
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
