package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/utils"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/wodbuster"
)

// SessionManager handles multiple users with dedicated browser contexts
type SessionManager struct {
	storage       Storage
	logger        *slog.Logger
	baseURL       string
	encryptionKey string
	clients       map[int64]*wodbuster.Client // One client per user
	clientsMux    sync.RWMutex
}

func NewSessionManager(storage Storage, logger *slog.Logger, baseURL, encryptionKey string) *SessionManager {
	return &SessionManager{
		storage:       storage,
		logger:        logger,
		baseURL:       baseURL,
		encryptionKey: encryptionKey,
		clients:       make(map[int64]*wodbuster.Client),
	}
}

// GetOrCreateUserClient gets existing client or creates new dedicated one for user
func (sm *SessionManager) GetOrCreateUserClient(ctx context.Context, chatID int64) (*wodbuster.Client, error) {
	sm.clientsMux.Lock()
	defer sm.clientsMux.Unlock()

	// Check if client already exists for this user
	if client, exists := sm.clients[chatID]; exists {
		return client, nil
	}

	// Create new dedicated client for this user
	client, err := wodbuster.NewClient(sm.baseURL,
		wodbuster.WithLogger(sm.logger),
		wodbuster.WithDedicatedContext(), // Each user gets their own browser context
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create client for user %d: %w", chatID, err)
	}

	sm.clients[chatID] = client
	sm.logger.Info("Created dedicated client for user", "chat_id", chatID)

	return client, nil
}

// CloseUserClient closes and removes client for specific user
func (sm *SessionManager) CloseUserClient(chatID int64) {
	sm.clientsMux.Lock()
	defer sm.clientsMux.Unlock()

	if client, exists := sm.clients[chatID]; exists {
		client.Close()
		delete(sm.clients, chatID)
		sm.logger.Info("Closed client for user", "chat_id", chatID)
	}
}

// CloseAllClients closes all user clients
func (sm *SessionManager) CloseAllClients() {
	sm.clientsMux.Lock()
	defer sm.clientsMux.Unlock()

	for chatID, client := range sm.clients {
		client.Close()
		sm.logger.Info("Closed client for user", "chat_id", chatID)
	}
	sm.clients = make(map[int64]*wodbuster.Client)
}

// EnsureUserSessionReady ensures user has valid session loaded
func (sm *SessionManager) EnsureUserSessionReady(ctx context.Context, chatID int64) error {
	// Get or create dedicated client for user
	client, err := sm.GetOrCreateUserClient(ctx, chatID)
	if err != nil {
		return err
	}

	// Get user from storage
	user, exists := sm.storage.GetUser(ctx, chatID)
	if !exists {
		return fmt.Errorf("user %d not found", chatID)
	}

	// Check if user has valid session
	if user.HasValidSession() {
		sm.logger.Info("Loading existing session from storage", "chat_id", chatID)

		// Create cookie from stored session
		sessionCookie := &http.Cookie{
			Name:  "ASP.NET_SessionId", // Adjust based on WODBuster's actual cookie name
			Value: user.WODBusterSessionCookie,
			Path:  "/",
		}

		// Load stored session into client
		if err := client.LoadStoredSession(ctx, []*http.Cookie{sessionCookie}); err != nil {
			sm.logger.Warn("Failed to load stored session, performing fresh login",
				"chat_id", chatID, "error", err)
			return sm.performFreshLogin(ctx, chatID, client, user)
		}

		return nil
	}

	// No valid session found, perform fresh login
	return sm.performFreshLogin(ctx, chatID, client, user)
}

