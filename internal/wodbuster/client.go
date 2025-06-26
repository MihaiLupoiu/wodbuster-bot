package wodbuster

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type Client struct {
	ctx     context.Context
	cancel  context.CancelFunc
	logger  *slog.Logger
	baseURL string
	cookies []*http.Cookie // Store session cookies
}

// Option defines the method to customize the Client.
type Option func(*Client)

// WithContext allows setting a custom context for the client.
func WithContext(ctx context.Context) Option {
	return func(c *Client) {
		if ctx != nil {
			if c.cancel != nil {
				c.cancel()
			}
			var cancel context.CancelFunc
			c.ctx, cancel = chromedp.NewContext(ctx)
			c.cancel = cancel
		}
	}
}

// WithLogger allows setting a custom logger for the client.
func WithLogger(logger *slog.Logger) Option {
	return func(c *Client) {
		if logger != nil {
			c.logger = logger
		}
	}
}

// WithStoredCookies allows initializing client with pre-stored cookies from MongoDB
func WithStoredCookies(cookies []*http.Cookie) Option {
	return func(c *Client) {
		if cookies != nil {
			c.cookies = cookies
		}
	}
}

// WithDedicatedContext creates a new dedicated browser context for this client instance
func WithDedicatedContext() Option {
	return func(c *Client) {
		// Create a new allocator context for this client
		allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(),
			append(chromedp.DefaultExecAllocatorOptions[:],
				chromedp.Flag("headless", true),
				chromedp.Flag("no-sandbox", true),
				chromedp.Flag("disable-gpu", true),
				chromedp.Flag("disable-dev-shm-usage", true),
			)...)

		// Create browser context with timeout
		ctx, cancel := chromedp.NewContext(allocCtx)
		ctx, _ = context.WithTimeout(ctx, 60*time.Second)

		// Cancel the old context if it exists
		if c.cancel != nil {
			c.cancel()
		}

		c.ctx = ctx
		c.cancel = func() {
			cancel()
			allocCancel()
		}
	}
}

func NewClient(baseURL string, opts ...Option) (*Client, error) {
	if baseURL == "" {
		return nil, ErrMissingBaseURL
	}

	// Create default client with background context and timeout
	ctx, cancel := chromedp.NewContext(context.Background())
	// Set a reasonable timeout for operations
	ctx, _ = context.WithTimeout(ctx, 60*time.Second)

	client := &Client{
		ctx:     ctx,
		cancel:  cancel,
		baseURL: baseURL,
		logger:  slog.Default(),
	}

	// Apply options
	for _, opt := range opts {
		opt(client)
	}

	return client, nil
}

func (c *Client) Close() {
	c.cancel()
}

var (
	ErrMissingBaseURL = errors.New("base URL is required")
	ErrNotImplemented = errors.New("method not implemented")
)

func (c *Client) LogIn(ctx context.Context, email, password string) (string, error) {
	if email == "" || password == "" {
		return "", fmt.Errorf("email and password are required")
	}

	// Check if we have valid session cookies first
	if len(c.cookies) > 0 {
		if err := c.RestoreCookies(); err != nil {
			c.logger.Warn("Failed to restore cookies, proceeding with fresh login", "error", err)
		} else {
			// Test if session is still valid by navigating to a protected page
			if err := chromedp.Run(c.ctx, chromedp.Navigate(c.baseURL+"/schedule")); err == nil {
				c.logger.Info("Session restored successfully", "username", email)
				return "", nil
			}
		}
	}

	// Perform fresh login
	actions := []chromedp.Action{
		chromedp.Navigate(c.baseURL + "/user"),
		chromedp.WaitVisible(`//input[@id="body_body_CtlLogin_IoEmail"]`),
		chromedp.WaitVisible(`//input[@id="body_body_CtlLogin_IoPassword"]`),
		chromedp.SendKeys(`//input[@id="body_body_CtlLogin_IoEmail"]`, email),
		chromedp.SendKeys(`//input[@id="body_body_CtlLogin_IoPassword"]`, password),
		chromedp.Sleep(2 * time.Second), // Human-like delay
		chromedp.Click(`//input[@id="body_body_CtlLogin_CtlAceptar"]`),
		chromedp.WaitNotPresent(`//input[@id="body_body_CtlLogin_CtlAceptar"]`),
	}

	// Add remember browser step if needed
	actions = append(actions, []chromedp.Action{
		chromedp.WaitVisible(`//input[@id="body_body_CtlConfiar_CtlNoSeguro"]`),
		chromedp.Click(`//input[@id="body_body_CtlConfiar_CtlNoSeguro"]`),
		chromedp.WaitNotPresent(`//input[@id="body_body_CtlConfiar_CtlNoSeguro"]`),
	}...)

	if err := chromedp.Run(c.ctx, actions...); err != nil {
		return "", fmt.Errorf("login failed: %w", err)
	}

	// Save session cookies for future use
	if err := c.SaveCookies(); err != nil {
		c.logger.Warn("Failed to save session cookies", "error", err)
	}

	c.logger.Info("Successfully logged in",
		"username", email,
		"url", c.baseURL)
	return "", nil
}

