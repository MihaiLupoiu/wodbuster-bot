package wodbuster

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/chromedp/chromedp"
)

type Client struct {
	ctx     context.Context
	cancel  context.CancelFunc
	logger  *slog.Logger
	baseURL string
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

func NewClient(baseURL string, opts ...Option) (*Client, error) {
	if baseURL == "" {
		return nil, ErrMissingBaseURL
	}

	// Create default client with background context
	ctx, cancel := chromedp.NewContext(context.Background())
	// TODO: Consider setting a timeout for the entire browser context
	//ctx, cancel = context.WithTimeout(ctx, 30*time.Second)

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

func (c *Client) Login(email, password string) error {
	if email == "" || password == "" {
		return fmt.Errorf("email and password are required")
	}

	// First navigate to the page using the main context
	if err := chromedp.Run(c.ctx,
		chromedp.Navigate("https://wodbuster.com"),
	); err != nil {
		return fmt.Errorf("failed to navigate to login page: %w", err)
	}

	// Create new context after navigation
	ctx, cancel := chromedp.NewContext(c.ctx,
		chromedp.WithNewBrowserContext(),
	)
	defer cancel()

	actions := login(c.baseURL, email, password)
	actions = append(actions, rememberBrowser()...)

	if err := chromedp.Run(ctx, actions...); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	c.logger.Info("Successfully logged in",
		"username", email,
		"url", c.baseURL)
	return nil
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

func (c *Client) BookClass(email, password string, day, hour string) error {
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
