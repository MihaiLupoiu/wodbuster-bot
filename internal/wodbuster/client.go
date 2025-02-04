package wodbuster

import (
	"context"
	"log/slog"
	"time"

	"github.com/chromedp/chromedp"
)

type Client struct {
	ctx    context.Context
	cancel context.CancelFunc
	logger *slog.Logger
	baseURL string
}

func NewClient(logger *slog.Logger, baseURL string) (*Client, error) {
	if baseURL == "" {
		return nil, ErrMissingBaseURL
	}

	// Create a new chrome instance
	ctx, cancel := chromedp.NewContext(
		context.Background(),
		// chromedp.WithDebugf(log.Printf), // Uncomment for debug logs
	)

	// Set a timeout for the entire browser context
	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)

	return &Client{
		ctx:     ctx,
		cancel:  cancel,
		logger:  logger,
		baseURL: baseURL,
	}, nil
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
		chromedp.Navigate(c.baseURL+"/login"),
	); err != nil {
		return fmt.Errorf("failed to navigate: %w", err)
	}
	return ErrNotImplemented
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
