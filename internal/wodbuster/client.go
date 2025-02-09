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
			// Set a timeout for the entire browser context
			c.ctx, cancel = context.WithTimeout(c.ctx, 30*time.Second)
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
	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	
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

func (c *Client) Login(username, password string) error {
	if err := chromedp.Run(c.ctx,
		// Navigate to login page
		chromedp.Navigate(c.baseURL+"/login"),
		
		// Wait for form elements to be present
		chromedp.WaitVisible(`//input[@name="username"]`),
		chromedp.WaitVisible(`//input[@name="password"]`),
		
		// Fill in the form
		chromedp.SendKeys(`//input[@name="username"]`, username),
		chromedp.SendKeys(`//input[@name="password"]`, password),
		
		// Click login button
		chromedp.Click(`//button[@type="submit"]`),
		
		// Wait for redirect or success element
		chromedp.WaitVisible(`//div[contains(@class, "dashboard")]`, chromedp.BySearch),
	); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	c.logger.Info("Successfully logged in", 
		"username", username,
		"url", c.baseURL)
	return nil
}

func (c *Client) GetAvailableClasses() ([]ClassSchedule, error) {
	if err := chromedp.Run(c.ctx,
		chromedp.Navigate(c.baseURL+"/schedule"),
	); err != nil {
		return nil, fmt.Errorf("failed to navigate: %w", err)
	}
	return nil, ErrNotImplemented
}

func (c *Client) BookClass(day, hour string) error {
	if err := chromedp.Run(c.ctx,
		chromedp.Navigate(c.baseURL+"/schedule"),
	); err != nil {
		return fmt.Errorf("failed to navigate: %w", err)
	}
	return ErrNotImplemented
}

func (c *Client) RemoveBooking(day, hour string) error {
	if err := chromedp.Run(c.ctx,
		chromedp.Navigate(c.baseURL+"/schedule"),
	); err != nil {
		return fmt.Errorf("failed to navigate: %w", err)
	}
	return ErrNotImplemented
}
