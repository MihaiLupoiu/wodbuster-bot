package wodbuster

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

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
