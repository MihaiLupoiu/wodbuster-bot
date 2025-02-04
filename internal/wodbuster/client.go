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
}

func NewClient(logger *slog.Logger) (*Client, error) {
	// Create a new chrome instance
	ctx, cancel := chromedp.NewContext(
		context.Background(),
		// chromedp.WithDebugf(log.Printf), // Uncomment for debug logs
	)

	// Set a timeout for the entire browser context
	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)

	return &Client{
		ctx:    ctx,
		cancel: cancel,
		logger: logger,
	}, nil
}

func (c *Client) Close() {
	c.cancel()
}

func (c *Client) Login(username, password string) error {
	return chromedp.Run(c.ctx,
		chromedp.Navigate("https://wodbuster.com/login"),
		// TODO: Add actual login steps
	)
}

func (c *Client) GetAvailableClasses() ([]ClassSchedule, error) {
	var classes []ClassSchedule
	err := chromedp.Run(c.ctx,
		chromedp.Navigate("https://wodbuster.com/schedule"),
		// TODO: Add actual schedule scraping steps
	)
	return classes, err
}

func (c *Client) BookClass(day, hour string) error {
	return chromedp.Run(c.ctx,
		chromedp.Navigate("https://wodbuster.com/schedule"),
		// TODO: Add actual booking steps
	)
}

func (c *Client) RemoveBooking(day, hour string) error {
	return chromedp.Run(c.ctx,
		chromedp.Navigate("https://wodbuster.com/schedule"),
		// TODO: Add actual booking removal steps
	)
}
