package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/robfig/cron/v3"
)

// BookingContext represents an active booking attempt
type BookingContext struct {
	ChatID      int64
	BookingData models.BookingWindow
	Cancel      context.CancelFunc
	Status      string
}

// BookingScheduler handles Saturday cronjob and parallel booking
type BookingScheduler struct {
	storage           Storage
	clientAPI         APIClient
	logger            *slog.Logger
	cron              *cron.Cron
	activeBookings    map[int64]*BookingContext
	activeBookingsMux sync.RWMutex
	isRunning         bool
}

func NewBookingScheduler(storage Storage, clientAPI APIClient, logger *slog.Logger) *BookingScheduler {
	return &BookingScheduler{
		storage:        storage,
		clientAPI:      clientAPI,
		logger:         logger,
		cron:           cron.New(),
		activeBookings: make(map[int64]*BookingContext),
	}
}

// Start begins the Saturday 11:55 cronjob
func (bs *BookingScheduler) Start() error {
	if bs.isRunning {
		return fmt.Errorf("booking scheduler is already running")
	}

	// Schedule for every Saturday at 11:55 AM
	// Cron format: "MIN HOUR DAY_OF_MONTH MONTH DAY_OF_WEEK"
	// 55 11 * * 6 = 11:55 AM every Saturday (6 = Saturday)
	_, err := bs.cron.AddFunc("55 11 * * 6", bs.processAllBookings)
	if err != nil {
		return fmt.Errorf("failed to schedule cronjob: %w", err)
	}

	bs.cron.Start()
	bs.isRunning = true
	bs.logger.Info("Booking scheduler started - will run every Saturday at 11:55 AM")

	return nil
}

// Stop stops the booking scheduler
func (bs *BookingScheduler) Stop() {
	if !bs.isRunning {
		return
	}

	bs.cron.Stop()
	bs.isRunning = false

	// Cancel all active bookings
	bs.activeBookingsMux.Lock()
	for chatID, booking := range bs.activeBookings {
		booking.Cancel()
		bs.logger.Info("Cancelled active booking", "chat_id", chatID)
	}
	bs.activeBookings = make(map[int64]*BookingContext)
	bs.activeBookingsMux.Unlock()

	bs.logger.Info("Booking scheduler stopped")
}

// processAllBookings processes all pending bookings (called by cronjob)
func (bs *BookingScheduler) processAllBookings() {
	bs.logger.Info("🚀 Saturday 11:55 - Starting booking process for all users")

	ctx := context.Background()

	// Get all pending booking attempts
	bookingAttempts, err := bs.storage.GetAllPendingBookings(ctx)
	if err != nil {
		bs.logger.Error("Failed to get pending bookings", "error", err)
		return
	}

	if len(bookingAttempts) == 0 {
		bs.logger.Info("No pending bookings found")
		return
	}

	bs.logger.Info("Processing bookings", "count", len(bookingAttempts))

	// Process each booking concurrently
	var wg sync.WaitGroup
	for _, attempt := range bookingAttempts {
		wg.Add(1)
		go func(booking models.BookingAttempt) {
			defer wg.Done()
			bs.processUserBooking(ctx, booking)
		}(attempt)
	}

	// Wait for all bookings to complete (or timeout)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// Wait up to 10 minutes for all bookings
	select {
	case <-done:
		bs.logger.Info("All booking attempts completed")
	case <-time.After(10 * time.Minute):
		bs.logger.Warn("Booking timeout reached - some bookings may still be in progress")
	}
}

