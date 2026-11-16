package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/utils"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type LogInManager interface {
	IsAuthenticated(ctx context.Context, chatID int64) bool
	LogInAndSave(ctx context.Context, chatID int64, email, password string) error
}

type LogInBotAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
}

type LoginHandler struct {
	api     LogInBotAPI
	manager LogInManager
}

func NewLoginHandler(api LogInBotAPI, logInManager LogInManager) *LoginHandler {
	return &LoginHandler{
		api:     api,
		manager: logInManager,
	}
}

// Handle checks and saves the athlete's WodBuster login. It reports whether the
// login was saved, so the bot can say what happens next — which depends on
// whether the account is approved yet.
func (h *LoginHandler) Handle(update tgbotapi.Update) bool {
	ctx := context.Background()

	args := strings.Split(update.Message.Text, " ")
	if len(args) != 3 {
		h.sendMessage(update.Message.Chat.ID,
			"Please provide email and password: /login email password")
		return false
	}

	email := utils.SanitizeInput(args[1])
	password := utils.SanitizeInput(args[2])

	// Validate email format
	if err := utils.ValidateEmail(email); err != nil {
		h.sendMessage(update.Message.Chat.ID,
			"Please provide a valid email address")
		return false
	}

	// Validate password requirements
	if err := utils.ValidatePassword(password); err != nil {
		h.sendMessage(update.Message.Chat.ID,
			fmt.Sprintf("Invalid password: %s", err.Error()))
		return false
	}

	// if err := h.wodbuster.Login(email, password); err != nil {
	// 	h.sendMessage(update.Message.Chat.ID,
	// 		"Login failed. Please check your credentials and try again.")
	// 	return
	// }

	// A login drives a browser against the real site, so it takes seconds and
	// is worth timing: a slow one here is the first sign of trouble on Sunday.
	started := time.Now()
	slog.Info("login attempt", "chat_id", update.Message.Chat.ID, "email", email)

	if err := h.manager.LogInAndSave(ctx, update.Message.Chat.ID, email, password); err != nil {
		h.sendMessage(update.Message.Chat.ID,
			"Login failed: "+err.Error()+"\nCheck your email and password and try again.")
		slog.Error("login failed", "error", err, "chat_id", update.Message.Chat.ID,
			"email", email, "took", time.Since(started).Round(time.Millisecond))
		return false
	}

	slog.Info("login succeeded", "chat_id", update.Message.Chat.ID, "email", email,
		"took", time.Since(started).Round(time.Millisecond))
	h.sendMessage(update.Message.Chat.ID, "✅ Login successful.")
	return true
}

func (h *LoginHandler) sendMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := h.api.Send(msg); err != nil {
		slog.Error("Failed to send message",
			"error", err,
			"chat_id", chatID)
	}
}
