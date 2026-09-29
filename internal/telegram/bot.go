package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/booking"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/handlers"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/usecase"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/utils"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// BotManager defines the interface the bot needs from the manager
type BotManager interface {
	IsAuthenticated(ctx context.Context, chatID int64) bool
	GetUser(ctx context.Context, chatID int64) (models.User, bool)
	LogInAndSave(ctx context.Context, chatID int64, email, password string) error
	ScheduleBookClass(ctx context.Context, chatID int64, class models.ClassBookingSchedule) error
	GetActiveBookings() map[int64]*usecase.BookingContext
	CancelBooking(chatID int64) bool
	TestUserSession(ctx context.Context, chatID int64) error
	GetScheduleInfo() string
	Rehearse(ctx context.Context, chatID int64) ([]booking.Outcome, error)
}

type Bot struct {
	api          *tgbotapi.BotAPI
	logger       *slog.Logger
	manager      BotManager
	loginHandler *handlers.LoginHandler
	bookHandler  *handlers.BookingHandler
	rateLimiter  *utils.RateLimiter
	stopChan     chan struct{}
	// removeHandler *handlers.RemoveHandler
}

func New(token string, manager BotManager, logger *slog.Logger) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		switch err.Error() {
		case "Not Found", "Unauthorized":
			logger.Error("Invalid token. Please check your TELEGRAM_BOT_TOKEN is correct",
				"error", err)
		default:
			logger.Error("Failed to initialize bot",
				"error", err)
		}
		return nil, err
	}

	api.Debug = false // Can be made configurable if needed
	logger.Info("Bot authorized successfully",
		"user", api.Self.UserName)

	// Create rate limiter: 1 command per 2 seconds, max 5 tokens
	rateLimiter := utils.NewRateLimiter(2*time.Second, 5)

	return &Bot{
		api:          api,
		logger:       logger,
		manager:      manager,
		loginHandler: handlers.NewLoginHandler(api, manager),
		bookHandler:  handlers.NewBookingHandler(api, manager),
		rateLimiter:  rateLimiter,
	}, nil
}

func (b *Bot) Start() error {
	b.stopChan = make(chan struct{})

	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 60

	updates := b.api.GetUpdatesChan(updateConfig)

	// Start cleanup goroutine for rate limiter
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				b.rateLimiter.Cleanup(24 * time.Hour)
			case <-b.stopChan:
				return
			}
		}
	}()

	for {
		select {
		case update := <-updates:
			if update.Message == nil {
				continue
			}
			go b.handleUpdate(update)
		case <-b.stopChan:
			b.logger.Info("Bot stopping...")
			return nil
		}
	}
}

func (b *Bot) Stop() error {
	if b.stopChan != nil {
		close(b.stopChan)
	}
	return nil
}

func (b *Bot) handleUpdate(update tgbotapi.Update) {
	if update.Message == nil {
		return
	}

	// Check rate limit
	if !b.rateLimiter.Allow(update.Message.Chat.ID) {
		b.sendMessage(update.Message.Chat.ID,
			"You're sending commands too quickly. Please wait a moment before trying again.")
		return
	}

	switch update.Message.Command() {
	case "start":
		b.sendMessage(update.Message.Chat.ID,
			"Welcome to WODBuster Bot! 🏋️‍♂️\n\n"+
				"This bot helps you automatically book your fitness classes.\n"+
				"Please use /login to authenticate first.")
	case "login":
		b.loginHandler.Handle(update)
	case "book":
		b.bookHandler.Handle(update)
	case "status":
		b.handleStatus(update)
	case "test":
		b.handleTestSession(update)
	case "active":
		b.handleActiveBookings(update)
	case "schedule":
		b.handleSchedule(update)
	case "rehearse":
		b.handleRehearse(update)
	case "help":
		b.sendMessage(update.Message.Chat.ID,
			"🤖 **WODBuster Bot Commands**\n\n"+
				"**Authentication:**\n"+
				"• `/login email password` - Login to WODBuster\n"+
				"• `/test` - Test your current session\n\n"+
				"**Booking:**\n"+
				"• `/book day hour class-type` - Schedule a class\n"+
				"  Example: `/book Monday 10:00 wod`\n"+
				"• `/active` - Show active booking attempts\n"+
				"• `/status` - Show your account status\n"+
				"• `/schedule` - Show when the next run happens\n"+
				"• `/rehearse` - Try your classes now, booking nothing\n\n"+
				"**Other:**\n"+
				"• `/help` - Show this help message\n\n"+
				"**How it works:**\n"+
				"1. Login with your WODBuster credentials\n"+
				"2. Schedule classes with `/book`\n"+
				"3. On Sunday the bot wakes up before noon and books your classes the moment the week opens!")
	default:
		b.sendMessage(update.Message.Chat.ID,
			"I don't know that command. Use /help to see available commands")
	}
}