func login(baseURL, email, password string) []chromedp.Action {
	return []chromedp.Action{
		// Navigate to login page
		chromedp.Navigate(baseURL + "/user"),

		// Wait for form elements to be present
		chromedp.WaitVisible(`//input[@id="body_body_CtlLogin_IoEmail"]`),
		chromedp.WaitVisible(`//input[@id="body_body_CtlLogin_IoPassword"]`),

		// Fill in the form
		chromedp.SendKeys(`//input[@id="body_body_CtlLogin_IoEmail"]`, email),
		chromedp.SendKeys(`//input[@id="body_body_CtlLogin_IoPassword"]`, password),

		chromedp.Sleep(2 * time.Second), // Simulating human delay

		// Click login button
		chromedp.Click(`//input[@id="body_body_CtlLogin_CtlAceptar"]`),

		// Wait for button to disappear
		chromedp.WaitNotPresent(`//input[@id="body_body_CtlLogin_CtlAceptar"]`),
	}
}

func rememberBrowser() []chromedp.Action {
	return []chromedp.Action{
		// Wait for remember session confirmation button
		chromedp.WaitVisible(`//input[@id="body_body_CtlConfiar_CtlNoSeguro"]`),
		chromedp.Click(`//input[@id="body_body_CtlConfiar_CtlNoSeguro"]`),
		chromedp.WaitNotPresent(`//input[@id="body_body_CtlConfiar_CtlNoSeguro"]`),
	}
}

func (c *Client) GetAvailableClasses(email, password string) ([]ClassSchedule, error) {
	actions := login(c.baseURL, email, password)
	actions = append(actions, rememberBrowser()...)
	actions = append(actions, getAvailableClasses()...)
	actions = append(actions,
		// Wait for the next page to load
		chromedp.Sleep(5*time.Second))
	if err := chromedp.Run(c.ctx, actions...); err != nil {
		return nil, fmt.Errorf("failed to navigate: %w", err)
	}
	return nil, ErrNotImplemented
}

func getAvailableClasses() []chromedp.Action {
	return []chromedp.Action{
		// Click the link
		chromedp.Click(`//a[contains(text(), 'Reservar clases')]`),
		// Wait for form elements to be present
		chromedp.WaitVisible(`//div[@id="calendar"]`),
		// Click on the next week button
		chromedp.Click(`a.next.icon`, chromedp.ByQuery),
	}
}

func bookClass(hour string) []chromedp.Action {
	// Construct XPath to find the "Reservar" button based on the hour and class 'entrenar' button
	xpathReserve := fmt.Sprintf(`//div[contains(@class, 'clase')]//div[@class='hora' and text()='%s']/ancestor::div[contains(@class, 'clase')]//button[contains(@class, 'entrenar')]`, hour)

	return []chromedp.Action{
		// Wait for the "Reservar" button to appear
		chromedp.WaitVisible(xpathReserve),
		// Click the "Reservar" button
		chromedp.Click(xpathReserve),

		chromedp.Sleep(1 * time.Second),
	}
}

func acceptConfirmation() []chromedp.Action {
	// XPath for the "Aceptar" button inside the confirmation dialog
	xpath := `//div[h4[text()='Confirmación Requerida']]//button[contains(@class, 'button small radius') and text()='Aceptar']`

	return []chromedp.Action{
		// Wait for the confirmation dialog to appear
		chromedp.WaitVisible(xpath),
		// Click the "Aceptar" button
		chromedp.Click(xpath),
		chromedp.Sleep(1 * time.Second),
	}
}