// processUserBooking processes booking for a single user
func (bs *BookingScheduler) processUserBooking(ctx context.Context, booking models.BookingAttempt) {
	// Create cancellable context for this booking
	bookingCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	// Add random delay to avoid synchronized requests (800-1200ms)
	delay := time.Duration(800+booking.ChatID%400) * time.Millisecond
	bs.logger.Info("Starting booking with delay",
		"chat_id", booking.ChatID,
		"delay_ms", delay.Milliseconds(),
		"class", fmt.Sprintf("%s %s %s", booking.Day, booking.Hour, booking.ClassType))

	time.Sleep(delay)

	// Track active booking
	bookingContext := &BookingContext{
		ChatID: booking.ChatID,
		BookingData: models.BookingWindow{
			Day:       booking.Day,
			Hour:      booking.Hour,
			ClassType: booking.ClassType,
			OpensAt:   time.Now().Add(5 * time.Minute), // Opens at 12:00
		},
		Cancel: cancel,
		Status: "active",
	}

	bs.activeBookingsMux.Lock()
	bs.activeBookings[booking.ChatID] = bookingContext
	bs.activeBookingsMux.Unlock()

	// Update booking status to active
	if err := bs.storage.UpdateBookingStatus(bookingCtx, booking.ID, "active", ""); err != nil {
		bs.logger.Error("Failed to update booking status", "booking_id", booking.ID, "error", err)
	}

	// Perform the booking using APIClient
	err := bs.performBookingForUser(bookingCtx, booking.ChatID, bookingContext.BookingData)

	// Update final status
	status := "success"
	errorMsg := ""
	if err != nil {
		status = "failed"
		errorMsg = err.Error()
		bs.logger.Error("Booking failed", "chat_id", booking.ChatID, "error", err)
	} else {
		bs.logger.Info("Booking successful", "chat_id", booking.ChatID)
	}

	// Update booking attempt in storage
	if updateErr := bs.storage.UpdateBookingStatus(bookingCtx, booking.ID, status, errorMsg); updateErr != nil {
		bs.logger.Error("Failed to update final booking status", "booking_id", booking.ID, "error", updateErr)
	}

	// Remove from active bookings
	bs.activeBookingsMux.Lock()
	delete(bs.activeBookings, booking.ChatID)
	bs.activeBookingsMux.Unlock()
}

// GetActiveBookings returns currently active booking attempts
func (bs *BookingScheduler) GetActiveBookings() map[int64]*BookingContext {
	bs.activeBookingsMux.RLock()
	defer bs.activeBookingsMux.RUnlock()

	// Return copy to avoid race conditions
	result := make(map[int64]*BookingContext)
	for k, v := range bs.activeBookings {
		result[k] = v
	}
	return result
}

// CancelBooking cancels an active booking attempt
func (bs *BookingScheduler) CancelBooking(chatID int64) bool {
	bs.activeBookingsMux.Lock()
	defer bs.activeBookingsMux.Unlock()

	if booking, exists := bs.activeBookings[chatID]; exists {
		booking.Cancel()
		booking.Status = "cancelled"
		delete(bs.activeBookings, chatID)
		bs.logger.Info("Cancelled booking", "chat_id", chatID)
		return true
	}
	return false
}

// IsRunning returns whether the scheduler is currently running
func (bs *BookingScheduler) IsRunning() bool {
	return bs.isRunning
}

// GetNextRunTime returns when the scheduler will next run
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

// GetScheduleInfo returns human-readable schedule information
func (bs *BookingScheduler) GetScheduleInfo() string {
	if !bs.isRunning {
		return "Scheduler is not running"
	}

	nextRun := bs.GetNextRunTime()
	if nextRun.IsZero() {
		return "No scheduled runs found"
	}

	timeUntilNext := time.Until(nextRun)
	return fmt.Sprintf("Next booking run: %s (in %s)",
		nextRun.Format("Monday, January 2, 2006 at 15:04 MST"),
		timeUntilNext.Round(time.Minute))
}

// performBookingForUser uses APIClient to perform booking for specific user
func (bs *BookingScheduler) performBookingForUser(ctx context.Context, chatID int64, booking models.BookingWindow) error {
	// Get user from storage
	user, exists := bs.storage.GetUser(ctx, chatID)
	if !exists {
		return fmt.Errorf("user %d not found", chatID)
	}

	bs.logger.Info("Starting booking for user",
		"chat_id", chatID,
		"email", user.Email,
		"day", booking.Day,
		"hour", booking.Hour,
		"class_type", booking.ClassType)

	// Wait for booking window to open (12:00 PM)
	if err := bs.waitForBookingWindow(ctx, booking); err != nil {
		return fmt.Errorf("failed while waiting for booking window: %w", err)
	}

	// Use APIClient to perform booking - it will handle session management, login, etc.
	return bs.clientAPI.BookClass(ctx, user.Email, "", booking.Day, booking.ClassType, booking.Hour)
}

// waitForBookingWindow waits until booking window opens at 12:00 PM
func (bs *BookingScheduler) waitForBookingWindow(ctx context.Context, booking models.BookingWindow) error {
	now := time.Now()
	openTime := booking.OpensAt

	if now.Before(openTime) {
		waitDuration := openTime.Sub(now)
		bs.logger.Info("Waiting for booking window to open",
			"wait_duration", waitDuration,
			"opens_at", openTime)

		select {
		case <-time.After(waitDuration):
			bs.logger.Info("Booking window is now open!")
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}
