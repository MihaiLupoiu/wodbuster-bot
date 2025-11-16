package wodbuster

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/chromedp/chromedp"
)

// LogIn authenticates the user with email and password
// Returns a session cookie on successful login or nil if session was restored from cookies
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

// login performs the login sequence
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

// rememberBrowser clicks the "Remember this browser" button after login
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

		chromedp.Evaluate(`document.getElementById('body_body_CtlConfiar_CtlNoSeguroConfianza').click()`, nil),

		chromedp.Sleep(3 * time.Second),
	}
}

func (c *Client) LoginOnly(email, password string) error {
	if err := chromedp.Run(c.ctx, login(c.baseURL, email, password)...); err != nil {
		return fmt.Errorf("failed to login: %w", err)
	}
	return nil
}

func (c *Client) NotRememberBrowser() error {
	if err := chromedp.Run(c.ctx, notRememberBrowser()...); err != nil {
		return fmt.Errorf("failed to not remember browser: %w", err)
	}
	return nil
}

func (c *Client) RememberBrowser() error {
	if err := chromedp.Run(c.ctx, rememberBrowser()...); err != nil {
		return fmt.Errorf("failed to remember browser: %w", err)
	}
	return nil
}