func (c *Client) BookClass(_ context.Context, email, password string, day, hour string) error {
	if day == "" || hour == "" {
		return fmt.Errorf("day and hour are required")
	}

	actions := login(c.baseURL, email, password)
	actions = append(actions, rememberBrowser()...)
	actions = append(actions, getAvailableClasses()...)
	actions = append(actions, selectDay(day)...)
	//actions = append(actions, selectHour(hour)...)
	actions = append(actions, bookClass(hour)...)
	actions = append(actions, acceptConfirmation()...)
	actions = append(actions,
		// Wait for the next page to load
		chromedp.Sleep(10*time.Second))
	if err := chromedp.Run(c.ctx, actions...); err != nil {
		return fmt.Errorf("failed to navigate: %w", err)
	}
	return ErrNotImplemented
}

func selectDay(day string) []chromedp.Action {
	// Construct XPath based on the day abbreviation (L, M, X, J, V, S, D)
	xpath := fmt.Sprintf(`//a[@class="dia" or contains(@class, "current")]/span[text()='%s']/parent::a`, day)

	return []chromedp.Action{
		chromedp.Sleep(1 * time.Second),

		// Wait for the day to be visible
		chromedp.WaitVisible(xpath),
		// Click on the specified day
		chromedp.Click(xpath),
	}
}

func (c *Client) RemoveBooking(day, hour string) error {
	if err := chromedp.Run(c.ctx,
		chromedp.Navigate(c.baseURL+"/schedule"),
	); err != nil {
		return fmt.Errorf("failed to navigate: %w", err)
	}
	return ErrNotImplemented
}

// SaveCookies extracts and stores cookies from the current browser session
func (c *Client) SaveCookies() error {
	var cookies []*http.Cookie
	err := chromedp.Run(c.ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Get all cookies from the current page
			cookiesData, err := network.GetCookies().Do(ctx)
			if err != nil {
				return err
			}

			for _, cookie := range cookiesData {
				domain := cookie.Domain
				path := cookie.Path
				httpCookie := &http.Cookie{
					Name:     cookie.Name,
					Value:    cookie.Value,
					Domain:   domain,
					Path:     path,
					Expires:  time.Unix(int64(cookie.Expires), 0),
					Secure:   cookie.Secure,
					HttpOnly: cookie.HTTPOnly,
				}
				cookies = append(cookies, httpCookie)
			}
			return nil
		}),
	)

	if err != nil {
		return fmt.Errorf("failed to save cookies: %w", err)
	}

	c.cookies = cookies
	return nil
}

// RestoreCookies sets previously saved cookies in the browser session
func (c *Client) RestoreCookies() error {
	if len(c.cookies) == 0 {
		return nil // No cookies to restore
	}

	return chromedp.Run(c.ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			for _, cookie := range c.cookies {
				expires := cdp.TimeSinceEpoch(cookie.Expires)
				err := network.SetCookie(cookie.Name, cookie.Value).
					WithDomain(cookie.Domain).
					WithPath(cookie.Path).
					WithSecure(cookie.Secure).
					WithHTTPOnly(cookie.HttpOnly).
					WithExpires(&expires).
					Do(ctx)
				if err != nil {
					return err
				}
			}
			return nil
		}),
	)
}

// LoadStoredSession initializes client with cookies from storage and validates session
func (c *Client) LoadStoredSession(ctx context.Context, cookies []*http.Cookie) error {
	if len(cookies) == 0 {
		return fmt.Errorf("no cookies provided")
	}

	c.cookies = cookies

	// Restore cookies to browser context
	if err := c.RestoreCookies(); err != nil {
		return fmt.Errorf("failed to restore cookies: %w", err)
	}

	// Validate session by navigating to protected page
	err := chromedp.Run(c.ctx,
		chromedp.Navigate(c.baseURL+"/schedule"),
		chromedp.WaitVisible(`#calendar`, chromedp.ByQuery),
	)

	if err != nil {
		return fmt.Errorf("session validation failed: %w", err)
	}

	c.logger.Info("Session loaded and validated successfully")
	return nil
}

// GetCookies returns the current stored cookies
func (c *Client) GetCookies() []*http.Cookie {
	return c.cookies
}

// ClearCookies clears all stored cookies
func (c *Client) ClearCookies() {
	c.cookies = nil
}
