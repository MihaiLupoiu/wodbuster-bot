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

// WithHeadlessMode configures chromedp for headless operation with anti-detection
func WithHeadlessMode(headless bool) Option {
	return func(c *Client) {
		// Create a new allocator context for this client
		allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(),
			append(chromedp.DefaultExecAllocatorOptions[:],
				chromedp.Flag("headless", headless),
				chromedp.Flag("no-sandbox", true),
				chromedp.Flag("disable-gpu", true),
				chromedp.Flag("disable-dev-shm-usage", true),
				chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
				chromedp.Flag("disable-blink-features", "AutomationControlled"),
				chromedp.Flag("disable-web-security", true),
				chromedp.Flag("disable-features", "TranslateUI"),
			)...)

		// Create browser context with timeout
		ctx, cancel := chromedp.NewContext(allocCtx)
		ctx, timeoutCancel := context.WithTimeout(ctx, 60*time.Second)

		// Cancel the old context if it exists
		if c.cancel != nil {
			c.cancel()
		}

		c.ctx = ctx
		c.cancel = func() {
			timeoutCancel()
			cancel()
			allocCancel()
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
				chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
				chromedp.Flag("disable-blink-features", "AutomationControlled"),
				chromedp.Flag("disable-web-security", true),
				chromedp.Flag("disable-features", "TranslateUI"),
			)...)

		// Create browser context with timeout
		ctx, cancel := chromedp.NewContext(allocCtx)
		ctx, timeoutCancel := context.WithTimeout(ctx, 60*time.Second)

		// Cancel the old context if it exists
		if c.cancel != nil {
			c.cancel()
		}

		c.ctx = ctx
		c.cancel = func() {
			timeoutCancel()
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
	ctx, timeoutCancel := context.WithTimeout(ctx, 60*time.Second)

	client := &Client{
		ctx:     ctx,
		cancel:  func() { timeoutCancel(); cancel() },
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

func (c *Client) LogIn(ctx context.Context, email, password string) (*http.Cookie, error) {
	if email == "" || password == "" {
		return nil, fmt.Errorf("email and password are required")
	}

	// Check if we have valid session cookies first
	if len(c.cookies) > 0 {
		if err := c.RestoreCookies(); err != nil {
			c.logger.Warn("Failed to restore cookies, proceeding with fresh login", "error", err)
		} else {
			// Test if session is still valid by navigating to a protected page
			if err := chromedp.Run(c.ctx, chromedp.Navigate(c.baseURL+"/schedule")); err == nil {
				c.logger.Info("Session restored successfully", "username", email)
				return nil, nil
			}
		}
	}

	actions := login(c.baseURL, email, password)
	actions = append(actions, rememberBrowser()...)

	if err := chromedp.Run(c.ctx, actions...); err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}

	// Save session cookies for future use
	if err := c.SaveCookies(); err != nil {
		c.logger.Warn("Failed to save session cookies", "error", err)
		return nil, fmt.Errorf("failed to save session cookies: %w", err)
	}

	// Extract main session cookie to return
	sessionCookie := c.extractMainSessionCookie()
	if sessionCookie == nil {
		c.logger.Warn("No session cookie found after successful login")
		return nil, fmt.Errorf("no session cookie found after login")
	}

	c.logger.Info("Successfully logged in",
		"username", email,
		"url", c.baseURL,
		"session_cookie_found", true)
	return sessionCookie, nil
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
		chromedp.WaitVisible(`//div[@id="body_body_CtlUp"]`),
		chromedp.Sleep(2 * time.Second),

		// Use the exact JavaScript approach that worked
		chromedp.Evaluate(`document.getElementById('body_body_CtlConfiar_CtlSeguro').click()`, nil),

		chromedp.Sleep(3 * time.Second),
	}
}

func notRememberBrowser() []chromedp.Action {
	return []chromedp.Action{
		chromedp.WaitVisible(`//div[@id="body_body_CtlUp"]`),
		chromedp.Sleep(2 * time.Second),

		// Use the exact JavaScript approach that worked
		chromedp.Evaluate(`document.getElementById('body_body_CtlConfiar_CtlNoSeguroConfianza').click()`, nil),

		chromedp.Sleep(3 * time.Second),
	}
}

// parseClassNode extracts class information from a DOM node
func parseClassNode(ctx context.Context, node *cdp.Node) (*ClassSchedule, error) {
	if node == nil {
		return nil, fmt.Errorf("node is nil")
	}

	var classTypeStr, hour string
	var hasReservarButton bool

	// Get class type from h3.entrenamiento
	if err := chromedp.Text(`.//h3[contains(@class, 'entrenamiento')]`, &classTypeStr, chromedp.BySearch, chromedp.FromNode(node)).Do(ctx); err != nil {
		return nil, fmt.Errorf("failed to get class type: %w", err)
	}

	// Get hour from div.hora
	if err := chromedp.Text(`.//div[@class='hora']`, &hour, chromedp.BySearch, chromedp.FromNode(node)).Do(ctx); err != nil {
		return nil, fmt.Errorf("failed to get hour: %w", err)
	}

	// Check if "Reservar" button exists (class is available)
	var nodes []*cdp.Node
	if err := chromedp.Nodes(`.//button[contains(@class, 'entrenar') and contains(., 'Reservar')]`, &nodes, chromedp.BySearch, chromedp.FromNode(node)).Do(ctx); err == nil {
		hasReservarButton = len(nodes) > 0
	}

	// Clean up class type (remove extra whitespace and asterisks)
	classTypeStr = cleanClassType(classTypeStr)

	return &ClassSchedule{
		Day:       "", // Day needs to be set from context
		Hour:      hour,
		ClassType: ClassType(classTypeStr),
		Available: hasReservarButton,
	}, nil
}