// performFreshLogin performs fresh login and saves session
func (sm *SessionManager) performFreshLogin(ctx context.Context, chatID int64, client *wodbuster.Client, user models.User) error {
	// Decrypt password
	password, err := utils.DecryptPassword(user.Password, sm.encryptionKey)
	if err != nil {
		return fmt.Errorf("failed to decrypt password for user %d: %w", chatID, err)
	}

	sm.logger.Info("Performing fresh login", "chat_id", chatID, "email", user.Email)

	// Perform login
	_, err = client.LogIn(ctx, user.Email, password)
	if err != nil {
		return fmt.Errorf("login failed for user %d: %w", chatID, err)
	}

	// Save session to storage
	return sm.saveUserSession(ctx, chatID, client)
}

// saveUserSession extracts and saves user session to storage
func (sm *SessionManager) saveUserSession(ctx context.Context, chatID int64, client *wodbuster.Client) error {
	// Get cookies from client
	if err := client.SaveCookies(); err != nil {
		return fmt.Errorf("failed to extract cookies: %w", err)
	}

	// Extract the WODBuster session cookie (we only need the main session cookie)
	var wodBusterSessionCookie string
	for _, cookie := range client.GetCookies() {
		// Look for the main WODBuster session cookie (adjust name as needed)
		if cookie.Name == "ASP.NET_SessionId" || cookie.Name == "PHPSESSID" || cookie.Name == "sessionid" {
			wodBusterSessionCookie = cookie.Value
			break
		}
	}

	if wodBusterSessionCookie == "" {
		return fmt.Errorf("no valid session cookie found")
	}

	// Get user and update session
	user, exists := sm.storage.GetUser(ctx, chatID)
	if !exists {
		return fmt.Errorf("user %d not found", chatID)
	}

	// Update session in user model
	user.UpdateSession(wodBusterSessionCookie, time.Now().Add(24*time.Hour))

	return sm.storage.SaveUser(ctx, user)
}

// WaitAndBookForUser performs booking for specific user with their dedicated context
func (sm *SessionManager) WaitAndBookForUser(ctx context.Context, chatID int64, booking models.BookingWindow) error {
	// Ensure user session is ready
	if err := sm.EnsureUserSessionReady(ctx, chatID); err != nil {
		return fmt.Errorf("failed to prepare session for user %d: %w", chatID, err)
	}

	// Get user's dedicated client
	client, err := sm.GetOrCreateUserClient(ctx, chatID)
	if err != nil {
		return err
	}

	sm.logger.Info("Starting booking process for user",
		"chat_id", chatID,
		"day", booking.Day,
		"hour", booking.Hour,
		"class_type", booking.ClassType)

	// Navigate to booking page early
	if err := sm.navigateToBookingPage(ctx, client, booking.Day); err != nil {
		return fmt.Errorf("failed to navigate to booking page: %w", err)
	}

	// Wait for booking window to open
	if err := sm.waitForBookingWindow(ctx, client, booking); err != nil {
		return fmt.Errorf("failed while waiting for booking window: %w", err)
	}

	// Attempt booking immediately when window opens
	return sm.attemptBooking(ctx, client, booking, chatID)
}

func (sm *SessionManager) navigateToBookingPage(ctx context.Context, client *wodbuster.Client, day string) error {
	// Implementation would use client to navigate to booking page
	// This is simplified for the example
	sm.logger.Info("Navigating to booking page", "day", day)
	return nil
}

func (sm *SessionManager) waitForBookingWindow(ctx context.Context, client *wodbuster.Client, booking models.BookingWindow) error {
	// Implementation would wait for booking buttons to appear
	// This is simplified for the example
	sm.logger.Info("Waiting for booking window to open")
	return nil
}

func (sm *SessionManager) attemptBooking(ctx context.Context, client *wodbuster.Client, booking models.BookingWindow, chatID int64) error {
	// Implementation would attempt to book the class
	// This is simplified for the example
	sm.logger.Info("Attempting to book class", "chat_id", chatID)
	return nil
}

func (sm *SessionManager) GetActiveUserCount() int {
	sm.clientsMux.RLock()
	defer sm.clientsMux.RUnlock()
	return len(sm.clients)
}
