package wodbuster

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/chromedp/chromedp"
)

// Client represents a WODBuster web automation client
type Client struct {
	ctx     context.Context
	cancel  context.CancelFunc
	logger  *slog.Logger
	baseURL string
	cookies []*http.Cookie // Store session cookies
}

// Option defines the method to customize the Client.
type Option func(*Client)

var (
	ErrMissingBaseURL = errors.New("base URL is required")
	ErrNotImplemented = errors.New("method not implemented")
)

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

// NewClient creates a new WODBuster client with the given base URL and options
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

// Close closes the client and cleans up resources
func (c *Client) Close() {
	c.cancel()
}