func (b *Bot) handleStatus(update tgbotapi.Update) {
	ctx := context.Background()
	chatID := update.Message.Chat.ID

	user, exists := b.manager.GetUser(ctx, chatID)
	if !exists {
		b.sendMessage(chatID, "❌ You are not registered. Please use /login first.")
		return
	}

	status := "🔴 Not authenticated"
	if user.IsAuthenticated {
		status = "✅ Authenticated"
	}

	scheduleCount := len(user.ClassBookingSchedules)

	message := "📊 **Your Status**\n\n" +
		"Authentication: " + status + "\n" +
		"Email: " + user.Email + "\n" +
		"Scheduled Classes: " + fmt.Sprintf("%d", scheduleCount) + "\n\n"

	if scheduleCount > 0 {
		message += "**Scheduled Classes:**\n"
		for _, class := range user.ClassBookingSchedules {
			message += "• " + class.Day + " " + class.Hour + " - " + class.ClassType + "\n"
		}
	}

	b.sendMessage(chatID, message)
}

func (b *Bot) handleTestSession(update tgbotapi.Update) {
	ctx := context.Background()
	chatID := update.Message.Chat.ID

	if !b.manager.IsAuthenticated(ctx, chatID) {
		b.sendMessage(chatID, "❌ You are not authenticated. Please use /login first.")
		return
	}

	b.sendMessage(chatID, "🧪 Testing your session...")

	if err := b.manager.TestUserSession(ctx, chatID); err != nil {
		b.sendMessage(chatID, "❌ Session test failed: "+err.Error()+"\nPlease use /login to authenticate again.")
		return
	}

	b.sendMessage(chatID, "✅ Your session is working correctly!")
}

func (b *Bot) handleActiveBookings(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	activeBookings := b.manager.GetActiveBookings()

	userBooking, hasActiveBooking := activeBookings[chatID]
	if !hasActiveBooking {
		b.sendMessage(chatID, "📭 You have no active booking attempts right now.")
		return
	}

	message := "🚀 **Active Booking**\n\n" +
		"Status: " + userBooking.Status + "\n" +
		"Class: " + userBooking.BookingData.Day + " " + userBooking.BookingData.Hour + " - " + userBooking.BookingData.ClassType + "\n"

	b.sendMessage(chatID, message)
}

// Notify delivers a booking result to an athlete. It is how the Sunday run
// reports back: nobody is watching the logs at noon.
func (b *Bot) Notify(chatID int64, text string) {
	b.sendMessage(chatID, text)
}

// handleRehearse runs the athlete's classes right now against the published
// week and books nothing, so a broken login or a renamed class is found on a
// Tuesday rather than at the opening.
func (b *Bot) handleRehearse(update tgbotapi.Update) {
	ctx := context.Background()
	chatID := update.Message.Chat.ID

	if !b.manager.IsAuthenticated(ctx, chatID) {
		b.sendMessage(chatID, "❌ You are not authenticated. Please use /login first.")
		return
	}

	b.sendMessage(chatID, "🧪 Rehearsing: logging in and resolving your classes. Nothing will be booked.\n"+
		"This takes a few seconds.")

	outcomes, err := b.manager.Rehearse(ctx, chatID)
	if err != nil {
		b.sendMessage(chatID, "❌ Rehearsal failed: "+err.Error())
		return
	}

	msg := "🧪 **Rehearsal results**\n\n"
	for _, o := range outcomes {
		msg += formatOutcome(o) + "\n"
	}
	msg += "\nA class the box has not published yet cannot be found — that is expected " +
		"before the opening, not a failure of your setup."
	b.sendMessage(chatID, msg)
}

func formatOutcome(o booking.Outcome) string {
	if o.Err != nil {
		return "❌ " + o.Class + ": " + o.Err.Error()
	}
	if o.Status == "dry-run" {
		return "✅ " + o.Class + " — found and bookable"
	}
	return "• " + o.Class + " — " + o.Status
}

func (b *Bot) handleSchedule(update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	scheduleInfo := b.manager.GetScheduleInfo()

	message := "⏰ **Booking Schedule**\n\n" + scheduleInfo
	b.sendMessage(chatID, message)
}

func (b *Bot) sendMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown

	if _, err := b.api.Send(msg); err != nil {
		b.logger.Error("Failed to send message", "error", err, "chat_id", chatID)
	}
}