// cleanClassType removes extra whitespace and special characters from class type
func cleanClassType(classType string) string {
	// Remove leading/trailing whitespace
	classType = trimSpace(classType)
	// Remove asterisks that indicate additional info
	if len(classType) > 0 && classType[len(classType)-1] == '*' {
		classType = classType[:len(classType)-1]
	}
	return trimSpace(classType)
}

// trimSpace removes leading and trailing whitespace
func trimSpace(s string) string {
	start := 0
	end := len(s)

	// Find first non-space character
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}

	// Find last non-space character
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}

	return s[start:end]
}

// GetAvailableClasses retrieves all available classes for booking
// day: Day abbreviation string (e.g., "L" for Monday, "X" for Wednesday)
func (c *Client) GetAvailableClasses(email, password string, day string) ([]ClassSchedule, error) {
	c.logger.Info("Getting available classes", "day", day)

	actions := login(c.baseURL, email, password)
	actions = append(actions, rememberBrowser()...)
	actions = append(actions, getAvailableClasses()...)

	if day != "" {
		actions = append(actions, selectDay(day)...)
	}

	actions = append(actions,
		// Wait for the classes to load
		chromedp.Sleep(2*time.Second))

	if err := chromedp.Run(c.ctx, actions...); err != nil {
		return nil, fmt.Errorf("failed to navigate: %w", err)
	}

	// Parse available classes from the page
	var classes []ClassSchedule
	err := chromedp.Run(c.ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			var nodes []*cdp.Node
			if err := chromedp.Nodes(`//div[contains(@class, 'clase')]`, &nodes, chromedp.BySearch).Do(ctx); err != nil {
				return err
			}

			for _, node := range nodes {
				class, err := parseClassNode(ctx, node)
				if err != nil {
					c.logger.Warn("Failed to parse class node", "error", err)
					continue
				}
				if class != nil {
					classes = append(classes, *class)
				}
			}
			return nil
		}),
	)

	if err != nil {
		return nil, fmt.Errorf("failed to parse classes: %w", err)
	}

	c.logger.Info("Found available classes", "count", len(classes))
	return classes, nil
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

// bookClass finds and clicks the "Reservar" button for a specific class type and hour
// classType examples: "Wod", "Open box", "HYROX", etc.
// hour examples: "07:00", "08:00", "19:30", etc.
func bookClass(classType string, hour string) []chromedp.Action {
	// Construct XPath to find the "Reservar" button based on both class type and hour
	// Structure: div.clase > div.entrenamientoHead > div.namehour > (h3.entrenamiento + div.hora)
	// Then find the button in div.actionsjs > button.entrenar
	xpathReserve := fmt.Sprintf(
		`//div[contains(@class, 'clase')]//div[@class='namehour'][.//h3[contains(@class, 'entrenamiento') and contains(normalize-space(text()), '%s')] and .//div[@class='hora' and text()='%s']]/ancestor::div[contains(@class, 'clase')]//button[contains(@class, 'entrenar') and contains(., 'Reservar')]`,
		classType, hour,
	)

	return []chromedp.Action{
		// Wait for the "Reservar" button to appear
		chromedp.WaitVisible(xpathReserve, chromedp.BySearch),
		// Click the "Reservar" button
		chromedp.Click(xpathReserve, chromedp.BySearch),

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

// BookClass books a class for a specific day, class type, and hour
// day: Day abbreviation string (e.g., "L", "M", "X", "J", "V", "S", "D")
//
//	You can use the Day constants (DayMonday, DayTuesday, etc.) or plain strings
//
// classType: Class type string (e.g., "Wod", "Open box", "HYROX")
//
//	You can use the ClassType constants (ClassTypeWod, ClassTypeOpenBox, etc.) or plain strings
//
// hour: Time in format "HH:MM" (e.g., "07:00", "19:30")
func (c *Client) BookClass(_ context.Context, email, password string, day, classType, hour string) error {
	if day == "" || classType == "" || hour == "" {
		return fmt.Errorf("day, classType, and hour are required")
	}

	c.logger.Info("Starting class booking",
		"day", day,
		"classType", classType,
		"hour", hour)

	actions := login(c.baseURL, email, password)
	actions = append(actions, rememberBrowser()...)
	actions = append(actions, getAvailableClasses()...)
	actions = append(actions, selectDay(day)...)
	actions = append(actions, bookClass(classType, hour)...)
	actions = append(actions, acceptConfirmation()...)
	actions = append(actions,
		// Wait for the confirmation to process
		chromedp.Sleep(3*time.Second))

	if err := chromedp.Run(c.ctx, actions...); err != nil {
		c.logger.Error("Failed to book class",
			"error", err,
			"day", day,
			"classType", classType,
			"hour", hour)
		return fmt.Errorf("failed to book class: %w", err)
	}

	c.logger.Info("Successfully booked class",
		"day", day,
		"classType", classType,
		"hour", hour)

	return nil
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

// extractMainSessionCookie finds the main WODBuster session cookie from stored cookies
func (c *Client) extractMainSessionCookie() *http.Cookie {
	sessionCookieNames := []string{".WBAuth"}

	for _, cookie := range c.cookies {
		for _, name := range sessionCookieNames {
			if cookie.Name == name && cookie.Value != "" {
				return cookie
			}
		}
	}
	return nil
}
